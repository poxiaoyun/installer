package postrender

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

// RawManifestHandler appends Kubernetes objects declared by a RawManifest extension.
type RawManifestHandler struct{}

func (h *RawManifestHandler) Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error) {
	manifest := strings.TrimSpace(ext.Params[apps.ExtensionParamManifest])
	if manifest == "" {
		return nil, fmt.Errorf("%s parameter is required", apps.ExtensionParamManifest)
	}
	additional, err := ParseObjects([]byte(manifest))
	if err != nil {
		return nil, fmt.Errorf("parse %s parameter: %w", apps.ExtensionParamManifest, err)
	}
	if len(additional) == 0 {
		return nil, fmt.Errorf("%s parameter contains no Kubernetes objects", apps.ExtensionParamManifest)
	}
	return append(objects, additional...), nil
}
