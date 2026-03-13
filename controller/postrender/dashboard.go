package postrender

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
	"xiaoshiai.cn/installer/install"
)

const DashboardConfigMapSuffix = "-dashboards"

// DashboardPostRenderer appends a dashboard ConfigMap extracted from the
// chart's dashboards/ folder to the rendered manifests.
type DashboardPostRenderer struct {
	Name      string
	Namespace string
}

var _ install.PostRenderer = DashboardPostRenderer{}

func (d DashboardPostRenderer) Run(in *bytes.Buffer, ch *chart.Chart) (*bytes.Buffer, error) {
	dashboards := extractDashboardFiles(ch)
	if len(dashboards) == 0 {
		return in, nil
	}

	cm := buildDashboardConfigMap(d.Name, d.Namespace, dashboards)
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cm)
	if err != nil {
		return nil, fmt.Errorf("convert dashboard configmap: %w", err)
	}
	obj := &unstructured.Unstructured{Object: u}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")

	yamlBytes, err := yaml.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("marshal dashboard configmap: %w", err)
	}
	in.WriteString("---\n")
	in.Write(yamlBytes)
	return in, nil
}

// extractDashboardFiles extracts dashboard files from a helm chart's dashboards/ folder.
func extractDashboardFiles(ch *chart.Chart) map[string][]byte {
	if ch == nil {
		return nil
	}
	dashboards := map[string][]byte{}
	for _, file := range ch.Raw {
		if !strings.HasPrefix(file.Name, "dashboards/") {
			continue
		}
		ext := path.Ext(file.Name)
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			continue
		}
		name := strings.TrimSuffix(path.Base(file.Name), ext)
		dashboards[name] = file.Data
	}
	if len(dashboards) == 0 {
		return nil
	}
	return dashboards
}

// buildDashboardConfigMap creates a ConfigMap containing dashboard data.
func buildDashboardConfigMap(instanceName, namespace string, dashboards map[string][]byte) *corev1.ConfigMap {
	data := make(map[string]string, len(dashboards))
	for name, content := range dashboards {
		data[name] = string(content)
	}
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      instanceName + DashboardConfigMapSuffix,
			Namespace: namespace,
		},
		Data: data,
	}
}
