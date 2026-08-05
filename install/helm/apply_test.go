package helm

import (
	"bytes"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"xiaoshiai.cn/installer/install"
)

type appendTemplatePostRenderer struct{}

func (appendTemplatePostRenderer) Run(in *bytes.Buffer, _ *chart.Chart) (*bytes.Buffer, error) {
	in.WriteString("---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: added\n")
	return in, nil
}

func TestTemplateRunsPostRenderer(t *testing.T) {
	rendered, err := New(nil).Template(t.Context(), install.Instance{
		Name:         "template-test",
		Namespace:    "default",
		Location:     "../../testdata/helm-test",
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
