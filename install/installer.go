package install

import (
	"bytes"
	"context"
	"time"

	"helm.sh/helm/v3/pkg/chart"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

// PostRenderer is an interface for modifying rendered manifests before they are applied.
// It receives the loaded chart so renderers can access chart metadata or raw files.
type PostRenderer interface {
	Run(renderedManifests *bytes.Buffer, ch *chart.Chart) (modifiedManifests *bytes.Buffer, err error)
}

// PostRendererChain chains multiple PostRenderers sequentially.
type PostRendererChain []PostRenderer

func (chain PostRendererChain) Run(in *bytes.Buffer, ch *chart.Chart) (*bytes.Buffer, error) {
	out := in
	for _, pr := range chain {
		if pr == nil {
			continue
		}
		var err error
		out, err = pr.Run(out, ch)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type Instance struct {
	Name      string
	Namespace string
	Values    map[string]any

	Kind       InstanceKind
	Repository string
	Version    string
	Chart      string
	Path       string

	// Location is the local path where the bundle is located
	// installer should use this path to apply the bundle if exists
	Location string

	// Resources is the previously applied resources
	Resources         []ManagedResource
	CreationTimestamp time.Time
	UpgradeTimestamp  time.Time

	Options []Option

	// PostRenderer is an optional post-render pipeline applied to rendered manifests
	// before they are submitted to Kubernetes.
	PostRenderer PostRenderer
}

type Option = appsv1.Option

type InstanceStatus struct {
	Note              string
	Values            map[string]any
	Version           string
	AppVersion        string
	Namespace         string
	CreationTimestamp time.Time
	UpgradeTimestamp  time.Time
	Resources         []ManagedResource

	// ChartAnnotations from Chart.yaml metadata annotations.
	ChartAnnotations map[string]string
}

type ManagedResource = appsv1.ManagedResource

type InstanceKind = appsv1.InstanceKind

const (
	InstanceKindHelm      InstanceKind = "helm"
	InstanceKindKustomize InstanceKind = "kustomize"
	InstanceKindTemplate  InstanceKind = "template"
)

type Installer interface {
	Apply(ctx context.Context, bundle Instance) (*InstanceStatus, error)
	Remove(ctx context.Context, bundle Instance) error

	Template(ctx context.Context, bundle Instance) ([]byte, error)
}
