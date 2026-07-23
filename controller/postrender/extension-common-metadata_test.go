package postrender

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func TestCommonMetadataExtensionRenderer(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: config
  labels:
    chart: original
    team: chart
  annotations:
    note: chart
---
apiVersion: v1
kind: Pod
metadata:
  name: pod
spec:
  containers: []
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: deployment
spec:
  selector:
    matchLabels:
      chart: selector
  template:
    spec:
      containers: []
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: statefulset
spec:
  selector:
    matchLabels: {}
  template:
    metadata: {}
    spec:
      containers: []
  volumeClaimTemplates:
    - metadata:
        name: data
      spec: {}
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: daemonset
spec:
  selector:
    matchLabels: {}
  template:
    metadata: {}
    spec:
      containers: []
---
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: replicaset
spec:
  selector:
    matchLabels: {}
  template:
    metadata: {}
    spec:
      containers: []
---
apiVersion: batch/v1
kind: Job
metadata:
  name: job
spec:
  manualSelector: true
  selector:
    matchLabels:
      chart: selector
  template:
    spec:
      containers: []
      restartPolicy: Never
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: cronjob
spec:
  schedule: "0 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers: []
          restartPolicy: Never
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: monitor
spec:
  selector:
    matchLabels:
      chart: service
`)

	identity := &InstanceIdentityRenderer{InstanceName: "sample"}
	if _, err := identity.ModifyObjects(objects); err != nil {
		t.Fatalf("identity ModifyObjects() error = %v", err)
	}
	renderer := &CommonMetadataRenderer{
		CommonLabels: map[string]string{
			"team":        "platform",
			LabelInstance: "attempted-override",
		},
		CommonAnnotations: map[string]string{
			"note":  "common",
			"owner": "apps",
		},
		InjectPodTemplates:         true,
		InjectVolumeClaimTemplates: true,
	}
	got, err := renderer.ModifyObjects(objects)
	if err != nil {
		t.Fatalf("ModifyObjects() error = %v", err)
	}

	for _, obj := range got {
		assertStringMapValue(t, obj.GetLabels(), LabelInstance, "sample", obj.GetKind()+" top-level labels")
		assertStringMapValue(t, obj.GetLabels(), "team", "platform", obj.GetKind()+" top-level labels")
		assertStringMapValue(t, obj.GetAnnotations(), "note", "common", obj.GetKind()+" top-level annotations")
		assertStringMapValue(t, obj.GetAnnotations(), "owner", "apps", obj.GetKind()+" top-level annotations")
	}

	config := objectByName(t, got, "config")
	assertStringMapValue(t, config.GetLabels(), "chart", "original", "ConfigMap labels")

	pod := objectByName(t, got, "pod")
	assertStringMapValue(t, pod.GetLabels(), LabelInstance, "sample", "Pod labels")

	for _, name := range []string{"deployment", "statefulset", "daemonset", "replicaset", "job"} {
		obj := objectByName(t, got, name)
		labels := nestedStringMap(t, obj.Object, "spec", "template", "metadata", "labels")
		annotations := nestedStringMap(t, obj.Object, "spec", "template", "metadata", "annotations")
		assertStringMapValue(t, labels, LabelInstance, "sample", name+" pod template labels")
		assertStringMapValue(t, labels, "team", "platform", name+" pod template labels")
		assertStringMapValue(t, annotations, "owner", "apps", name+" pod template annotations")
	}

	cronjob := objectByName(t, got, "cronjob")
	cronLabels := nestedStringMap(t, cronjob.Object, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
	cronAnnotations := nestedStringMap(t, cronjob.Object, "spec", "jobTemplate", "spec", "template", "metadata", "annotations")
	assertStringMapValue(t, cronLabels, LabelInstance, "sample", "CronJob pod template labels")
	assertStringMapValue(t, cronAnnotations, "owner", "apps", "CronJob pod template annotations")

	for _, name := range []string{"deployment", "statefulset", "daemonset", "replicaset", "job"} {
		selector := nestedStringMap(t, objectByName(t, got, name).Object, "spec", "selector", "matchLabels")
		if _, found := selector[LabelInstance]; found {
			t.Errorf("%s selector unexpectedly contains instance label", name)
		}
		if _, found := selector["team"]; found {
			t.Errorf("%s selector unexpectedly contains common label team", name)
		}
	}
	if _, found, _ := unstructured.NestedMap(cronjob.Object, "spec", "jobTemplate", "spec", "selector"); found {
		t.Error("CronJob without a manual selector unexpectedly received one")
	}

	monitor := objectByName(t, got, "monitor")
	monitorSelector := nestedStringMap(t, monitor.Object, "spec", "selector", "matchLabels")
	if len(monitorSelector) != 1 || monitorSelector["chart"] != "service" {
		t.Errorf("ServiceMonitor selector was changed: %#v", monitorSelector)
	}

	statefulset := objectByName(t, got, "statefulset")
	vctLabels := nestedStringMap(t, statefulset.Object, "spec", "volumeClaimTemplates", "0", "metadata", "labels")
	vctAnnotations := nestedStringMap(t, statefulset.Object, "spec", "volumeClaimTemplates", "0", "metadata", "annotations")
	if _, found := vctLabels[LabelInstance]; found {
		t.Error("volumeClaimTemplate unexpectedly received the instance identity label")
	}
	assertStringMapValue(t, vctLabels, "team", "platform", "volumeClaimTemplate labels")
	assertStringMapValue(t, vctAnnotations, "owner", "apps", "volumeClaimTemplate annotations")
}

func TestCommonMetadataRendererDoesNotCreateMetadataOnMalformedTemplate(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "broken"},
		"spec":       map[string]any{},
	}}
	_, err := (&CommonMetadataRenderer{InjectPodTemplates: true}).ModifyObjects([]*unstructured.Unstructured{obj})
	if err != nil {
		t.Fatalf("ModifyObjects() error = %v", err)
	}
	if _, found, _ := unstructured.NestedMap(obj.Object, "spec", "template"); found {
		t.Error("malformed workload unexpectedly received a fabricated template")
	}
}

func TestCommonMetadataHandlerDefaultsAndVolumeClaimOptOut(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: database
spec:
  selector:
    matchLabels: {}
  template:
    metadata: {}
    spec:
      containers: []
  volumeClaimTemplates:
    - metadata:
        name: data
      spec: {}
`
	newObjects := func() []*unstructured.Unstructured { return mustParseObjects(t, manifest) }
	handler := &CommonMetadataHandler{
		CommonLabels:      map[string]string{"team": "platform", LabelInstance: "override"},
		CommonAnnotations: map[string]string{"owner": "apps"},
	}

	objects, err := handler.Handle(newObjects(), appsv1.Extension{Kind: "CommonMetadata"})
	if err != nil {
		t.Fatal(err)
	}
	templateLabels := nestedStringMap(t, objects[0].Object, "spec", "template", "metadata", "labels")
	assertStringMapValue(t, templateLabels, "team", "platform", "default pod template labels")
	if _, exists := templateLabels[LabelInstance]; exists {
		t.Fatal("CommonMetadata extension injected the reserved instance label")
	}
	vctLabels := nestedStringMap(t, objects[0].Object, "spec", "volumeClaimTemplates", "0", "metadata", "labels")
	assertStringMapValue(t, vctLabels, "team", "platform", "default volume claim labels")

	objects, err = handler.Handle(newObjects(), appsv1.Extension{Kind: "CommonMetadata", Params: map[string]string{
		"volumeClaimTemplates": "false",
	}})
	if err != nil {
		t.Fatal(err)
	}
	vctMetadata, _, _ := unstructured.NestedSlice(objects[0].Object, "spec", "volumeClaimTemplates")
	vct := vctMetadata[0].(map[string]any)
	if labels, found, _ := unstructured.NestedStringMap(vct, "metadata", "labels"); found && len(labels) != 0 {
		t.Fatalf("volume claim labels injected after opt-out: %#v", labels)
	}

	if _, err := handler.Handle(newObjects(), appsv1.Extension{Kind: "CommonMetadata", Params: map[string]string{
		"podTemplates": "sometimes",
	}}); err == nil {
		t.Fatal("invalid boolean parameter was accepted")
	}
}

func mustParseObjects(t *testing.T, manifest string) []*unstructured.Unstructured {
	t.Helper()
	objects, err := ParseObjects([]byte(strings.TrimSpace(manifest)))
	if err != nil {
		t.Fatalf("ParseObjects() error = %v", err)
	}
	return objects
}

func objectByName(t *testing.T, objects []*unstructured.Unstructured, name string) *unstructured.Unstructured {
	t.Helper()
	for _, obj := range objects {
		if obj.GetName() == name {
			return obj
		}
	}
	t.Fatalf("object %q not found", name)
	return nil
}

func nestedStringMap(t *testing.T, object map[string]any, path ...string) map[string]string {
	t.Helper()
	// Tests use "0" as a compact notation for the first array element.
	if len(path) >= 3 && path[0] == "spec" && path[1] == "volumeClaimTemplates" && path[2] == "0" {
		items, found, err := unstructured.NestedSlice(object, path[:2]...)
		if err != nil || !found || len(items) == 0 {
			t.Fatalf("nested slice %v: found=%v err=%v", path[:2], found, err)
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("nested slice item has type %T", items[0])
		}
		return nestedStringMap(t, item, path[3:]...)
	}
	values, found, err := unstructured.NestedStringMap(object, path...)
	if err != nil || !found {
		t.Fatalf("nested string map %v: found=%v err=%v", path, found, err)
	}
	return values
}

func assertStringMapValue(t *testing.T, values map[string]string, key, want, context string) {
	t.Helper()
	if got := values[key]; got != want {
		t.Errorf("%s[%q] = %q, want %q", context, key, got, want)
	}
}
