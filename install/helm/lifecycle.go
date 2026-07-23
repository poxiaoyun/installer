package helm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/postrender"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/utils"
)

const (
	helmResourcePolicyAnnotation = "helm.sh/resource-policy"
	helmKeepPolicy               = "keep"
)

// lifecyclePostRenderer validates lifecycle annotations and translates the
// installer remove policy into Helm's native resource keep policy. It is
// intentionally the final renderer so policies added by another renderer are
// handled as well.
type lifecyclePostRenderer struct {
	next postrender.PostRenderer
}

func newLifecyclePostRenderer(next postrender.PostRenderer) postrender.PostRenderer {
	return &lifecyclePostRenderer{next: next}
}

func (r *lifecyclePostRenderer) Run(in *bytes.Buffer) (*bytes.Buffer, error) {
	if r.next != nil {
		var err error
		in, err = r.next.Run(in)
		if err != nil {
			return nil, err
		}
	}
	objects, err := utils.SplitYAML(in.Bytes())
	if err != nil {
		return nil, err
	}
	for _, object := range objects {
		if err := install.ValidateLifecycleStrategies(object); err != nil {
			return nil, err
		}
		strategy, _ := install.RemoveStrategy(object)
		if strategy == install.RemoveStrategyRetain {
			annotations := object.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[helmResourcePolicyAnnotation] = helmKeepPolicy
			object.SetAnnotations(annotations)
		}
	}

	out := &bytes.Buffer{}
	for _, object := range objects {
		data, err := yaml.Marshal(object.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal lifecycle resource %s/%s: %w", object.GetNamespace(), object.GetName(), err)
		}
		out.WriteString("---\n")
		out.Write(data)
	}
	return out, nil
}

// lifecycleKubeClient adds resource-level Retain and Recreate behavior to
// Helm upgrades. Helm's global Force/Recreate option is deliberately not used.
type lifecycleKubeClient struct {
	kube.Interface
	timeout             time.Duration
	getLiveResourceInfo func(*resource.Info) (*resource.Info, error)
}

func newLifecycleKubeClient(delegate kube.Interface) *lifecycleKubeClient {
	return &lifecycleKubeClient{
		Interface:           delegate,
		timeout:             DefaultTimeout,
		getLiveResourceInfo: liveResourceInfo,
	}
}

func (c *lifecycleKubeClient) Create(resources kube.ResourceList) (*kube.Result, error) {
	if err := validateResourceList(resources); err != nil {
		return nil, err
	}
	return c.Interface.Create(resources)
}

func (c *lifecycleKubeClient) Update(original, target kube.ResourceList, force bool) (*kube.Result, error) {
	return c.update(original, target, force, false)
}

func (c *lifecycleKubeClient) UpdateThreeWayMerge(original, target kube.ResourceList, force bool) (*kube.Result, error) {
	return c.update(original, target, force, true)
}

func (c *lifecycleKubeClient) update(original, target kube.ResourceList, force, threeWayMerge bool) (*kube.Result, error) {
	regularUpdate := c.Interface.Update
	if threeWayMerge {
		delegate, ok := c.Interface.(kube.InterfaceThreeWayMerge)
		if !ok {
			return nil, errors.New("Kubernetes client does not support three-way merge updates")
		}
		regularUpdate = delegate.UpdateThreeWayMerge
	}

	// A complete validation pass must happen before the delegate mutates any
	// resources.
	if err := validateResourceList(target); err != nil {
		return nil, err
	}
	removed := original.Difference(target)
	for _, info := range removed {
		object, err := meta.Accessor(info.Object)
		if err != nil {
			return nil, fmt.Errorf("access metadata for %s: %w", info.String(), err)
		}
		if _, err := install.RemoveStrategy(object); err != nil {
			return nil, err
		}
	}

	retained := kube.ResourceList{}
	recreateChanged := kube.ResourceList{}
	recreateUnchanged := kube.ResourceList{}
	liveOriginals := kube.ResourceList{}
	for _, targetInfo := range target {
		originalInfo := original.Get(targetInfo)
		if originalInfo == nil {
			continue // strategies only affect an existing resource
		}
		strategy, _ := upgradeStrategy(targetInfo)
		switch strategy {
		case install.UpgradeStrategyRetain:
			retained = append(retained, targetInfo)
		case install.UpgradeStrategyRecreate:
			if apiequality.Semantic.DeepEqual(originalInfo.Object, targetInfo.Object) {
				recreateUnchanged = append(recreateUnchanged, targetInfo)
			} else {
				recreateChanged = append(recreateChanged, targetInfo)
			}
		default:
			originalObject, err := meta.Accessor(originalInfo.Object)
			if err != nil {
				return nil, fmt.Errorf("access metadata for %s: %w", originalInfo.String(), err)
			}
			// Read the old value only to detect a Retain transition. The target
			// was already validated above, so an invalid annotation in a legacy
			// release must not prevent the user from correcting it.
			if originalObject.GetAnnotations()[install.AnnotationUpgradeStrategy] == install.UpgradeStrategyRetain {
				liveInfo, err := c.getLiveResourceInfo(originalInfo)
				if err != nil {
					// A missing retained resource is handled by Helm's normal
					// Update path, which creates targets that no longer exist.
					if apierrors.IsNotFound(err) {
						continue
					}
					return nil, fmt.Errorf("get live resource after Retain for %s: %w", originalInfo.String(), err)
				}
				liveOriginals = append(liveOriginals, liveInfo)
			}
		}
	}

	excluded := append(append(kube.ResourceList{}, retained...), recreateChanged...)
	excluded = append(excluded, recreateUnchanged...)
	// Resources removed from the new manifest can only carry their policy in
	// the old release manifest. Excluding them from both lists keeps them live,
	// including for releases created before the Helm keep annotation mapping
	// was introduced.
	for _, info := range removed {
		object, _ := meta.Accessor(info.Object)
		strategy, _ := install.RemoveStrategy(object)
		if strategy == install.RemoveStrategyRetain {
			excluded = append(excluded, info)
		}
	}
	regularOriginal := original.Filter(func(info *resource.Info) bool { return !excluded.Contains(info) })
	regularTarget := target.Filter(func(info *resource.Info) bool { return !excluded.Contains(info) })
	for idx, originalInfo := range regularOriginal {
		if liveInfo := liveOriginals.Get(originalInfo); liveInfo != nil {
			regularOriginal[idx] = liveInfo
		}
	}

	result, err := regularUpdate(regularOriginal, regularTarget, force)
	if err != nil {
		return result, err
	}
	if result == nil {
		result = &kube.Result{}
	}
	if len(recreateChanged) == 0 {
		return result, nil
	}

	deleteResult, deleteErrors := c.deleteForeground(recreateChanged)
	if len(deleteErrors) != 0 {
		return result, fmt.Errorf("delete resources for recreate: %w", errors.Join(deleteErrors...))
	}
	if deleteResult != nil {
		result.Deleted = append(result.Deleted, deleteResult.Deleted...)
	}
	if err := c.waitForDelete(recreateChanged, c.timeout); err != nil {
		return result, fmt.Errorf("wait for resources to be deleted for recreate: %w", err)
	}
	createResult, err := c.Interface.Create(recreateChanged)
	if createResult != nil {
		result.Created = append(result.Created, createResult.Created...)
	}
	if err != nil {
		return result, fmt.Errorf("create resources after recreate deletion: %w", err)
	}
	return result, nil
}

func liveResourceInfo(info *resource.Info) (*resource.Info, error) {
	live := *info
	if err := live.Get(); err != nil {
		return nil, err
	}
	return &live, nil
}

func upgradeStrategy(info *resource.Info) (string, error) {
	object, err := meta.Accessor(info.Object)
	if err != nil {
		return "", err
	}
	return install.UpgradeStrategy(object)
}

func validateResourceList(resources kube.ResourceList) error {
	for _, info := range resources {
		object, err := meta.Accessor(info.Object)
		if err != nil {
			return fmt.Errorf("access metadata for %s: %w", info.String(), err)
		}
		if err := install.ValidateLifecycleStrategies(object); err != nil {
			return err
		}
	}
	return nil
}

func (c *lifecycleKubeClient) deleteForeground(resources kube.ResourceList) (*kube.Result, []error) {
	if delegate, ok := c.Interface.(kube.InterfaceDeletionPropagation); ok {
		return delegate.DeleteWithPropagationPolicy(resources, metav1.DeletePropagationForeground)
	}
	return nil, []error{errors.New("Kubernetes client does not support foreground deletion")}
}

func (c *lifecycleKubeClient) WaitForDelete(resources kube.ResourceList, timeout time.Duration) error {
	filtered, err := filterRemoveRetained(resources)
	if err != nil {
		return err
	}
	return c.waitForDelete(filtered, timeout)
}

func (c *lifecycleKubeClient) waitForDelete(resources kube.ResourceList, timeout time.Duration) error {
	if delegate, ok := c.Interface.(kube.InterfaceExt); ok {
		return delegate.WaitForDelete(resources, timeout)
	}
	return errors.New("Kubernetes client does not support waiting for deletion")
}

func (c *lifecycleKubeClient) Delete(resources kube.ResourceList) (*kube.Result, []error) {
	filtered, err := filterRemoveRetained(resources)
	if err != nil {
		return nil, []error{err}
	}
	return c.Interface.Delete(filtered)
}

func (c *lifecycleKubeClient) DeleteWithPropagationPolicy(resources kube.ResourceList, policy metav1.DeletionPropagation) (*kube.Result, []error) {
	filtered, err := filterRemoveRetained(resources)
	if err != nil {
		return nil, []error{err}
	}
	if delegate, ok := c.Interface.(kube.InterfaceDeletionPropagation); ok {
		return delegate.DeleteWithPropagationPolicy(filtered, policy)
	}
	return c.Interface.Delete(filtered)
}

func filterRemoveRetained(resources kube.ResourceList) (kube.ResourceList, error) {
	if err := validateResourceList(resources); err != nil {
		return nil, err
	}
	return resources.Filter(func(info *resource.Info) bool {
		object, _ := meta.Accessor(info.Object)
		strategy, _ := install.RemoveStrategy(object)
		return strategy != install.RemoveStrategyRetain
	}), nil
}

// Keep optional kube interfaces available after wrapping the Helm client.
func (c *lifecycleKubeClient) Get(resources kube.ResourceList, related bool) (map[string][]runtime.Object, error) {
	delegate, ok := c.Interface.(kube.InterfaceResources)
	if !ok {
		return nil, errors.New("Kubernetes client does not support getting resources")
	}
	return delegate.Get(resources, related)
}

func (c *lifecycleKubeClient) BuildTable(reader io.Reader, validate bool) (kube.ResourceList, error) {
	delegate, ok := c.Interface.(kube.InterfaceResources)
	if !ok {
		return nil, errors.New("Kubernetes client does not support building tables")
	}
	return delegate.BuildTable(reader, validate)
}

func (c *lifecycleKubeClient) GetPodList(namespace string, listOptions metav1.ListOptions) (*corev1.PodList, error) {
	delegate, ok := c.Interface.(kube.InterfaceLogs)
	if !ok {
		return nil, errors.New("Kubernetes client does not support listing pods for logs")
	}
	return delegate.GetPodList(namespace, listOptions)
}

func (c *lifecycleKubeClient) OutputContainerLogsForPodList(
	podList *corev1.PodList,
	namespace string,
	writerFunc func(namespace, pod, container string) io.Writer,
) error {
	delegate, ok := c.Interface.(kube.InterfaceLogs)
	if !ok {
		return errors.New("Kubernetes client does not support container logs")
	}
	return delegate.OutputContainerLogsForPodList(podList, namespace, writerFunc)
}

// Assert the interfaces Helm discovers dynamically.
var (
	_ kube.Interface                    = (*lifecycleKubeClient)(nil)
	_ kube.InterfaceExt                 = (*lifecycleKubeClient)(nil)
	_ kube.InterfaceThreeWayMerge       = (*lifecycleKubeClient)(nil)
	_ kube.InterfaceLogs                = (*lifecycleKubeClient)(nil)
	_ kube.InterfaceDeletionPropagation = (*lifecycleKubeClient)(nil)
	_ kube.InterfaceResources           = (*lifecycleKubeClient)(nil)
)
