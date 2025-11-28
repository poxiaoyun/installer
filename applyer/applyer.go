package applyer

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/applyer/helm"
	"xiaoshiai.cn/installer/applyer/kustomize"
	"xiaoshiai.cn/installer/applyer/native"
	"xiaoshiai.cn/installer/applyer/template"
)

type Apply interface {
	Apply(ctx context.Context, bundle *appsv1.Instance, into string) error
	Remove(ctx context.Context, bundle *appsv1.Instance) error
	Template(ctx context.Context, bundle *appsv1.Instance, into string) ([]byte, error)
}

type BundleApplier struct {
	Options  *Options
	appliers map[appsv1.InstanceKind]Apply
}

type Options struct {
	CacheDir string
}

func NewDefaultOptions() *Options {
	return &Options{CacheDir: ""}
}

func NewDefaultApply(cfg *rest.Config, cli client.Client, options *Options) *BundleApplier {
	return &BundleApplier{
		Options: options,
		appliers: map[appsv1.InstanceKind]Apply{
			appsv1.InstanceKindHelm:      helm.New(cfg),
			appsv1.InstanceKindKustomize: native.New(cli, kustomize.KustomizeBuildFunc),
			appsv1.InstanceKindTemplate:  native.New(cli, template.NewTemplaterFunc(cfg)),
		},
	}
}

func (b *BundleApplier) Template(ctx context.Context, bundle *appsv1.Instance) ([]byte, error) {
	into, err := b.Download(ctx, bundle)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if apply, ok := b.appliers[bundle.Spec.Kind]; ok {
		return apply.Template(ctx, bundle, into)
	}
	return nil, fmt.Errorf("unknown bundle kind: %s", bundle.Spec.Kind)
}

func (b *BundleApplier) Download(ctx context.Context, bundle *appsv1.Instance) (string, error) {
	name := bundle.Name
	if chart := bundle.Spec.Chart; chart != "" {
		name = chart
	}
	return Download(ctx,
		bundle.Spec.URL,
		name,
		bundle.Spec.Version,
		bundle.Spec.Path,
		b.Options.CacheDir,
	)
}

func (b *BundleApplier) Apply(ctx context.Context, instance *appsv1.Instance) error {
	into, err := b.Download(ctx, instance)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if apply, ok := b.appliers[instance.Spec.Kind]; ok {
		return apply.Apply(ctx, instance, into)
	}
	return fmt.Errorf("unknown bundle kind: %s", instance.Spec.Kind)
}

func (b *BundleApplier) Remove(ctx context.Context, instance *appsv1.Instance) error {
	if apply, ok := b.appliers[instance.Spec.Kind]; ok {
		return apply.Remove(ctx, instance)
	}
	return fmt.Errorf("unknown bundle kind: %s", instance.Spec.Kind)
}
