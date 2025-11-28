package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/strvals"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/applyer"
)

const MaxConcurrentReconciles = 5

const (
	FinalizerName = apps.GroupName + "/finalizer"
)

func Setup(ctx context.Context, mgr ctrl.Manager, options *Options) error {
	cfg, cli := mgr.GetConfig(), mgr.GetClient()
	r := &InstanceReconciler{
		Client:  cli,
		Scheme:  mgr.GetScheme(),
		Applier: applyer.NewDefaultApply(cfg, cli, &applyer.Options{CacheDir: options.CacheDir}),
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Instance{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: MaxConcurrentReconciles}).
		WatchesRawSource(
			source.TypedKind(mgr.GetCache(), &corev1.ConfigMap{}, ValueFromEventHandler[*corev1.ConfigMap](cli)),
		).
		WatchesRawSource(
			source.TypedKind(mgr.GetCache(), &corev1.Secret{}, ValueFromEventHandler[*corev1.Secret](cli)),
		).
		Complete(r)
}

func ValueFromEventHandler[T client.Object](cli client.Client) handler.TypedEventHandler[T, reconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(handler.TypedMapFunc[T, reconcile.Request](func(ctx context.Context, obj T) []reconcile.Request {
		instances := &appsv1.InstanceList{}
		_ = cli.List(ctx, instances, client.InNamespace(obj.GetNamespace()))
		kind := obj.GetObjectKind().GroupVersionKind().Kind
		var requests []reconcile.Request
		for _, b := range instances.Items {
			for _, ref := range b.Spec.ValuesFrom {
				if strings.EqualFold(ref.Kind, kind) && ref.Name == obj.GetName() {
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&b)})
				}
			}
		}
		return requests
	}))
}

type InstanceReconciler struct {
	Client  client.Client
	Scheme  *runtime.Scheme
	Applier *applyer.BundleApplier
}

func (r *InstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	instance := &appsv1.Instance{}
	if err := r.Client.Get(ctx, req.NamespacedName, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	original := instance.DeepCopy()

	// check the object is being deleted then remove the finalizer
	if instance.DeletionTimestamp != nil {
		// remove
		if err := r.Remove(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.RemoveFinalizer(instance, FinalizerName) {
			log.Info("remove finalizer")
			if err := r.Client.Update(ctx, instance); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if reflect.DeepEqual(original.Status, instance.Status) {
			return ctrl.Result{}, nil
		}
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}
	if instance.DeletionTimestamp == nil && controllerutil.AddFinalizer(instance, FinalizerName) {
		log.Info("add finalizer")
		if err := r.Client.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	// sync
	err := r.Sync(ctx, instance)
	if err != nil {
		instance.Status.Phase = appsv1.PhaseFailed
		instance.Status.Message = err.Error()
	}
	// update status if updated whenever the sync has error or not
	if !reflect.DeepEqual(original.Status, instance.Status) {
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, err
}

func (r *InstanceReconciler) Sync(ctx context.Context, instance *appsv1.Instance) error {
	// check all dependencies are installed
	if err := r.checkDepenency(ctx, instance); err != nil {
		return err
	}
	// resolve valuesRef
	if err := r.resolveValuesRef(ctx, instance); err != nil {
		return err
	}
	return r.Applier.Apply(ctx, instance)
}

type DependencyError struct {
	Reason string
	Object corev1.ObjectReference
}

func (e DependencyError) Error() string {
	return fmt.Sprintf("dependency %s/%s :%s", e.Object.Namespace, e.Object.Name, e.Reason)
}

func (r *InstanceReconciler) checkDepenency(ctx context.Context, instance *appsv1.Instance) error {
	for _, dep := range instance.Spec.Dependencies {
		if dep.Name == "" {
			continue
		}
		if dep.Namespace == "" {
			dep.Namespace = instance.Namespace
		}
		if dep.Kind == "" {
			dep.APIVersion = instance.APIVersion
			dep.Kind = instance.Kind
		}
		gvk := schema.FromAPIVersionAndKind(dep.APIVersion, dep.Kind)
		newobj, _ := r.Scheme.New(gvk)
		depobj, ok := newobj.(client.Object)
		if !ok {
			depobj = &metav1.PartialObjectMetadata{
				TypeMeta: metav1.TypeMeta{
					APIVersion: gvk.GroupVersion().String(),
					Kind:       dep.Kind,
				},
			}
		}

		// exists check
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: dep.Namespace, Name: dep.Name}, depobj); err != nil {
			if apierrors.IsNotFound(err) {
				return DependencyError{Reason: err.Error(), Object: dep}
			}
			return err
		}

		// status check
		switch obj := depobj.(type) {
		case *appsv1.Instance:
			if obj.Status.Phase != appsv1.PhaseInstalled {
				return DependencyError{Reason: "not installed", Object: dep}
			}
		}
	}
	return nil
}

func (r *InstanceReconciler) resolveValuesRef(ctx context.Context, instance *appsv1.Instance) error {
	base := map[string]any{}

	for _, ref := range instance.Spec.ValuesFrom {
		switch strings.ToLower(ref.Kind) {
		case "secret":
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: instance.Namespace}}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
				if apierrors.IsNotFound(err) && ref.Optional {
					continue
				}
				return err
			}
			// --set
			for k, v := range secret.Data {
				if err := mergeInto(ref.Prefix+k, string(v), base); err != nil {
					return fmt.Errorf("parse %#v key[%s]: %w", ref, k, err)
				}
			}
		case "configmap":
			configmap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: instance.Namespace}}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(configmap), configmap); err != nil {
				if apierrors.IsNotFound(err) && ref.Optional {
					continue
				}
				return err
			}
			// -f/--values
			for k, v := range configmap.BinaryData {
				currentMap := map[string]any{}
				if err := yaml.Unmarshal(v, &currentMap); err != nil {
					return fmt.Errorf("parse %#v key[%s]: %w", ref, k, err)
				}
				base = mergeMaps(base, currentMap)
			}
			// --set
			for k, v := range configmap.Data {
				if err := mergeInto(ref.Prefix+k, string(v), base); err != nil {
					return fmt.Errorf("parse %#v key[%s]: %w", ref, k, err)
				}
			}
		default:
			return fmt.Errorf("valuesRef kind [%s] is not supported", ref.Kind)
		}
	}

	// inlined values
	base = mergeMaps(base, instance.Spec.Values.Object)
	instance.Spec.Values = appsv1.Values{Object: base}
	return nil
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if v, ok := v.(map[string]any); ok {
			if bv, ok := out[k]; ok {
				if bv, ok := bv.(map[string]any); ok {
					out[k] = mergeMaps(bv, v)
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

func mergeInto(k, v string, base map[string]any) error {
	if err := strvals.ParseInto(fmt.Sprintf("%s=%s", k, v), base); err != nil {
		return fmt.Errorf("parse %#v key[%s]: %w", k, v, err)
	}
	return nil
}

func (r *InstanceReconciler) Remove(ctx context.Context, instance *appsv1.Instance) error {
	return r.Applier.Remove(ctx, instance)
}
