package install

import (
	"context"
	"time"

	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

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
}

type InstanceStatus struct {
	Note              string
	Values            map[string]any
	Version           string
	AppVersion        string
	Namespace         string
	CreationTimestamp time.Time
	UpgradeTimestamp  time.Time
	Resources         []ManagedResource
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
