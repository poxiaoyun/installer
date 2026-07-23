package native

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"xiaoshiai.cn/installer/install"
)

var configMapGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}

type trackingClient struct {
	client.Client
	creates int
	patches int
	deletes int
	order   []string
}

func (c *trackingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.creates++
	c.order = append(c.order, "create")
	return c.Client.Create(ctx, obj, opts...)
}

func (c *trackingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches++
	c.order = append(c.order, "patch")
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *trackingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes++
	c.order = append(c.order, "delete")
	return c.Client.Delete(ctx, obj, opts...)
}

func testResource(name, value string, annotations map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "default",
		},
		"data": map[string]any{"value": value},
	}}
	obj.SetGroupVersionKind(configMapGVK)
	obj.SetAnnotations(annotations)
	return obj
}

func testSyncOptions() *SyncOptions {
	return &SyncOptions{ServerSideApply: true, CreateNamespace: false, DeleteTimeout: time.Second}
}

func TestSyncDiffRetainsExistingResourceOnUpgrade(t *testing.T) {
	ctx := context.Background()
	live := testResource("settings", "old", nil)
	base := fake.NewClientBuilder().WithRuntimeObjects(live).Build()
	tracking := &trackingClient{Client: base}
	desired := testResource("settings", "new", map[string]string{
		install.AnnotationUpgradeStrategy: install.UpgradeStrategyRetain,
	})

	managed, err := (&ClientApply{Client: tracking}).SyncDiff(ctx, DiffResult{Applys: []*unstructured.Unstructured{desired}}, testSyncOptions())
	if err != nil {
		t.Fatalf("SyncDiff() error = %v", err)
	}
	if tracking.creates != 0 || tracking.patches != 0 || tracking.deletes != 0 {
		t.Fatalf("Retain mutated resource: creates=%d patches=%d deletes=%d", tracking.creates, tracking.patches, tracking.deletes)
	}
	if len(managed) != 1 {
		t.Fatalf("managed resources = %d, want 1", len(managed))
	}
	got := testResource("settings", "", nil)
	if err := base.Get(ctx, client.ObjectKeyFromObject(live), got); err != nil {
		t.Fatal(err)
	}
	value, _, _ := unstructured.NestedString(got.Object, "data", "value")
	if value != "old" {
		t.Fatalf("live value = %q, want old", value)
	}
}

func TestSyncDiffRecreatesExistingResource(t *testing.T) {
	ctx := context.Background()
	live := testResource("settings", "old", nil)
	base := fake.NewClientBuilder().WithRuntimeObjects(live).Build()
	tracking := &trackingClient{Client: base}
	desired := testResource("settings", "new", map[string]string{
		install.AnnotationUpgradeStrategy: install.UpgradeStrategyRecreate,
	})

	_, err := (&ClientApply{Client: tracking}).SyncDiff(ctx, DiffResult{Applys: []*unstructured.Unstructured{desired}}, testSyncOptions())
	if err != nil {
		t.Fatalf("SyncDiff() error = %v", err)
	}
	if tracking.deletes != 1 || tracking.creates != 1 || tracking.patches != 0 {
		t.Fatalf("Recreate operations: creates=%d patches=%d deletes=%d", tracking.creates, tracking.patches, tracking.deletes)
	}
	if len(tracking.order) != 2 || tracking.order[0] != "delete" || tracking.order[1] != "create" {
		t.Fatalf("operation order = %v, want [delete create]", tracking.order)
	}
	got := testResource("settings", "", nil)
	if err := base.Get(ctx, client.ObjectKeyFromObject(desired), got); err != nil {
		t.Fatal(err)
	}
	value, _, _ := unstructured.NestedString(got.Object, "data", "value")
	if value != "new" {
		t.Fatalf("live value = %q, want new", value)
	}
}

func TestSyncDiffRetainsRemovedLiveResource(t *testing.T) {
	ctx := context.Background()
	live := testResource("settings", "old", map[string]string{
		install.AnnotationRemoveStrategy: install.RemoveStrategyRetain,
	})
	base := fake.NewClientBuilder().WithRuntimeObjects(live).Build()
	tracking := &trackingClient{Client: base}
	ref := &unstructured.Unstructured{}
	ref.SetGroupVersionKind(configMapGVK)
	ref.SetName(live.GetName())
	ref.SetNamespace(live.GetNamespace())

	managed, err := (&ClientApply{Client: tracking}).SyncDiff(ctx, DiffResult{Removes: []*unstructured.Unstructured{ref}}, testSyncOptions())
	if err != nil {
		t.Fatalf("SyncDiff() error = %v", err)
	}
	if tracking.deletes != 0 {
		t.Fatalf("Remove Retain deleted resource")
	}
	if len(managed) != 0 {
		t.Fatalf("retained removed resource remains managed: %#v", managed)
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(live), testResource("settings", "", nil)); err != nil {
		t.Fatalf("retained live resource is missing: %v", err)
	}
}

func TestSyncDiffRejectsInvalidStrategyBeforeMutation(t *testing.T) {
	ctx := context.Background()
	base := fake.NewClientBuilder().Build()
	tracking := &trackingClient{Client: base}
	first := testResource("first", "one", nil)
	invalid := testResource("invalid", "two", map[string]string{
		install.AnnotationUpgradeStrategy: "Replace",
	})

	_, err := (&ClientApply{Client: tracking}).SyncDiff(ctx, DiffResult{Creats: []*unstructured.Unstructured{first, invalid}}, testSyncOptions())
	if err == nil {
		t.Fatal("SyncDiff() error = nil, want invalid strategy error")
	}
	if tracking.creates != 0 || tracking.patches != 0 || tracking.deletes != 0 {
		t.Fatalf("invalid strategy caused mutations: creates=%d patches=%d deletes=%d", tracking.creates, tracking.patches, tracking.deletes)
	}
}

func TestIsLifecycleStrategy(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap"}}
	obj.SetAnnotations(map[string]string{
		install.AnnotationUpgradeStrategy: install.UpgradeStrategyRetain,
		install.AnnotationRemoveStrategy:  install.RemoveStrategyRetain,
	})
	if !IsSkipUpdate(obj) || IsRecreateUpdate(obj) || !IsSkipDelete(obj) {
		t.Fatal("Retain strategies were not recognized")
	}
	obj.SetAnnotations(map[string]string{install.AnnotationUpgradeStrategy: install.UpgradeStrategyRecreate})
	if IsSkipUpdate(obj) || !IsRecreateUpdate(obj) || IsSkipDelete(obj) {
		t.Fatal("Recreate strategy was not recognized")
	}
}
