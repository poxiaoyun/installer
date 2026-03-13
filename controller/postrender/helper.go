package postrender

import (
	"bytes"
	"fmt"
	"io"
	"maps"

	"helm.sh/helm/v3/pkg/chart"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubeyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/install"
)

// ObjectModifier modifies a list of unstructured Kubernetes objects in-place.
type ObjectModifier interface {
	ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error)
}

// CompositeRenderer chains multiple ObjectModifiers into a single install.PostRenderer.
type CompositeRenderer struct {
	Modifiers []ObjectModifier
}

var _ install.PostRenderer = &CompositeRenderer{}

// Run parses the rendered manifests, applies each modifier in order, and serializes back.
// The chart parameter is ignored; object modifiers operate on rendered YAML only.
func (c CompositeRenderer) Run(renderedManifests *bytes.Buffer, _ *chart.Chart) (*bytes.Buffer, error) {
	objects, err := ParseObjects(renderedManifests.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse manifests: %w", err)
	}
	for _, modifier := range c.Modifiers {
		objects, err = modifier.ModifyObjects(objects)
		if err != nil {
			return nil, fmt.Errorf("post-render modifier: %w", err)
		}
	}
	return SerializeObjects(objects)
}

// ParseObjects splits a multi-document YAML byte stream into unstructured objects.
func ParseObjects(data []byte) ([]*unstructured.Unstructured, error) {
	d := kubeyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var objects []*unstructured.Unstructured
	for {
		u := &unstructured.Unstructured{}
		if err := d.Decode(u); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode manifest: %w", err)
		}
		if len(u.Object) == 0 {
			continue
		}
		objects = append(objects, u)
	}
	return objects, nil
}

// SerializeObjects writes a list of unstructured objects back to a multi-document YAML buffer.
func SerializeObjects(objects []*unstructured.Unstructured) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	for _, obj := range objects {
		yamlBytes, err := yaml.Marshal(obj.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal object %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		buf.WriteString("---\n")
		buf.Write(yamlBytes)
	}
	return buf, nil
}

// MergeLabels merges src labels into dst, creating the map if needed.
func MergeLabels(dst, src map[string]string) map[string]string {
	if dst == nil {
		dst = make(map[string]string, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

// IsGroupKind checks whether the unstructured object matches the given group and kind.
func IsGroupKind(obj *unstructured.Unstructured, group, kind string) bool {
	gvk := obj.GroupVersionKind()
	return gvk.Group == group && gvk.Kind == kind
}
