package postrender

import (
	"fmt"
	"maps"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xiaoshiai.cn/installer/apis/apps"
)

// InstanceMembershipRenderer marks resources directly managed by a native
// Instance. Pod templates and selectors remain owned by the rendered source.
type InstanceMembershipRenderer struct {
	InstanceName      string
	InstanceNamespace string
}

// ModifyObjects adds native Instance membership to direct resources and
// rejects membership that names a different Instance.
func (r *InstanceMembershipRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	required := map[string]string{
		apps.AnnotationInstanceName:      r.InstanceName,
		apps.AnnotationInstanceNamespace: r.InstanceNamespace,
	}
	for _, obj := range objects {
		annotations := maps.Clone(obj.GetAnnotations())
		if annotations == nil {
			annotations = make(map[string]string, len(required))
		}
		for _, key := range []string{apps.AnnotationInstanceName, apps.AnnotationInstanceNamespace} {
			if actual, exists := annotations[key]; exists && actual != required[key] {
				return nil, fmt.Errorf(
					"%s %s/%s annotation %s is %q, want %q",
					obj.GetKind(), obj.GetNamespace(), obj.GetName(), key, actual, required[key],
				)
			}
			annotations[key] = required[key]
		}
		obj.SetAnnotations(annotations)
	}
	return objects, nil
}
