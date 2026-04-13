package postrender

import (
	"maps"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

const (
	// LabelInstance is the label key for the instance name.
	LabelInstance = "app.kubernetes.io/instance"
	// LabelManagedBy is the label key for the managed-by annotation.
	LabelManagedBy = "app.kubernetes.io/managed-by"
)

// LabelsHandler creates a labelsRenderer from extension params.
type LabelsHandler struct {
	InstanceName string
	CommonLabels map[string]string
}

func (h *LabelsHandler) Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error) {
	renderer := &labelsRenderer{
		instanceName:   h.InstanceName,
		commonLabels:   h.CommonLabels,
		injectSelector: getParamKey(ext.Params, "selector") == "true",
	}
	return renderer.modifyObjects(objects)
}

// labelsRenderer injects CommonLabels and instance label into all rendered objects,
// including pod templates, volume claim templates, and optionally pod selectors.
type labelsRenderer struct {
	instanceName   string
	commonLabels   map[string]string
	injectSelector bool
}

func (l *labelsRenderer) modifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	labels := make(map[string]string, len(l.commonLabels)+1)
	maps.Copy(labels, l.commonLabels)
	labels[LabelInstance] = l.instanceName

	instanceLabel := map[string]string{LabelInstance: l.instanceName}

	for _, obj := range objects {
		// inject top-level labels
		obj.SetLabels(MergeLabels(obj.GetLabels(), labels))

		// inject pod template labels (only if the path already exists)
		mergeNestedLabels(obj.Object, labels, "spec", "template", "metadata", "labels")

		// inject pod selector matchLabels only when explicitly enabled.
		// Only workload resources (Deployment, StatefulSet, etc.) are modified;
		// CRDs like ServiceMonitor/PodMonitor must NOT be touched.
		if l.injectSelector && isWorkloadKind(obj) {
			mergeNestedLabels(obj.Object, instanceLabel, "spec", "selector", "matchLabels")
		}

		// inject volumeClaimTemplate labels for StatefulSets
		if IsGroupKind(obj, "apps", "StatefulSet") {
			injectVolumeClaimTemplateLabels(obj.Object, labels)
		}
	}
	return objects, nil
}

// isWorkloadKind returns true for core workload kinds whose spec.selector.matchLabels
// is a pod selector that should track the instance label.
func isWorkloadKind(obj *unstructured.Unstructured) bool {
	gvk := obj.GroupVersionKind()
	switch gvk.Group {
	case "apps":
		return gvk.Kind == "Deployment" || gvk.Kind == "StatefulSet" ||
			gvk.Kind == "DaemonSet" || gvk.Kind == "ReplicaSet"
	case "batch":
		return gvk.Kind == "Job"
	default:
		return false
	}
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
