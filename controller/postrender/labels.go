package postrender

import (
	"maps"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// LabelInstance is the label key for the instance name.
	LabelInstance = "app.kubernetes.io/instance"
	// LabelManagedBy is the label key for the managed-by annotation.
	LabelManagedBy = "app.kubernetes.io/managed-by"
)

// LabelsRenderer injects CommonLabels and instance label into all rendered objects,
// including pod templates, volume claim templates, and pod selectors.
type LabelsRenderer struct {
	// InstanceName is the name of the instance, always injected as LabelInstance.
	InstanceName string
	// CommonLabels is user-provided labels to inject into all resources.
	CommonLabels map[string]string
}

func (l *LabelsRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	labels := make(map[string]string, len(l.CommonLabels)+1)
	maps.Copy(labels, l.CommonLabels)
	labels[LabelInstance] = l.InstanceName

	instanceLabel := map[string]string{LabelInstance: l.InstanceName}

	for _, obj := range objects {
		// inject top-level labels
		obj.SetLabels(MergeLabels(obj.GetLabels(), labels))

		// inject pod template labels (only if the path already exists)
		mergeNestedLabels(obj.Object, labels, "spec", "template", "metadata", "labels")

		// inject pod selector matchLabels (instance label only, only if matchLabels already exists)
		mergeNestedLabels(obj.Object, instanceLabel, "spec", "selector", "matchLabels")

		// inject volumeClaimTemplate labels for StatefulSets
		if IsGroupKind(obj, "apps", "StatefulSet") {
			injectVolumeClaimTemplateLabels(obj.Object, labels)
		}
	}
	return objects, nil
}

// mergeNestedLabels merges labels into a nested map[string]any path only
// if every key in the path already exists. It never creates missing keys.
func mergeNestedLabels(obj map[string]any, labels map[string]string, path ...string) {
	current := obj
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	for k, v := range labels {
		current[k] = v
	}
}

// injectVolumeClaimTemplateLabels injects labels into StatefulSet volumeClaimTemplates.
func injectVolumeClaimTemplateLabels(obj map[string]any, labels map[string]string) {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return
	}
	vcts, ok := spec["volumeClaimTemplates"].([]any)
	if !ok {
		return
	}
	for _, vctRaw := range vcts {
		vct, ok := vctRaw.(map[string]any)
		if !ok {
			continue
		}
		md, ok := vct["metadata"].(map[string]any)
		if !ok {
			md = make(map[string]any)
			vct["metadata"] = md
		}
		existing, _ := md["labels"].(map[string]any)
		if existing == nil {
			existing = make(map[string]any, len(labels))
		}
		for k, v := range labels {
			existing[k] = v
		}
		md["labels"] = existing
	}
}
