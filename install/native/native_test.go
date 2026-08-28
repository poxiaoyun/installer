package native

import (
	"bytes"
	"context"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	"xiaoshiai.cn/installer/install"
)

type appendPostRenderer struct{}

func (appendPostRenderer) Run(in *bytes.Buffer, _ *chart.Chart) (*bytes.Buffer, error) {
	in.WriteString("---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: added\n")
	return in, nil
}

func TestTemplateRunsPostRenderer(t *testing.T) {
	apply := &Apply{TemplateFun: func(context.Context, install.Instance) ([]byte, error) {
		return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: base\n"), nil
	}}

	rendered, err := apply.Template(t.Context(), install.Instance{PostRenderer: appendPostRenderer{}})
	if err != nil {
		t.Fatalf("Template() error = %v", err)
	}
	if !bytes.Contains(rendered, []byte("name: base")) || !bytes.Contains(rendered, []byte("name: added")) {
		t.Fatalf("rendered manifests = %s", rendered)
	}
}
