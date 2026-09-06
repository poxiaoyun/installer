package postrender

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFlavorRendererProjectsAcceleratorRuntime(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inference
  annotations:
    apps.xiaoshiai.cn/flavor-path: /flavor
spec:
  template:
    metadata:
      labels:
        app: inference
    spec:
      containers: []
`)
	renderer := &FlavorRenderer{Values: map[string]any{
		"flavor": map[string]any{
			"type":             "Accelerator",
			"schedulerName":    "volcano",
			"runtimeClassName": "nvidia",
			"podLabels": map[string]any{
				"nvidia.com/device-plugin.config": "vgpu",
			},
		},
	}}

	input, err := SerializeObjects(objects)
	if err != nil {
		t.Fatal(err)
	}
	// Native sources run the same public post-render entry point with no Chart.
	out, err := renderer.Run(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseObjects(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	deployment := objectByName(t, got, "inference")
	assertNestedString(t, deployment, "volcano", "spec", "template", "spec", "schedulerName")
	assertNestedString(t, deployment, "nvidia", "spec", "template", "spec", "runtimeClassName")
	labels := nestedStringMap(t, deployment.Object, "spec", "template", "metadata", "labels")
	assertStringMapValue(t, labels, "app", "inference", "Pod labels")
	assertStringMapValue(t, labels, "nvidia.com/device-plugin.config", "vgpu", "Pod labels")
}

func TestFlavorRendererDoesNotProjectCPUFlavor(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: batch/v1
kind: CronJob
metadata:
  name: reports
  annotations:
    apps.xiaoshiai.cn/flavor-path: /jobs/0/flavor
spec:
  schedule: "0 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers: []
          restartPolicy: Never
`)
	renderer := &FlavorRenderer{Values: map[string]any{
		"jobs": []any{map[string]any{"flavor": map[string]any{
			"type":          "CPU",
			"schedulerName": "volcano",
		}}},
	}}

	got, err := renderer.ModifyObjects(objects)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := unstructured.NestedString(objectByName(t, got, "reports").Object,
		"spec", "jobTemplate", "spec", "template", "spec", "schedulerName")
	if err != nil || found {
		t.Fatalf("CPU schedulerName found=%v err=%v", found, err)
	}
}

func TestFlavorRendererRejectsChartRuntimeConflict(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: inference
  annotations:
    apps.xiaoshiai.cn/flavor-path: /flavor
spec:
  template:
    spec:
      schedulerName: default-scheduler
      containers: []
`)
	_, err := (&FlavorRenderer{Values: map[string]any{
		"flavor": map[string]any{"type": "Accelerator", "schedulerName": "volcano"},
	}}).ModifyObjects(objects)
	if err == nil || !strings.Contains(err.Error(), "conflicting schedulerName") {
		t.Fatalf("error = %v, want scheduler conflict", err)
	}
}

func TestFlavorRendererRejectsMissingFlavorPath(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inference
  annotations:
    apps.xiaoshiai.cn/flavor-path: /missing
spec:
  template:
    spec:
      containers: []
`)
	_, err := (&FlavorRenderer{Values: map[string]any{}}).ModifyObjects(objects)
	if err == nil || !strings.Contains(err.Error(), `field "missing" does not exist`) {
		t.Fatalf("error = %v, want missing Flavor path", err)
	}
}

func assertNestedString(t *testing.T, object *unstructured.Unstructured, want string, fields ...string) {
	t.Helper()
	got, found, err := unstructured.NestedString(object.Object, fields...)
	if err != nil || !found || got != want {
		t.Fatalf("%v = %q found=%v err=%v, want %q", fields, got, found, err, want)
	}
}
