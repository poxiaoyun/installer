package postrender

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// PausedRenderer modifies workload objects to achieve a "paused" state:
//   - Deployment/StatefulSet: sets spec.replicas = 0
//   - Job: sets spec.suspend = true
type PausedRenderer struct {
	Paused bool
}

func (p *PausedRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	if !p.Paused {
		return objects, nil
	}
	for _, obj := range objects {
		switch obj.GetKind() {
		case "Deployment", "StatefulSet":
			_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "replicas")
		case "Job":
			_ = unstructured.SetNestedField(obj.Object, true, "spec", "suspend")
		}
	}
	return objects, nil
}
