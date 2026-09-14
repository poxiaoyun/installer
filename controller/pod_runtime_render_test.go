package controller

import (
	"bytes"
	"reflect"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	appbase "xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller/postrender"
)

func TestPostRendererPreservesRenderedPodRuntime(t *testing.T) {
	// Retired path annotations are ordinary metadata, even with conflicting
	// values, missing paths, or resources that do not contain a Pod template.
	// Scheduling admission owns validation of the NUMA configuration below.
	const manifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: rendered
  annotations:
    apps.xiaoshiai.cn/flavor-path: /flavor
    scheduling.xiaoshiai.cn/numa-policy: single-numa-node
spec:
  selector:
    matchLabels: {app: rendered}
  template:
    metadata:
      labels: {app: rendered, device: chart-device}
      annotations: {example.com/source: chart}
    spec:
      schedulerName: chart-scheduler
      runtimeClassName: chart-runtime
      containers:
        - name: worker
          image: example/worker
          resources:
            requests: {cpu: 500m, memory: 1Gi}
            limits: {cpu: "1", memory: 2Gi}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: omitted
  annotations:
    apps.xiaoshiai.cn/flavor-path: /flavor
spec:
  template:
    spec:
      containers:
        - name: worker
          image: example/worker
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: ordinary-metadata
  annotations:
    apps.xiaoshiai.cn/flavor-path: /missing
data:
  message: chart-owned
`
	for _, kind := range []appsv1.InstanceKind{
		appsv1.InstanceKindHelm, appsv1.InstanceKindKustomize, appsv1.InstanceKindTemplate,
	} {
		t.Run(string(kind), func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion, {Group: "apps", Version: "v1"}})
			mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), meta.RESTScopeNamespace)
			mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
			r := &InstanceReconciler{Client: fake.NewClientBuilder().WithRESTMapper(mapper).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo"}},
			).Build()}
			instance := &appsv1.Instance{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "demo"},
				Spec:       appsv1.InstanceSpec{Kind: kind},
			}
			values := map[string]any{"flavor": map[string]any{
				"type":             "Accelerator",
				"schedulerName":    "volcano",
				"runtimeClassName": "nvidia",
				"podLabels":        map[string]any{"device": "flavor-device"},
				"podAnnotations": map[string]any{
					"scheduling.xiaoshiai.cn/numa-policy": "single-numa-node",
					"example.com/source":                  "flavor",
				},
			}}
			originalValues := runtime.DeepCopyJSON(values)
			var ch *chart.Chart
			if kind == appsv1.InstanceKindHelm {
				ch = &chart.Chart{
					Metadata: &chart.Metadata{Name: "test", Version: "1.0.0"},
					Values: map[string]any{"flavor": map[string]any{
						"podLabels": map[string]any{"from-defaults": "true"},
					}},
				}
			}
			out, err := r.buildPostRenderer(t.Context(), instance, values).Run(bytes.NewBufferString(manifest), ch)
			if err != nil {
				t.Fatal(err)
			}
			got, err := postrender.ParseObjects(out.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			want, err := postrender.ParseObjects([]byte(manifest))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("rendered %d objects, want %d", len(got), len(want))
			}
			for i, object := range got {
				want[i].SetNamespace(instance.Namespace)
				if kind != appsv1.InstanceKindHelm {
					annotations := want[i].GetAnnotations()
					annotations[appbase.AnnotationInstanceName] = instance.Name
					annotations[appbase.AnnotationInstanceNamespace] = instance.Namespace
					want[i].SetAnnotations(annotations)
				}
				if !reflect.DeepEqual(object.Object, want[i].Object) {
					t.Fatalf("rendered resource %s changed beyond namespace and ownership:\ngot: %#v\nwant: %#v", object.GetName(), object.Object, want[i].Object)
				}
			}
			if !reflect.DeepEqual(values, originalValues) {
				t.Fatal("post-render mutated caller values")
			}
		})
	}
}
