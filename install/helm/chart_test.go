package helm

import (
	"path/filepath"
	"strings"
	"testing"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/filesystem/osfs"
)

func TestLoadChartRejectsMissingDependencies(t *testing.T) {
	root := t.TempDir()
	parent := &chart.Chart{Metadata: &chart.Metadata{
		APIVersion: "v2",
		Name:       "parent",
		Version:    "1.0.0",
		Dependencies: []*chart.Dependency{{
			Name:       "child",
			Version:    "1.0.0",
			Repository: "https://example.test/charts",
		}},
	}}
	if err := chartutil.SaveDir(parent, root); err != nil {
		t.Fatalf("save parent chart: %v", err)
	}
	_, err := LoadChart(filesystem.Location{FS: osfs.New(), Path: filepath.Join(root, "parent")})
	if err == nil || !strings.Contains(err.Error(), "chart dependencies are incomplete") {
		t.Fatalf("LoadChart() error = %v", err)
	}
}
