package postrender

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPausedRendererPausesSupportedWorkloads(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: deployment}
spec: {replicas: 3}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: statefulset}
spec: {replicas: 2}
---
apiVersion: batch/v1
kind: Job
metadata: {name: job}
spec: {suspend: false}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: cronjob}
spec: {schedule: "0 * * * *", suspend: false}
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: daemonset}
spec:
  template:
    spec:
      nodeSelector:
        disk: ssd
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 1
              preference:
                matchExpressions:
                  - key: zone
                    operator: In
                    values: [a]
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: architecture
                    operator: In
                    values: [amd64]
              - matchFields:
                  - key: metadata.name
                    operator: In
                    values: [worker-1]
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: daemonset-without-affinity}
spec:
  template:
    spec: {}
---
apiVersion: example.io/v1
kind: Deployment
metadata: {name: custom-deployment}
spec: {replicas: 7}
---
apiVersion: v1
kind: Service
metadata: {name: service}
spec: {}
`)

	got, err := (&PausedRenderer{Paused: true}).ModifyObjects(objects)
	if err != nil {
		t.Fatalf("ModifyObjects() error = %v", err)
	}
	// Applying the renderer more than once must not duplicate the reserved
	// requirements.
	got, err = (&PausedRenderer{Paused: true}).ModifyObjects(got)
	if err != nil {
		t.Fatalf("second ModifyObjects() error = %v", err)
	}
	for _, name := range []string{"deployment", "statefulset"} {
		replicas, found, err := unstructured.NestedInt64(objectByName(t, got, name).Object, "spec", "replicas")
		if err != nil || !found || replicas != 0 {
			t.Errorf("%s replicas = %d, found=%v, err=%v; want 0", name, replicas, found, err)
		}
	}
	for _, name := range []string{"job", "cronjob"} {
		suspend, found, err := unstructured.NestedBool(objectByName(t, got, name).Object, "spec", "suspend")
		if err != nil || !found || !suspend {
			t.Errorf("%s suspend = %v, found=%v, err=%v; want true", name, suspend, found, err)
		}
	}
	nodeSelector := nestedStringMap(t, objectByName(t, got, "daemonset").Object, "spec", "template", "spec", "nodeSelector")
	assertStringMapValue(t, nodeSelector, "disk", "ssd", "DaemonSet nodeSelector")
	if _, found := nodeSelector[PausedNodeAffinityKey]; found {
		t.Error("DaemonSet pause unexpectedly changed nodeSelector")
	}
	assertDaemonSetPausedAffinity(t, objectByName(t, got, "daemonset"), 2)
	assertDaemonSetPausedAffinity(t, objectByName(t, got, "daemonset-without-affinity"), 1)

	customReplicas, _, _ := unstructured.NestedInt64(objectByName(t, got, "custom-deployment").Object, "spec", "replicas")
	if customReplicas != 7 {
		t.Errorf("custom resource replicas = %d, want 7", customReplicas)
	}
}

func assertDaemonSetPausedAffinity(t *testing.T, daemonset *unstructured.Unstructured, wantTerms int) {
	t.Helper()
	terms, found, err := unstructured.NestedSlice(daemonset.Object,
		"spec", "template", "spec", "affinity", "nodeAffinity",
		"requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms")
	if err != nil || !found || len(terms) != wantTerms {
		t.Fatalf("required nodeSelectorTerms: found=%v len=%d err=%v, want %d terms", found, len(terms), err, wantTerms)
	}
	for i, raw := range terms {
		term, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("term %d has type %T", i, raw)
		}
		expressions, _ := term["matchExpressions"].([]any)
		operators := map[string]int{}
		for _, expressionRaw := range expressions {
			expression, _ := expressionRaw.(map[string]any)
			if expression["key"] == PausedNodeAffinityKey {
				operators[expression["operator"].(string)]++
			}
		}
		if operators["Exists"] != 1 || operators["DoesNotExist"] != 1 {
			t.Errorf("term %d does not contain contradictory pause requirements: %#v", i, expressions)
		}
	}

	if daemonset.GetName() != "daemonset" {
		return
	}
	preferred, found, err := unstructured.NestedSlice(daemonset.Object,
		"spec", "template", "spec", "affinity", "nodeAffinity", "preferredDuringSchedulingIgnoredDuringExecution")
	if err != nil || !found || len(preferred) != 1 {
		t.Errorf("preferred node affinity was not preserved: found=%v len=%d err=%v", found, len(preferred), err)
	}
}

func TestPausedRendererDisabledLeavesObjectsUnchanged(t *testing.T) {
	objects := mustParseObjects(t, `
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: daemonset}
spec:
  template:
    spec:
      nodeSelector: {disk: ssd}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: cronjob}
spec: {suspend: false}
`)
	want := make([]map[string]any, len(objects))
	for i, obj := range objects {
		want[i] = obj.DeepCopy().Object
	}

	got, err := (&PausedRenderer{Paused: false}).ModifyObjects(objects)
	if err != nil {
		t.Fatalf("ModifyObjects() error = %v", err)
	}
	for i, obj := range got {
		if !reflect.DeepEqual(obj.Object, want[i]) {
			t.Errorf("object %s changed while Paused=false", obj.GetName())
		}
	}
}
