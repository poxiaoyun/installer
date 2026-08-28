package helm

import (
	"bytes"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/filesystem/osfs"
)

type appendTemplatePostRenderer struct{}

func (appendTemplatePostRenderer) Run(in *bytes.Buffer, _ *chart.Chart) (*bytes.Buffer, error) {
	in.WriteString("---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: added\n")
	return in, nil
}

func TestTemplateRunsPostRenderer(t *testing.T) {
	fsys := osfs.New()
	rendered, err := New(nil).Template(t.Context(), install.Instance{
		Name:         "template-test",
		Namespace:    "default",
		Location:     filesystem.Location{FS: fsys, Path: "../../testdata/helm-test"},
		Values:       map[string]any{"global": map[string]any{"replicas": 1, "paused": false}},
		PostRenderer: appendTemplatePostRenderer{},
	})
	if err != nil {
		t.Fatalf("Template() error = %v", err)
	}
	if !bytes.Contains(rendered, []byte("name: template-test-cm")) || !bytes.Contains(rendered, []byte("name: added")) {
		t.Fatalf("rendered manifests = %s", rendered)
	}
}
