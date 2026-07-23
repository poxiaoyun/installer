package helm

import (
	"bytes"
	"io"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/kube"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/utils"
)

func TestLifecyclePostRendererAddsHelmKeepPolicy(t *testing.T) {
	input := bytes.NewBufferString(`apiVersion: v1
kind: ConfigMap
metadata:
  name: retained
  annotations:
    app.kubernetes.io/remove-strategy: Retain
---
apiVersion: v1
kind: Secret
metadata:
  name: ordinary
`)

	out, err := newLifecyclePostRenderer(nil).Run(input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	objects, err := utils.SplitYAML(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 2 {
		t.Fatalf("objects = %d, want 2", len(objects))
	}
	if got := objects[0].GetAnnotations()[helmResourcePolicyAnnotation]; got != helmKeepPolicy {
		t.Fatalf("Helm resource policy = %q, want %q", got, helmKeepPolicy)
	}
	if _, exists := objects[1].GetAnnotations()[helmResourcePolicyAnnotation]; exists {
		t.Fatal("ordinary resource unexpectedly has Helm keep policy")
	}
}

func TestLifecyclePostRendererRejectsInvalidStrategy(t *testing.T) {
	input := bytes.NewBufferString(`apiVersion: v1
kind: ConfigMap
metadata:
  name: invalid
  annotations:
    app.kubernetes.io/upgrade-strategy: Replace
`)
	if _, err := newLifecyclePostRenderer(nil).Run(input); err == nil {
		t.Fatal("Run() error = nil, want invalid strategy error")
	}
}

type recordingHelmClient struct {
	kube.Interface
	updates         int
	updateOriginal  kube.ResourceList
	updateTarget    kube.ResourceList
	creates         kube.ResourceList
	deletes         kube.ResourceList
	waitsForDeletes kube.ResourceList
	deletePolicy    metav1.DeletionPropagation
	threeWayUpdates int
	logPodList      *corev1.PodList
	logNamespace    string
	logWrites       int
}

func (c *recordingHelmClient) Update(original, target kube.ResourceList, _ bool) (*kube.Result, error) {
	c.updates++
	c.updateOriginal = append(kube.ResourceList{}, original...)
	c.updateTarget = append(kube.ResourceList{}, target...)
	return &kube.Result{Updated: target}, nil
}

func (c *recordingHelmClient) UpdateThreeWayMerge(original, target kube.ResourceList, _ bool) (*kube.Result, error) {
	c.threeWayUpdates++
	c.updateOriginal = append(kube.ResourceList{}, original...)
	c.updateTarget = append(kube.ResourceList{}, target...)
	return &kube.Result{Updated: target}, nil
}

func (c *recordingHelmClient) Create(resources kube.ResourceList) (*kube.Result, error) {
	c.creates = append(c.creates, resources...)
	return &kube.Result{Created: resources}, nil
}

func (c *recordingHelmClient) DeleteWithPropagationPolicy(resources kube.ResourceList, policy metav1.DeletionPropagation) (*kube.Result, []error) {
	c.deletes = append(c.deletes, resources...)
	c.deletePolicy = policy
	return &kube.Result{Deleted: resources}, nil
}

func (c *recordingHelmClient) WaitForDelete(resources kube.ResourceList, _ time.Duration) error {
	c.waitsForDeletes = append(c.waitsForDeletes, resources...)
	return nil
}

func (c *recordingHelmClient) GetPodList(namespace string, _ metav1.ListOptions) (*corev1.PodList, error) {
	c.logNamespace = namespace
	if c.logPodList == nil {
		c.logPodList = &corev1.PodList{}
	}
	return c.logPodList, nil
}

func (c *recordingHelmClient) OutputContainerLogsForPodList(
	podList *corev1.PodList,
	namespace string,
	writerFunc func(namespace, pod, container string) io.Writer,
) error {
	c.logPodList = podList
	c.logNamespace = namespace
	if writerFunc != nil {
		_, _ = writerFunc(namespace, "pod", "container").Write([]byte("logs"))
		c.logWrites++
	}
	return nil
}

func helmResource(name, value, upgradeStrategy string) *resource.Info {
	annotations := map[string]string{}
	if upgradeStrategy != "" {
		annotations[install.AnnotationUpgradeStrategy] = upgradeStrategy
	}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
		},
		"data": map[string]any{"value": value},
	}}
	object.SetAnnotations(annotations)
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	object.SetGroupVersionKind(gvk)
	return &resource.Info{
		Name:      name,
		Namespace: "default",
		Object:    object,
		Mapping: &meta.RESTMapping{
			Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
			GroupVersionKind: gvk,
		},
	}
}

func TestLifecycleKubeClientUpdateStrategies(t *testing.T) {
	delegate := &recordingHelmClient{}
	client := newLifecycleKubeClient(delegate)
	client.timeout = time.Second

	original := kube.ResourceList{
		helmResource("normal", "old", ""),
		helmResource("retained", "old", install.UpgradeStrategyRetain),
		helmResource("recreated", "old", install.UpgradeStrategyRecreate),
		helmResource("unchanged", "same", install.UpgradeStrategyRecreate),
	}
	target := kube.ResourceList{
		helmResource("normal", "new", ""),
		helmResource("retained", "new", install.UpgradeStrategyRetain),
		helmResource("recreated", "new", install.UpgradeStrategyRecreate),
		helmResource("unchanged", "same", install.UpgradeStrategyRecreate),
	}

	result, err := client.Update(original, target, false)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if delegate.updates != 1 || len(delegate.updateTarget) != 1 || delegate.updateTarget[0].Name != "normal" {
		t.Fatalf("regular update target = %v", resourceNames(delegate.updateTarget))
	}
	if len(delegate.updateOriginal) != 1 || delegate.updateOriginal[0].Name != "normal" {
		t.Fatalf("regular update original = %v", resourceNames(delegate.updateOriginal))
	}
	if got := resourceNames(delegate.deletes); len(got) != 1 || got[0] != "recreated" {
		t.Fatalf("deleted = %v, want [recreated]", got)
	}
	if delegate.deletePolicy != metav1.DeletePropagationForeground {
		t.Fatalf("delete propagation = %q, want Foreground", delegate.deletePolicy)
	}
	if got := resourceNames(delegate.waitsForDeletes); len(got) != 1 || got[0] != "recreated" {
		t.Fatalf("waited deletes = %v, want [recreated]", got)
	}
	if got := resourceNames(delegate.creates); len(got) != 1 || got[0] != "recreated" {
		t.Fatalf("created = %v, want [recreated]", got)
	}
	if len(result.Updated) != 1 || len(result.Deleted) != 1 || len(result.Created) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestLifecycleKubeClientValidatesBeforeUpdate(t *testing.T) {
	delegate := &recordingHelmClient{}
	client := newLifecycleKubeClient(delegate)
	target := kube.ResourceList{helmResource("invalid", "new", "Replace")}

	if _, err := client.Update(nil, target, false); err == nil {
		t.Fatal("Update() error = nil, want invalid strategy error")
	}
	if delegate.updates != 0 || len(delegate.creates) != 0 || len(delegate.deletes) != 0 {
		t.Fatal("invalid strategy caused resource mutations")
	}
}

func TestLifecycleKubeClientUsesLiveObjectWhenLeavingRetain(t *testing.T) {
	delegate := &recordingHelmClient{}
	client := newLifecycleKubeClient(delegate)
	releaseOriginal := helmResource("retained", "new", install.UpgradeStrategyRetain)
	liveOriginal := helmResource("retained", "old", "")
	target := helmResource("retained", "new", "")
	client.getLiveResourceInfo = func(info *resource.Info) (*resource.Info, error) {
		if info != releaseOriginal {
			t.Fatalf("live lookup info = %p, want %p", info, releaseOriginal)
		}
		return liveOriginal, nil
	}

	if _, err := client.Update(kube.ResourceList{releaseOriginal}, kube.ResourceList{target}, false); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(delegate.updateOriginal) != 1 || delegate.updateOriginal[0] != liveOriginal {
		t.Fatalf("update original = %#v, want live object", delegate.updateOriginal)
	}
	if len(delegate.updateTarget) != 1 || delegate.updateTarget[0] != target {
		t.Fatalf("update target = %#v, want target object", delegate.updateTarget)
	}
}

func TestLifecycleKubeClientPreservesThreeWayMergeAndLogs(t *testing.T) {
	delegate := &recordingHelmClient{logPodList: &corev1.PodList{
		Items: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "hook"}}},
	}}
	client := newLifecycleKubeClient(delegate)
	original := kube.ResourceList{
		helmResource("normal", "old", ""),
		helmResource("retained", "old", install.UpgradeStrategyRetain),
		helmResource("recreated", "old", install.UpgradeStrategyRecreate),
	}
	target := kube.ResourceList{
		helmResource("normal", "new", ""),
		helmResource("retained", "new", install.UpgradeStrategyRetain),
		helmResource("recreated", "new", install.UpgradeStrategyRecreate),
	}

	if _, err := client.UpdateThreeWayMerge(original, target, false); err != nil {
		t.Fatalf("UpdateThreeWayMerge() error = %v", err)
	}
	if delegate.threeWayUpdates != 1 || delegate.updates != 0 {
		t.Fatalf("three-way updates = %d, ordinary updates = %d", delegate.threeWayUpdates, delegate.updates)
	}
	if got := resourceNames(delegate.updateTarget); len(got) != 1 || got[0] != "normal" {
		t.Fatalf("three-way update target = %v, want [normal]", got)
	}
	if got := resourceNames(delegate.deletes); len(got) != 1 || got[0] != "recreated" {
		t.Fatalf("three-way recreated deletes = %v, want [recreated]", got)
	}
	if got := resourceNames(delegate.creates); len(got) != 1 || got[0] != "recreated" {
		t.Fatalf("three-way recreated creates = %v, want [recreated]", got)
	}

	pods, err := client.GetPodList("default", metav1.ListOptions{LabelSelector: "job-name=hook"})
	if err != nil {
		t.Fatalf("GetPodList() error = %v", err)
	}
	var output bytes.Buffer
	if err := client.OutputContainerLogsForPodList(pods, "default", func(_, _, _ string) io.Writer {
		return &output
	}); err != nil {
		t.Fatalf("OutputContainerLogsForPodList() error = %v", err)
	}
	if delegate.logNamespace != "default" || delegate.logPodList != pods || delegate.logWrites != 1 || output.String() != "logs" {
		t.Fatalf("logs were not delegated: namespace=%q writes=%d output=%q", delegate.logNamespace, delegate.logWrites, output.String())
	}
}

func TestLifecycleKubeClientRetainsRemovedResources(t *testing.T) {
	delegate := &recordingHelmClient{}
	client := newLifecycleKubeClient(delegate)
	retained := helmResource("retained", "old", "")
	retained.Object.(*unstructured.Unstructured).SetAnnotations(map[string]string{
		install.AnnotationRemoveStrategy: install.RemoveStrategyRetain,
	})
	ordinary := helmResource("ordinary", "old", "")

	if _, err := client.Update(kube.ResourceList{retained, ordinary}, nil, false); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := resourceNames(delegate.updateOriginal); len(got) != 1 || got[0] != "ordinary" {
		t.Fatalf("original passed to Helm = %v, want [ordinary]", got)
	}

	delegate.deletes = nil
	if _, errs := client.DeleteWithPropagationPolicy(kube.ResourceList{retained, ordinary}, metav1.DeletePropagationBackground); len(errs) != 0 {
		t.Fatalf("DeleteWithPropagationPolicy() errors = %v", errs)
	}
	if got := resourceNames(delegate.deletes); len(got) != 1 || got[0] != "ordinary" {
		t.Fatalf("uninstall deletes = %v, want [ordinary]", got)
	}
}

func resourceNames(resources kube.ResourceList) []string {
	names := make([]string, 0, len(resources))
	for _, info := range resources {
		names = append(names, info.Name)
	}
	return names
}
