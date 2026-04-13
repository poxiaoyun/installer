package postrender

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

// ExtensionHandler processes a single extension kind against rendered objects.
type ExtensionHandler interface {
	Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error)
}

// ExtensionHandlerFunc adapts a plain function to the ExtensionHandler interface.
type ExtensionHandlerFunc func(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error)

func (f ExtensionHandlerFunc) Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error) {
	return f(objects, ext)
}

// ExtensionRenderer dispatches extensions to registered handlers by Kind.
// Unknown extension kinds are silently skipped.
type ExtensionRenderer struct {
	Extensions []appsv1.Extension
	Handlers   map[string]ExtensionHandler
}

func (e *ExtensionRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	for _, ext := range e.Extensions {
		handler, ok := e.Handlers[ext.Kind]
		if !ok {
			continue
		}
		var err error
		objects, err = handler.Handle(objects, ext)
		if err != nil {
			return nil, fmt.Errorf("apply %s extension %q: %w", ext.Kind, ext.Name, err)
		}
	}
	return objects, nil
}

// getParamKey returns the value of the first matching key found in params.
func getParamKey(params map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := params[k]; ok {
			return v
		}
	}
	return ""
}
