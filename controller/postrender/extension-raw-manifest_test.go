package postrender

import (
	"testing"

	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func TestRawManifestHandlerAppendsObjects(t *testing.T) {
	base := mustParseObjects(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: base
`)
	extension := appsv1.Extension{
		Name: "additional-resources",
		Kind: apps.ExtensionKindRawManifest,
		Params: map[string]string{
			apps.ExtensionParamManifest: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
---
apiVersion: v1
kind: Secret
metadata:
  name: second
`,
		},
	}

	got, err := (&RawManifestHandler{}).Handle(base, extension)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("objects = %d, want 3", len(got))
	}
	for i, name := range []string{"base", "first", "second"} {
		if got[i].GetName() != name {
			t.Fatalf("object %d name = %q, want %q", i, got[i].GetName(), name)
		}
	}
}

func TestRawManifestHandlerRejectsMissingOrInvalidManifest(t *testing.T) {
	tests := []appsv1.Extension{
		{Name: "missing", Kind: apps.ExtensionKindRawManifest},
		{Name: "empty", Kind: apps.ExtensionKindRawManifest, Params: map[string]string{apps.ExtensionParamManifest: "  \n"}},
		{Name: "invalid", Kind: apps.ExtensionKindRawManifest, Params: map[string]string{apps.ExtensionParamManifest: "metadata: ["}},
	}
	for _, extension := range tests {
		t.Run(extension.Name, func(t *testing.T) {
			if _, err := (&RawManifestHandler{}).Handle(nil, extension); err == nil {
				t.Fatal("Handle() error = nil")
			}
		})
	}
}

func TestExtensionRendererPreservesDeclaredOrder(t *testing.T) {
	baseManifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: base
`
	raw := appsv1.Extension{
		Name: "raw",
		Kind: apps.ExtensionKindRawManifest,
		Params: map[string]string{apps.ExtensionParamManifest: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: added
`},
	}
	metadata := appsv1.Extension{Name: "metadata", Kind: apps.ExtensionKindCommonMetadata}
	newRenderer := func(extensions []appsv1.Extension) *ExtensionRenderer {
		return &ExtensionRenderer{
			Extensions: extensions,
			Handlers: map[string]ExtensionHandler{
				apps.ExtensionKindRawManifest: &RawManifestHandler{},
				apps.ExtensionKindCommonMetadata: &CommonMetadataHandler{
					CommonLabels: map[string]string{"ordered": "true"},
				},
			},
		}
	}

	objects, err := newRenderer([]appsv1.Extension{metadata, raw}).ModifyObjects(mustParseObjects(t, baseManifest))
	if err != nil {
		t.Fatal(err)
	}
	if objects[0].GetLabels()["ordered"] != "true" {
		t.Fatal("base object was not modified")
	}
	if objects[1].GetLabels()["ordered"] != "" {
		t.Fatal("object added after CommonMetadata unexpectedly inherited metadata")
	}

	objects, err = newRenderer([]appsv1.Extension{raw, metadata}).ModifyObjects(mustParseObjects(t, baseManifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range objects {
		if obj.GetLabels()["ordered"] != "true" {
			t.Fatalf("object %q was not modified in declared order", obj.GetName())
		}
	}
}
