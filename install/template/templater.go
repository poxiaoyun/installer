package template

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chartcommonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
)

func NewTemplaterFunc(cfg *rest.Config) func(ctx context.Context, instance install.Instance) ([]byte, error) {
	return Templater{Config: cfg}.Template
}

type Templater struct {
	Config *rest.Config
	DC     discovery.CachedDiscoveryInterface
}

// TemplatesTemplate using helm template engine to render,but allow apply to different namespaces
func (t Templater) Template(ctx context.Context, instance install.Instance) ([]byte, error) {
	chart, err := Load(instance.Name, instance.Version, instance.Location)
	if err != nil {
		return nil, err
	}
	vals := instance.Values
	options := chartcommon.ReleaseOptions{
		Name:      instance.Name,
		Namespace: instance.Namespace,
		IsInstall: true,
	}

	caps := chartcommon.DefaultCapabilities.Copy()

	if t.Config != nil && t.DC == nil {
		cs, err := kubernetes.NewForConfig(t.Config)
		if err != nil {
			return nil, err
		}
		t.DC = memory.NewMemCacheClient(cs)
	}

	if dc := t.DC; dc != nil {
		kubeVersion, err := dc.ServerVersion()
		if err != nil {
			return nil, err
		}
		apiVersions, err := action.GetVersionSet(dc)
		if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
			return nil, fmt.Errorf("could not get apiVersions from Kubernetes: %w", err)
		}
		caps.APIVersions = apiVersions
		caps.KubeVersion = chartcommon.KubeVersion{
			Version: kubeVersion.GitVersion,
			Major:   kubeVersion.Major,
			Minor:   kubeVersion.Minor,
		}
	}

	if err := chartutil.ProcessDependencies(chart, vals); err != nil {
		return nil, err
	}
	valuesToRender, err := chartcommonutil.ToRenderValues(chart, vals, options, caps)
	if err != nil {
		return nil, err
	}

	var renderdFiles map[string]string
	if t.Config != nil {
		renderdFiles, err = engine.RenderWithClient(chart, valuesToRender, t.Config)
		if err != nil {
			return nil, err
		}
	} else {
		renderdFiles, err = engine.Render(chart, valuesToRender)
		if err != nil {
			return nil, err
		}
	}

	for k := range renderdFiles {
		if strings.HasSuffix(k, "NOTES.txt") {
			delete(renderdFiles, k)
		}
	}

	_, manifests, err := releaseutil.SortManifests(renderdFiles, caps.APIVersions, releaseutil.InstallOrder)
	if err != nil {
		out := os.Stderr
		for file, val := range renderdFiles {
			fmt.Fprintf(out, "---\n# Source: %s\n%s\n", file, val)
		}
		fmt.Fprintln(out, "---")
		return nil, err
	}
	out := bytes.NewBuffer(nil)
	for _, crd := range chart.CRDObjects() {
		fmt.Fprintf(out, "---\n# Source: %s\n%s\n", crd.Name, string(crd.File.Data[:]))
	}
	for _, m := range manifests {
		fmt.Fprintf(out, "---\n# Source: %s\n%s\n", m.Name, m.Content)
	}
	return out.Bytes(), nil
}

const chartFileName = "Chart.yaml"

func Load(name, version string, location filesystem.Location) (*chart.Chart, error) {
	if version == "" {
		version = "0.0.0"
	}
	info, err := location.FS.Stat(location.Path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		file, err := location.FS.Open(location.Path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return loader.LoadArchive(file)
	}
	containsChartFile := false
	files := []*archive.BufferedFile{}
	walk := func(filename string, entry fs.DirEntry, err error) error {
		relfilename := strings.TrimPrefix(strings.TrimPrefix(filename, location.Path), "/")
		if relfilename == "" {
			return nil
		}
		if relfilename == chartFileName {
			containsChartFile = true
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := location.FS.ReadFile(filename)
		if err != nil {
			return err
		}
		files = append(files, &archive.BufferedFile{Name: relfilename, Data: data})
		return nil
	}
	if err = fs.WalkDir(location.FS, path.Clean(location.Path), walk); err != nil {
		return nil, err
	}
	if !containsChartFile {
		chartfile := chart.Metadata{
			APIVersion: chart.APIVersionV2,
			Name:       name,
			Version:    version,
		}
		chartfilecontent, err := yaml.Marshal(chartfile)
		if err != nil {
			return nil, err
		}
		files = append(files, &archive.BufferedFile{Name: chartFileName, Data: chartfilecontent})
	}
	return loader.LoadFiles(files)
}
