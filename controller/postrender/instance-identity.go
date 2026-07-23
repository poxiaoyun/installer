package postrender

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

const LabelInstance = "app.kubernetes.io/instance"

// InstanceIdentityRenderer applies the installer-controlled instance label to
// rendered resources and Pod templates. It is a platform invariant rather
// than an opt-in extension because resource event routing depends on it.
type InstanceIdentityRenderer struct {
	InstanceName string
}

func (r *InstanceIdentityRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	labels := map[string]string{LabelInstance: r.InstanceName}
	for _, obj := range objects {
		obj.SetLabels(MergeLabels(obj.GetLabels(), labels))
		for _, path := range podTemplateMetadataPaths(obj) {
			mergeNestedMetadata(obj.Object, labels, nil, path...)
		}
	}
	return objects, nil
}
