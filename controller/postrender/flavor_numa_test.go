package postrender

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFlavorNUMAProjectionAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, flavorType, cpu, kind string
		sidecar, conflict, fail     bool
	}{
		{name: "CPU", flavorType: "CPU", cpu: "4", kind: "Deployment"},
		{name: "accelerator CPU", flavorType: "Accelerator", cpu: "4", kind: "StatefulSet"},
		{name: "fractional", flavorType: "CPU", cpu: "500m", kind: "Deployment", fail: true},
		{name: "sidecar resources", flavorType: "CPU", cpu: "4", kind: "Deployment", sidecar: true, fail: true},
		{name: "scheduler conflict", flavorType: "CPU", cpu: "4", kind: "Deployment", conflict: true, fail: true},
		{name: "unsupported workload", flavorType: "CPU", cpu: "4", kind: "ReplicaSet", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    apps.xiaoshiai.cn/flavor-path: /flavor
spec:
  template:
    metadata:
      annotations:
        example.com/keep: value
    spec:
      containers:
        - name: worker
          image: example/worker
          resources:
            requests: {cpu: "4", memory: 8Gi}
            limits: {cpu: "4", memory: 8Gi}
`)
			obj := objects[0]
			obj.SetKind(tc.kind)
			containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
			resources := containers[0].(map[string]any)["resources"].(map[string]any)
			resources["requests"].(map[string]any)["cpu"] = tc.cpu
			resources["limits"].(map[string]any)["cpu"] = tc.cpu
			if tc.sidecar {
				containers = append(containers, map[string]any{"name": "sidecar", "image": "example/sidecar"})
			}
			if err := unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers"); err != nil {
				t.Fatal(err)
			}
			if tc.conflict {
				if err := unstructured.SetNestedField(obj.Object, "default-scheduler", "spec", "template", "spec", "schedulerName"); err != nil {
					t.Fatal(err)
				}
			}
			renderer := &FlavorRenderer{Values: map[string]any{"flavor": map[string]any{
				"type": tc.flavorType, "schedulerName": "volcano", "podAnnotations": map[string]any{numaPolicyAnnotation: "single-numa-node", "example.com/new": "ignored"},
			}}}
			_, err := renderer.ModifyObjects(objects)
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v, want failure %v", err, tc.fail)
			}
			if err != nil {
				return
			}
			if obj.GetAnnotations()[numaPolicyAnnotation] != "single-numa-node" {
				t.Fatalf("annotations = %#v", obj.GetAnnotations())
			}
			assertNestedString(t, obj, "volcano", "spec", "template", "spec", "schedulerName")
			annotations := nestedStringMap(t, obj.Object, "spec", "template", "metadata", "annotations")
			if len(annotations) != 1 || annotations["example.com/keep"] != "value" {
				t.Fatalf("unrelated annotations changed: %#v", annotations)
			}
		})
	}
}
