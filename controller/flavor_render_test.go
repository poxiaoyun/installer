package controller

import (
	"bytes"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller/postrender"
)

func TestFlavorPostRendererUsesChartDefaultsAndInstanceOverrides(t *testing.T) {
	r := &InstanceReconciler{Client: fake.NewClientBuilder().WithObjects(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
	).Build()}
	instance := &appsv1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "demo"},
		Spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm}}
	values := map[string]any{"worker": map[string]any{"flavor": map[string]any{
		"schedulerName": "custom-scheduler", "podLabels": map[string]any{"device": "custom"},
	}}}
	ch := &chart.Chart{Metadata: &chart.Metadata{Name: "test", Version: "1.0.0"}, Values: map[string]any{
		"router": map[string]any{"flavor": map[string]any{"resources": map[string]any{}}},
		"worker": map[string]any{"flavor": map[string]any{
			"type": "Accelerator", "schedulerName": "volcano", "runtimeClassName": "nvidia",
			"podLabels": map[string]any{"device": "default"},
		}},
	}}
	renderer := r.buildPostRenderer(t.Context(), instance, values)
	out, err := renderer.Run(bytes.NewBufferString(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: router
  annotations:
    apps.xiaoshiai.cn/flavor-path: /router/flavor
spec:
  template:
    spec:
      containers: []
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    apps.xiaoshiai.cn/flavor-path: /worker/flavor
spec:
  template:
    metadata:
      labels:
        app: worker
    spec:
      containers: []
`), ch)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := postrender.ParseObjects(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 2 {
		t.Fatalf("rendered %d objects, want router and worker", len(objects))
	}
	for _, object := range objects {
		scheduler, _, _ := unstructured.NestedString(object.Object, "spec", "template", "spec", "schedulerName")
		if object.GetName() == "router" && scheduler != "" {
			t.Fatalf("CPU router scheduler = %q", scheduler)
		}
		if object.GetName() == "worker" {
			runtimeClass, _, _ := unstructured.NestedString(object.Object, "spec", "template", "spec", "runtimeClassName")
			labels, _, _ := unstructured.NestedStringMap(object.Object, "spec", "template", "metadata", "labels")
			if scheduler != "custom-scheduler" || runtimeClass != "nvidia" || labels["device"] != "custom" || labels["app"] != "worker" {
				t.Fatalf("incorrect runtime projection: %s %s %v", scheduler, runtimeClass, labels)
			}
		}
	}
	if _, found := values["router"]; found {
		t.Fatal("post-render mutated caller values")
	}
}
