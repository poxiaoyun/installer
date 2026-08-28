package install

import (
	"bytes"
	"context"
	"time"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install/filesystem"
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
	Artifact   *appsv1.Artifact

	Location filesystem.Location

	// Resources is the previously applied resources
	Resources         []ManagedResource
	CreationTimestamp time.Time
	UpgradeTimestamp  time.Time

	Options []Option

	// Auth holds resolved credentials for the URL source.
	Auth *ResolvedAuth

	// TLS holds resolved TLS settings for URL source downloads.
	TLS *ResolvedTLS

	// PostRenderer is an optional post-render pipeline applied to rendered manifests
	// before they are submitted to Kubernetes.
	PostRenderer PostRenderer
}

// ResolvedAuth contains plain-text source credentials resolved from the Instance spec.
type ResolvedAuth struct {
	Token    string
	Username string
	Password string
}

// ResolvedTLS contains URL source TLS settings resolved from the Instance spec.
type ResolvedTLS struct {
	CAData             []byte
	CertData           []byte
	KeyData            []byte
	InsecureSkipVerify bool
}

type Option = appsv1.Option

type InstanceStatus struct {
	Note              string
	Values            map[string]any
	Version           string
	AppVersion        string
	ArtifactDigest    string
	Namespace         string
	CreationTimestamp time.Time
	UpgradeTimestamp  time.Time
	Resources         []ManagedResource
}

type ManagedResource = appsv1.ManagedResource

type InstanceKind = appsv1.InstanceKind

type Installer interface {
	Apply(ctx context.Context, bundle Instance) (*InstanceStatus, error)
	Remove(ctx context.Context, bundle Instance) error

	Template(ctx context.Context, bundle Instance) ([]byte, error)
}
