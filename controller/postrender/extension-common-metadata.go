package postrender

import (
	"fmt"
	"maps"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

// CommonMetadataHandler enables global.commonLabels and
// global.commonAnnotations injection for an Instance.
type CommonMetadataHandler struct {
	CommonLabels      map[string]string
	CommonAnnotations map[string]string
}

func (h *CommonMetadataHandler) Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error) {
	injectPodTemplates, err := extensionBoolParam(ext.Params, "podTemplates", true)
	if err != nil {
		return nil, err
	}
	injectVolumeClaimTemplates, err := extensionBoolParam(ext.Params, "volumeClaimTemplates", true)
	if err != nil {
		return nil, err
	}
	return (&CommonMetadataRenderer{
		CommonLabels:               h.CommonLabels,
		CommonAnnotations:          h.CommonAnnotations,
		InjectPodTemplates:         injectPodTemplates,
		InjectVolumeClaimTemplates: injectVolumeClaimTemplates,
	}).ModifyObjects(objects)
}

// CommonMetadataRenderer applies user-provided metadata to rendered resources.
// Selectors are deliberately not changed because workload selectors are
// immutable and charts may already be installed.
type CommonMetadataRenderer struct {
	CommonLabels               map[string]string
	CommonAnnotations          map[string]string
	InjectPodTemplates         bool
	InjectVolumeClaimTemplates bool
}

func (r *CommonMetadataRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	labels := make(map[string]string, len(r.CommonLabels))
	maps.Copy(labels, r.CommonLabels)
	// Instance identity belongs to InstanceIdentityRenderer and cannot be
	// overridden through global.commonLabels.
	delete(labels, LabelInstance)

	for _, obj := range objects {
		if len(labels) != 0 {
			obj.SetLabels(MergeLabels(obj.GetLabels(), labels))
		}
		obj.SetAnnotations(mergeStringMap(obj.GetAnnotations(), r.CommonAnnotations))

		if r.InjectPodTemplates {
			for _, path := range podTemplateMetadataPaths(obj) {
				mergeNestedMetadata(obj.Object, labels, r.CommonAnnotations, path...)
			}
		}

		if r.InjectVolumeClaimTemplates && IsGroupKind(obj, "apps", "StatefulSet") {
			injectVolumeClaimTemplateMetadata(obj.Object, labels, r.CommonAnnotations)
		}
	}
	return objects, nil
}

func extensionBoolParam(params map[string]string, name string, defaultValue bool) (bool, error) {
	raw, exists := params[name]
	if !exists {
		return defaultValue, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", name, raw)
	}
	return value, nil
}

func mergeStringMap(dst, src map[string]string) map[string]string {
	if dst == nil && len(src) != 0 {
		dst = make(map[string]string, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

func podTemplateMetadataPaths(obj *unstructured.Unstructured) [][]string {
	switch {
	case obj.GroupVersionKind().Group == "apps" &&
		(obj.GetKind() == "Deployment" || obj.GetKind() == "StatefulSet" ||
			obj.GetKind() == "DaemonSet" || obj.GetKind() == "ReplicaSet"):
		return [][]string{{"spec", "template", "metadata"}}
	case IsGroupKind(obj, "batch", "Job"):
		return [][]string{{"spec", "template", "metadata"}}
	case IsGroupKind(obj, "batch", "CronJob"):
		return [][]string{{"spec", "jobTemplate", "spec", "template", "metadata"}}
	default:
		return nil
	}
}

// mergeNestedMetadata creates a metadata object at path and merges labels and
// annotations into it. The structural parents (spec/template/etc.) must exist;
// malformed resources are left untouched rather than being reshaped.
func mergeNestedMetadata(obj map[string]any, labels, annotations map[string]string, path ...string) {
	metadata, ok := nestedMap(obj, true, path...)
	if !ok {
		return
	}
	mergeStringMapField(metadata, "labels", labels)
	mergeStringMapField(metadata, "annotations", annotations)
}

func nestedMap(obj map[string]any, createLast bool, path ...string) (map[string]any, bool) {
	current := obj
	for i, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			if !createLast || i != len(path)-1 || current[key] != nil {
				return nil, false
			}
			next = make(map[string]any)
			current[key] = next
		}
		current = next
	}
	return current, true
}

func mergeStringMapField(obj map[string]any, field string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	existing, _ := obj[field].(map[string]any)
	if existing == nil {
		existing = make(map[string]any, len(values))
		obj[field] = existing
	}
	for key, value := range values {
		existing[key] = value
	}
}

func injectVolumeClaimTemplateMetadata(obj map[string]any, labels, annotations map[string]string) {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return
	}
	vcts, ok := spec["volumeClaimTemplates"].([]any)
	if !ok {
		return
	}
	for _, raw := range vcts {
		vct, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		mergeNestedMetadata(vct, labels, annotations, "metadata")
	}
}
