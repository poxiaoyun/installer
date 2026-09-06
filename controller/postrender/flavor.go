package postrender

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	chartutil "helm.sh/helm/v4/pkg/chart/common/util"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appbase "xiaoshiai.cn/installer/apis/apps"
)

const acceleratorFlavorType = "Accelerator"

// FlavorRenderer projects Pod runtime settings from a resolved Flavor value
// onto workloads that explicitly declare their Flavor value path.
type FlavorRenderer struct {
	Values map[string]any
}

// Run resolves pointers against the same coalesced values Helm uses, including
// optional roles supplied only by chart defaults. Native sources have no chart.
// CoalesceValues copies its inputs; rendering never changes Instance values.
func (r *FlavorRenderer) Run(in *bytes.Buffer, ch *chart.Chart) (*bytes.Buffer, error) {
	values := r.Values
	if ch != nil {
		var err error
		values, err = chartutil.CoalesceValues(ch, values)
		if err != nil {
			return nil, fmt.Errorf("coalesce Flavor values: %w", err)
		}
	}
	return (CompositeRenderer{Modifiers: []ObjectModifier{&FlavorRenderer{Values: values}}}).Run(in, ch)
}

func (r *FlavorRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	for _, object := range objects {
		path := object.GetAnnotations()[appbase.AnnotationFlavorPath]
		if path == "" {
			continue
		}
		podSpecPath, podMetadataPath, supported := flavorWorkloadPaths(object)
		if !supported {
			return nil, fmt.Errorf("resource %s/%s declares %s but is not a supported Pod workload", object.GetKind(), object.GetName(), appbase.AnnotationFlavorPath)
		}
		flavor, err := resolveFlavorValue(r.Values, path)
		if err != nil {
			return nil, fmt.Errorf("workload %s/%s: %w", object.GetKind(), object.GetName(), err)
		}
		if flavor.Type != acceleratorFlavorType {
			continue
		}
		if err := applyFlavorRuntime(object, podSpecPath, podMetadataPath, flavor); err != nil {
			return nil, err
		}
	}
	return objects, nil
}

type resolvedFlavorRuntime struct {
	Type             string
	PodLabels        map[string]string
	RuntimeClassName string
	SchedulerName    string
}

func resolveFlavorValue(values map[string]any, pointer string) (resolvedFlavorRuntime, error) {
	value, err := resolveJSONPointer(values, pointer)
	if err != nil {
		return resolvedFlavorRuntime{}, fmt.Errorf("resolve Flavor path %q: %w", pointer, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q resolved to %T, want object", pointer, value)
	}
	flavor := resolvedFlavorRuntime{}
	if flavor.Type, err = optionalString(object, "type"); err != nil {
		return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q: %w", pointer, err)
	}
	if flavor.RuntimeClassName, err = optionalString(object, "runtimeClassName"); err != nil {
		return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q: %w", pointer, err)
	}
	if flavor.SchedulerName, err = optionalString(object, "schedulerName"); err != nil {
		return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q: %w", pointer, err)
	}
	if raw, found := object["podLabels"]; found && raw != nil {
		labels, ok := raw.(map[string]any)
		if !ok {
			return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q field podLabels has type %T, want object", pointer, raw)
		}
		flavor.PodLabels = make(map[string]string, len(labels))
		for key, rawValue := range labels {
			value, ok := rawValue.(string)
			if !ok {
				return resolvedFlavorRuntime{}, fmt.Errorf("Flavor path %q pod label %q has type %T, want string", pointer, key, rawValue)
			}
			flavor.PodLabels[key] = value
		}
	}
	return flavor, nil
}

func resolveJSONPointer(document any, pointer string) (any, error) {
	if pointer == "" {
		return document, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("must be an RFC 6901 JSON Pointer starting with '/'")
	}
	current := document
	for _, encodedToken := range strings.Split(pointer[1:], "/") {
		token, err := decodeJSONPointerToken(encodedToken)
		if err != nil {
			return nil, err
		}
		switch typed := current.(type) {
		case map[string]any:
			value, found := typed[token]
			if !found {
				return nil, fmt.Errorf("field %q does not exist", token)
			}
			current = value
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("array index %q is invalid", token)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("cannot select %q from %T", token, current)
		}
	}
	return current, nil
}

func decodeJSONPointerToken(token string) (string, error) {
	var decoded strings.Builder
	for index := 0; index < len(token); index++ {
		if token[index] != '~' {
			decoded.WriteByte(token[index])
			continue
		}
		if index+1 >= len(token) {
			return "", fmt.Errorf("invalid JSON Pointer escape in token %q", token)
		}
		index++
		switch token[index] {
		case '0':
			decoded.WriteByte('~')
		case '1':
			decoded.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid JSON Pointer escape in token %q", token)
		}
	}
	return decoded.String(), nil
}

func optionalString(object map[string]any, field string) (string, error) {
	value, found := object[field]
	if !found || value == nil {
		return "", nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("field %s has type %T, want string", field, value)
	}
	return stringValue, nil
}

func flavorWorkloadPaths(object *unstructured.Unstructured) (podSpec, podMetadata []string, supported bool) {
	gvk := object.GroupVersionKind()
	switch {
	case gvk.Group == "" && gvk.Kind == "Pod":
		return []string{"spec"}, []string{"metadata"}, true
	case gvk.Group == "apps" && (gvk.Kind == "Deployment" || gvk.Kind == "StatefulSet" || gvk.Kind == "DaemonSet" || gvk.Kind == "ReplicaSet"):
		return []string{"spec", "template", "spec"}, []string{"spec", "template", "metadata"}, true
	case gvk.Group == "batch" && gvk.Kind == "Job":
		return []string{"spec", "template", "spec"}, []string{"spec", "template", "metadata"}, true
	case gvk.Group == "batch" && gvk.Kind == "CronJob":
		return []string{"spec", "jobTemplate", "spec", "template", "spec"}, []string{"spec", "jobTemplate", "spec", "template", "metadata"}, true
	default:
		return nil, nil, false
	}
}

func applyFlavorRuntime(object *unstructured.Unstructured, podSpecPath, podMetadataPath []string, flavor resolvedFlavorRuntime) error {
	if err := setFlavorString(object, append(podSpecPath, "schedulerName"), flavor.SchedulerName); err != nil {
		return err
	}
	if err := setFlavorString(object, append(podSpecPath, "runtimeClassName"), flavor.RuntimeClassName); err != nil {
		return err
	}
	labelsPath := append(podMetadataPath, "labels")
	existing, found, err := unstructured.NestedStringMap(object.Object, labelsPath...)
	if err != nil {
		return fmt.Errorf("read Pod labels on %s/%s: %w", object.GetKind(), object.GetName(), err)
	}
	if !found {
		existing = map[string]string{}
	}
	for key, value := range flavor.PodLabels {
		if current, exists := existing[key]; exists && current != value {
			return fmt.Errorf("workload %s/%s Pod label %q conflicts with Flavor value %q", object.GetKind(), object.GetName(), key, value)
		}
		existing[key] = value
	}
	if len(flavor.PodLabels) > 0 {
		if err := unstructured.SetNestedStringMap(object.Object, existing, labelsPath...); err != nil {
			return fmt.Errorf("set Pod labels on %s/%s: %w", object.GetKind(), object.GetName(), err)
		}
	}
	return nil
}

func setFlavorString(object *unstructured.Unstructured, path []string, desired string) error {
	if desired == "" {
		return nil
	}
	existing, found, err := unstructured.NestedString(object.Object, path...)
	if err != nil {
		return fmt.Errorf("read %s on %s/%s: %w", path[len(path)-1], object.GetKind(), object.GetName(), err)
	}
	if found && existing != "" && existing != desired {
		return fmt.Errorf("workload %s/%s has conflicting %s %q; Flavor requires %q", object.GetKind(), object.GetName(), path[len(path)-1], existing, desired)
	}
	if err := unstructured.SetNestedField(object.Object, desired, path...); err != nil {
		return fmt.Errorf("set %s on %s/%s: %w", path[len(path)-1], object.GetKind(), object.GetName(), err)
	}
	return nil
}
