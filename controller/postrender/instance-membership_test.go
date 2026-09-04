package postrender_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xiaoshiai.cn/installer/apis/apps"
	"xiaoshiai.cn/installer/controller/postrender"
)

func TestInstanceMembershipRendererMarksOnlyDirectResources(t *testing.T) {
	config := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":        "config",
			"annotations": map[string]any{"chart": "original"},
		},
	}}
	workload := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "workload"},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "workload"}},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels":      map[string]any{"app": "workload"},
					"annotations": map[string]any{"chart": "template"},
				},
				"spec": map[string]any{"containers": []any{}},
			},
		},
	}}
	renderer := &postrender.InstanceMembershipRenderer{
		InstanceName:      "demo",
		InstanceNamespace: "workspace-a",
	}
	got, err := renderer.ModifyObjects([]*unstructured.Unstructured{config, workload})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range got {
		if actual := object.GetAnnotations()[apps.AnnotationInstanceName]; actual != "demo" {
			t.Errorf("%s instance name annotation = %q, want %q", object.GetKind(), actual, "demo")
		}
		if actual := object.GetAnnotations()[apps.AnnotationInstanceNamespace]; actual != "workspace-a" {
			t.Errorf("%s instance namespace annotation = %q, want %q", object.GetKind(), actual, "workspace-a")
		}
	}
	if actual := config.GetAnnotations()["chart"]; actual != "original" {
		t.Errorf("ConfigMap chart annotation = %q, want %q", actual, "original")
	}

	templateAnnotations, found, err := unstructured.NestedStringMap(workload.Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found {
		t.Fatalf("read Pod template annotations: found=%v err=%v", found, err)
	}
	if _, exists := templateAnnotations[apps.AnnotationInstanceName]; exists {
		t.Fatal("Pod template received Instance membership annotations")
	}
	if actual := templateAnnotations["chart"]; actual != "template" {
		t.Errorf("Pod template chart annotation = %q, want %q", actual, "template")
	}
	selector, found, err := unstructured.NestedStringMap(workload.Object, "spec", "selector", "matchLabels")
	if err != nil || !found {
		t.Fatalf("read Deployment selector: found=%v err=%v", found, err)
	}
	if len(selector) != 1 || selector["app"] != "workload" {
		t.Fatalf("Deployment selector was changed: %#v", selector)
	}
}

func TestInstanceMembershipRendererRejectsConflictingAnnotations(t *testing.T) {
	for _, key := range []string{apps.AnnotationInstanceName, apps.AnnotationInstanceNamespace} {
		t.Run(key, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":        "config",
					"annotations": map[string]any{key: "other"},
				},
			}}
			_, err := (&postrender.InstanceMembershipRenderer{
				InstanceName:      "demo",
				InstanceNamespace: "workspace-a",
			}).ModifyObjects([]*unstructured.Unstructured{object})
			if err != nil {
				if !strings.Contains(err.Error(), key) {
					t.Fatalf("ModifyObjects() error = %v, want conflict for %s", err, key)
				}
				return
			}
			t.Fatalf("ModifyObjects() error = nil, want conflict for %s", key)
		})
	}
}
