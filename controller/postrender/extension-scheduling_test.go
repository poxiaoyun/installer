package postrender

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appbase "xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func TestSchedulingHandlerVolcanoLowPriority(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec: {template: {spec: {containers: []}}}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: worker}
spec: {template: {spec: {containers: []}}}
`)
	got, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeVolcano, SchedulingPriorityLow, ""))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	for _, name := range []string{"api", "worker"} {
		obj := objectByName(t, got, name)
		assertNestedString(t, obj, VolcanoSchedulerName, "spec", "template", "spec", "schedulerName")
		assertNestedString(t, obj, LowPriorityClassName, "spec", "template", "spec", "priorityClassName")
	}
}

func TestSchedulingHandlerGangUsesChartDefinedPodGroup(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    apps.xiaoshiai.cn/scheduling-target: "true"
    scheduling.k8s.io/group-name: worker-group
spec: {template: {spec: {containers: []}}}
---
apiVersion: scheduling.k8s.io/v1alpha2
kind: PodGroup
metadata: {name: worker-group}
spec:
  schedulingPolicy:
    gang:
      minCount: 2
`)
	got, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeGang, SchedulingPriorityMedium, "2"))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("objects = %d, want the two Chart-rendered objects", len(got))
	}
	worker := objectByName(t, got, "worker")
	assertNestedString(t, worker, VolcanoSchedulerName, "spec", "template", "spec", "schedulerName")
	assertNestedString(t, worker, MediumPriorityClassName, "spec", "template", "spec", "priorityClassName")
	if got := worker.GetAnnotations()[PodGroupReferenceAnnotation]; got != "worker-group" {
		t.Fatalf("PodGroup reference = %q, want worker-group", got)
	}
	podGroup := objectByName(t, got, "worker-group")
	assertNestedString(t, podGroup, MediumPriorityClassName, "spec", "priorityClassName")
	minCount, found, err := unstructured.NestedInt64(podGroup.Object, "spec", "schedulingPolicy", "gang", "minCount")
	if err != nil || !found || minCount != 2 {
		t.Fatalf("PodGroup minCount = %d, found=%v, err=%v; want Chart value 2", minCount, found, err)
	}
}

func TestSchedulingHandlerGangRequiresChartDefinedPodGroup(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    apps.xiaoshiai.cn/scheduling-target: "true"
spec: {template: {spec: {containers: []}}}
`)
	if _, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeGang, SchedulingPriorityHigh, "2")); err == nil {
		t.Fatal("Handle() error = nil, want missing Chart PodGroup error")
	}
}

func TestSchedulingHandlerGangRejectsMissingReferencedPodGroup(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    scheduling.k8s.io/group-name: missing-group
spec: {template: {spec: {containers: []}}}
`)
	if _, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeGang, SchedulingPriorityHigh, "2")); err == nil {
		t.Fatal("Handle() error = nil, want unresolved PodGroup error")
	}
}

func TestSchedulingHandlerGangRejectsChartIgnoringPlatformMinCount(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    scheduling.k8s.io/group-name: worker-group
spec: {template: {spec: {containers: []}}}
---
apiVersion: scheduling.k8s.io/v1alpha2
kind: PodGroup
metadata: {name: worker-group}
spec: {schedulingPolicy: {gang: {minCount: 1}}}
`)
	if _, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeGang, SchedulingPriorityHigh, "2")); err == nil {
		t.Fatal("Handle() error = nil, want minCount mismatch error")
	}
}

func TestSchedulingHandlerMarkersExcludeAuxiliaryWorkloads(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: router
  annotations:
    apps.xiaoshiai.cn/scheduling-target: "false"
spec: {template: {spec: {containers: []}}}
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: worker
  annotations:
    apps.xiaoshiai.cn/scheduling-target: "true"
spec: {template: {spec: {containers: []}}}
`)
	got, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeVolcano, SchedulingPriorityHigh, ""))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	router := objectByName(t, got, "router")
	if _, found, _ := unstructured.NestedString(router.Object, "spec", "template", "spec", "schedulerName"); found {
		t.Fatal("router unexpectedly received schedulerName")
	}
	worker := objectByName(t, got, "worker")
	assertNestedString(t, worker, VolcanoSchedulerName, "spec", "template", "spec", "schedulerName")
}

func TestSchedulingHandlerNonGangRejectsChartGroupReference(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  annotations:
    scheduling.k8s.io/group-name: chart-group
spec: {template: {spec: {containers: []}}}
`)
	if _, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeDefault, SchedulingPriorityDefault, "")); err == nil {
		t.Fatal("Handle() error = nil, want non-gang PodGroup reference error")
	}
}

func TestSchedulingHandlerRejectsConflictingScheduler(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: worker}
spec: {template: {spec: {schedulerName: custom-scheduler, containers: []}}}
`)
	if _, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeVolcano, SchedulingPriorityHigh, "")); err == nil {
		t.Fatal("Handle() error = nil, want scheduler conflict")
	}
}

func TestSchedulingRendererRejectsMultipleProfiles(t *testing.T) {
	renderer := &SchedulingRenderer{
		Extensions: []appsv1.Extension{
			schedulingExtension(SchedulingModeDefault, SchedulingPriorityDefault, ""),
			schedulingExtension(SchedulingModeVolcano, SchedulingPriorityHigh, ""),
		},
		Handler: &SchedulingHandler{},
	}
	if _, err := renderer.ModifyObjects(nil); err == nil {
		t.Fatal("ModifyObjects() error = nil, want duplicate Scheduling extension error")
	}
}

func TestSchedulingValuesForGang(t *testing.T) {
	got, err := SchedulingValues([]appsv1.Extension{
		schedulingExtension(SchedulingModeGang, SchedulingPriorityLow, "3"),
	})
	if err != nil {
		t.Fatalf("SchedulingValues() error = %v", err)
	}
	want := map[string]any{"mode": "gang", "priority": "low", "minCount": float64(3)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SchedulingValues() = %#v, want %#v", got, want)
	}
}

func TestSchedulingValuesWithoutExtensionUsesPlatformDefault(t *testing.T) {
	got, err := SchedulingValues(nil)
	if err != nil {
		t.Fatalf("SchedulingValues() error = %v", err)
	}
	want := map[string]any{"mode": "default", "priority": "default"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SchedulingValues() = %#v, want %#v", got, want)
	}
}

func TestSchedulingHandlerDefaultAllowsResourceOnlyChart(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: v1
kind: ConfigMap
metadata: {name: config}
data: {key: value}
`)
	got, err := (&SchedulingHandler{}).Handle(objects, schedulingExtension(SchedulingModeDefault, SchedulingPriorityDefault, ""))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(got) != 1 || got[0].GetName() != "config" {
		t.Fatalf("default scheduling changed resource-only chart: %#v", got)
	}
}

func schedulingExtension(mode, priority, minCount string) appsv1.Extension {
	params := map[string]string{
		appbase.ExtensionParamSchedulingMode:     mode,
		appbase.ExtensionParamSchedulingPriority: priority,
	}
	if minCount != "" {
		params[appbase.ExtensionParamGangMinCount] = minCount
	}
	return appsv1.Extension{Name: "platform-scheduling", Kind: appbase.ExtensionKindScheduling, Params: params}
}

func assertNestedString(t *testing.T, obj *unstructured.Unstructured, want string, fields ...string) {
	t.Helper()
	got, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found || got != want {
		t.Fatalf("%s %v = %q, found=%v, err=%v; want %q", obj.GetName(), fields, got, found, err, want)
	}
}
