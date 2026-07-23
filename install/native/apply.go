package native

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
)

type DiffResult struct {
	Creats  []*unstructured.Unstructured
	Applys  []*unstructured.Unstructured
	Removes []*unstructured.Unstructured
}

func DiffWithDefaultNamespace(
	cli client.Client,
	defaultnamespace string,
	managed []appsv1.ManagedResource,
	resources []*unstructured.Unstructured,
) DiffResult {
	CorrectNamespaces(cli, defaultnamespace, resources)
	CorrectNamespacesForRefrences(cli, defaultnamespace, managed)
	return Diff(managed, resources)
}

func Diff(managed []appsv1.ManagedResource, resources []*unstructured.Unstructured) DiffResult {
	result := DiffResult{}
	managedmap := map[appsv1.ManagedResource]bool{}
	for _, item := range managed {
		managedmap[item] = false
	}
	for _, item := range resources {
		man := appsv1.GetReference(item)
		if _, ok := managedmap[man]; !ok {
			result.Creats = append(result.Creats, item)
		} else {
			result.Applys = append(result.Applys, item)
		}
		managedmap[man] = true
	}
	for k, v := range managedmap {
		if !v {
			uns := &unstructured.Unstructured{}
			uns.SetAPIVersion(k.APIVersion)
			uns.SetKind(k.Kind)
			uns.SetName(k.Name)
			uns.SetNamespace(k.Namespace)
			result.Removes = append(result.Removes, uns)
		}
	}
	return result
}

func NewDefaultSyncOptions() *SyncOptions {
	return &SyncOptions{
		ServerSideApply: true,
		CreateNamespace: true,
		CleanCRD:        false,
		DeleteTimeout:   2 * time.Minute,
	}
}

type SyncOptions struct {
	ServerSideApply bool
	CreateNamespace bool
	CleanCRD        bool
	// DeleteTimeout bounds foreground deletion for resources using the
	// Recreate upgrade strategy.
	DeleteTimeout time.Duration
}

type ClientApply struct {
	Client client.Client
}

func (a *ClientApply) Sync(ctx context.Context,
	defaultnamespace string,
	managed []appsv1.ManagedResource,
	resources []*unstructured.Unstructured,
	options *SyncOptions,
) ([]appsv1.ManagedResource, error) {
	return a.SyncDiff(
		ctx,
		DiffWithDefaultNamespace(
			a.Client,
			defaultnamespace,
			managed,
			resources,
		),
		options)
}

func (a *ClientApply) SyncDiff(ctx context.Context, diff DiffResult, options *SyncOptions) ([]appsv1.ManagedResource, error) {
	log := logr.FromContextOrDiscard(ctx)
	if options == nil {
		options = NewDefaultSyncOptions()
	}

	// Resolve removed resources from the API before validating. Diff only has
	// references for removed resources, while remove strategy is stored on the
	// live object. Do the complete validation pass before any mutation.
	for _, item := range append(append([]*unstructured.Unstructured{}, diff.Creats...), diff.Applys...) {
		if err := install.ValidateLifecycleStrategies(item); err != nil {
			return nil, err
		}
	}
	for i, item := range diff.Removes {
		live := item.DeepCopy()
		if err := a.Client.Get(ctx, client.ObjectKeyFromObject(item), live); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("get removed resource %s %s/%s: %w",
				item.GroupVersionKind().String(), item.GetNamespace(), item.GetName(), err)
		}
		if err := install.ValidateLifecycleStrategies(live); err != nil {
			return nil, err
		}
		diff.Removes[i] = live
	}

	errs := []string{}

	managed := []appsv1.ManagedResource{}
	// create
	for _, item := range diff.Creats {
		log.Info("creating resource", "resource", item.GetObjectKind().GroupVersionKind().String(), "name", item.GetName(), "namespace", item.GetNamespace())
		if options.CreateNamespace {
			a.createNsIfNotExists(ctx, item.GetNamespace())
		}
		if err := ApplyResource(ctx, a.Client, item, ApplyOptions{ServerSideApply: options.ServerSideApply}); err != nil {
			err = fmt.Errorf("%s %s/%s: %v", item.GetObjectKind().GroupVersionKind().String(), item.GetNamespace(), item.GetName(), err)
			log.Error(err, "creating resource")
			errs = append(errs, err.Error())
			continue
		}
		managed = append(managed, appsv1.GetReference(item)) // set managed
	}

	// apply
	for _, item := range diff.Applys {
		managed = append(managed, appsv1.GetReference(item)) // set managed

		if IsSkipUpdate(item) {
			log.Info("ignoring update", "resource", item.GetObjectKind().GroupVersionKind().String(), "name", item.GetName(), "namespace", item.GetNamespace())
			continue
		}
		if IsRecreateUpdate(item) {
			log.Info("recreating resource", "resource", item.GetObjectKind().GroupVersionKind().String(), "name", item.GetName(), "namespace", item.GetNamespace())
			if options.CreateNamespace {
				a.createNsIfNotExists(ctx, item.GetNamespace())
			}
			if err := a.recreateResource(ctx, item, options.DeleteTimeout); err != nil {
				err = fmt.Errorf("%s %s/%s: %v", item.GetObjectKind().GroupVersionKind().String(), item.GetNamespace(), item.GetName(), err)
				log.Error(err, "recreating resource")
				errs = append(errs, err.Error())
			}
			continue
		}

		log.Info("applying resource", "resource", item.GetObjectKind().GroupVersionKind().String(), "name", item.GetName(), "namespace", item.GetNamespace())
		if options.CreateNamespace {
			a.createNsIfNotExists(ctx, item.GetNamespace())
		}
		if err := ApplyResource(ctx, a.Client, item, ApplyOptions{ServerSideApply: options.ServerSideApply}); err != nil {
			err = fmt.Errorf("%s %s/%s: %v", item.GetObjectKind().GroupVersionKind().String(), item.GetNamespace(), item.GetName(), err)
			log.Error(err, "applying resource")
			errs = append(errs, err.Error())
			continue
		}
	}
	// remove
	for _, item := range diff.Removes {
		if IsCRD(item) && !options.CleanCRD {
			continue
		}
		if IsSkipDelete(item) {
			log.Info("ignoring delete", "resource", item.GetObjectKind().GroupVersionKind().String(), "name", item.GetName(), "namespace", item.GetNamespace())
			continue
		}
		partial := item
		log.Info("deleting resource", "resource", partial.GetObjectKind().GroupVersionKind().String(), "name", partial.GetName(), "namespace", partial.GetNamespace())
		if err := a.Client.Delete(ctx, partial, &client.DeleteOptions{}); err != nil {
			if !apierrors.IsNotFound(err) {
				err = fmt.Errorf("%s %s/%s: %v", partial.GetObjectKind().GroupVersionKind().String(), partial.GetNamespace(), partial.GetName(), err)
				log.Error(err, "deleting resource")
				errs = append(errs, err.Error())
				// if not removed, keep in managed
				managed = append(managed, appsv1.GetReference(item)) // set managed
				continue
			}
		}
	}

	// sort manged
	sort.Slice(managed, func(i, j int) bool {
		return strings.Compare(managed[i].APIVersion, managed[j].APIVersion) == 1
	})
	if len(errs) > 0 {
		return managed, errors.New(strings.Join(errs, "\n"))
	} else {
		return managed, nil
	}
}

func (a *ClientApply) recreateResource(ctx context.Context, obj *unstructured.Unstructured, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	live := obj.DeepCopy()
	err := a.Client.Get(ctx, client.ObjectKeyFromObject(obj), live)
	switch {
	case apierrors.IsNotFound(err):
		return a.Client.Create(ctx, obj)
	case err != nil:
		return fmt.Errorf("get before recreate: %w", err)
	}

	propagation := metav1.DeletePropagationForeground
	if err := a.Client.Delete(ctx, live, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete before recreate: %w", err)
	}
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, timeout, true, func(ctx context.Context) (bool, error) {
		check := obj.DeepCopy()
		err := a.Client.Get(ctx, client.ObjectKeyFromObject(obj), check)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}); err != nil {
		return fmt.Errorf("wait for foreground deletion: %w", err)
	}
	obj.SetResourceVersion("")
	obj.SetUID("")
	return a.Client.Create(ctx, obj)
}

func (a *ClientApply) createNsIfNotExists(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := controllerutil.CreateOrUpdate(ctx, a.Client, ns, func() error { return nil })
	return err
}

type ApplyOptions struct {
	ServerSideApply bool
	FieldOwner      string
}

func ApplyResource(ctx context.Context, cli client.Client, obj client.Object, options ApplyOptions) error {
	if options.FieldOwner == "" {
		options.FieldOwner = "bundler"
	}

	exists, _ := obj.DeepCopyObject().(client.Object)
	if err := cli.Get(ctx, client.ObjectKeyFromObject(exists), exists); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return cli.Create(ctx, obj)
	}

	var patch client.Patch
	var patchoptions []client.PatchOption
	if options.ServerSideApply {
		obj.SetManagedFields(nil)
		patch = client.Apply
		patchoptions = append(patchoptions,
			client.FieldOwner(options.FieldOwner),
			client.ForceOwnership,
		)
	} else {
		patch = client.StrategicMergeFrom(exists)
	}

	// patch
	if err := cli.Patch(ctx, obj, patch, patchoptions...); err != nil {
		return err
	}
	return nil
}

func IsSkipUpdate(obj client.Object) bool {
	strategy, err := install.UpgradeStrategy(obj)
	return err == nil && strategy == install.UpgradeStrategyRetain
}

func IsRecreateUpdate(obj client.Object) bool {
	strategy, err := install.UpgradeStrategy(obj)
	return err == nil && strategy == install.UpgradeStrategyRecreate
}

func IsSkipDelete(obj client.Object) bool {
	strategy, err := install.RemoveStrategy(obj)
	return err == nil && strategy == install.RemoveStrategyRetain
}

func IsCRD(obj client.Object) bool {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return gvk.Group == "apiextensions.k8s.io" && gvk.Kind == "CustomResourceDefinition"
}

func CorrectNamespaces[T client.Object](cli client.Client, defaultNamespace string, list []T) {
	for i, item := range list {
		scopeName, err := NamespacedScopeOf(cli, item)
		if err != nil {
			continue
		}
		switch {
		case scopeName == apimeta.RESTScopeNameNamespace && item.GetNamespace() == "":
			item.SetNamespace(defaultNamespace)
		case scopeName == apimeta.RESTScopeNameRoot && item.GetNamespace() != "":
			item.SetNamespace("")
		}
		list[i] = item
	}
}

func CorrectNamespacesForRefrences(cli client.Client, defaultns string, list []appsv1.ManagedResource) {
	for i, val := range list {
		scopeName, err := NamespacedScopeOfGVK(cli, val.GroupVersionKind())
		if err != nil {
			continue
		}
		switch {
		case scopeName == apimeta.RESTScopeNameNamespace && val.Namespace == "":
			val.Namespace = defaultns
		case scopeName == apimeta.RESTScopeNameRoot && val.Namespace != "":
			val.Namespace = ""
		}
		list[i] = val
	}
}

func NamespacedScopeOfGVK(cli client.Client, gvk schema.GroupVersionKind) (apimeta.RESTScopeName, error) {
	restmapping, err := cli.RESTMapper().RESTMapping(gvk.GroupKind())
	if err != nil {
		return "", fmt.Errorf("failed to get restmapping: %w", err)
	}
	return restmapping.Scope.Name(), nil
}

func NamespacedScopeOf(cli client.Client, obj runtime.Object) (apimeta.RESTScopeName, error) {
	gvk, err := apiutil.GVKForObject(obj, cli.Scheme())
	if err != nil {
		return "", err
	}
	return NamespacedScopeOfGVK(cli, gvk)
}
