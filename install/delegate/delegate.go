package delegate

import (
	"context"
	"fmt"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/download"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/filesystem/osfs"
	"xiaoshiai.cn/installer/install/helm"
	"xiaoshiai.cn/installer/install/kustomize"
	"xiaoshiai.cn/installer/install/native"
	"xiaoshiai.cn/installer/install/template"
)

type Options struct {
	CacheDir string
}

func NewDefaultOptions() *Options {
	return &Options{CacheDir: ""}
}

func NewDelegate(cfg *rest.Config, cli client.Client, options *Options) *BundleApplier {
	fsys := osfs.New()
	downloader := download.NewDownloader(options.CacheDir, fsys)
	return &BundleApplier{
		appliers: map[appsv1.InstanceKind]install.Installer{
			appsv1.InstanceKindHelm:      helm.New(cfg),
			appsv1.InstanceKindKustomize: native.New(cli, kustomize.KustomizeBuildFunc),
			appsv1.InstanceKindTemplate:  native.New(cli, template.NewTemplaterFunc(cfg)),
		},
		downloader:     downloader,
		artifactLoader: download.NewArtifactLoader(cli),
	}
}

type BundleApplier struct {
	appliers       map[appsv1.InstanceKind]install.Installer
	downloader     *download.Downloader
	artifactLoader *download.ArtifactLoader
}

var _ install.Installer = &BundleApplier{}

func (b *BundleApplier) Template(ctx context.Context, instance install.Instance) ([]byte, error) {
	into, _, err := b.resolveLocation(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	instance.Location = into
	if apply, ok := b.appliers[instance.Kind]; ok {
		return apply.Template(ctx, instance)
	}
	return nil, fmt.Errorf("unknown bundle kind: %s", instance.Kind)
}

func (b *BundleApplier) Download(ctx context.Context, instance install.Instance) (filesystem.Location, error) {
	name := instance.Chart
	if name == "" {
		name = instance.Name
	}
	return b.downloader.Download(ctx, download.DownloadOptions{
		URL:     instance.Repository,
		Name:    name,
		Version: instance.Version,
		Subpath: instance.Path,
		Auth:    instance.Auth,
		TLS:     instance.TLS,
	})
}

func (b *BundleApplier) resolveLocation(ctx context.Context, instance install.Instance) (filesystem.Location, string, error) {
	if instance.Artifact != nil {
		return b.artifactLoader.Load(ctx, instance.Namespace, instance.Artifact)
	}
	path, err := b.Download(ctx, instance)
	return path, "", err
}

func (b *BundleApplier) Apply(ctx context.Context, instance install.Instance) (*install.InstanceStatus, error) {
	into, artifactDigest, err := b.resolveLocation(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	instance.Location = into
	if apply, ok := b.appliers[instance.Kind]; ok {
		status, err := apply.Apply(ctx, instance)
		if err == nil && status != nil {
			status.ArtifactDigest = artifactDigest
		}
		return status, err
	}
	return nil, fmt.Errorf("unknown bundle kind: %s", instance.Kind)
}

func (b *BundleApplier) Remove(ctx context.Context, instance install.Instance) error {
	if apply, ok := b.appliers[instance.Kind]; ok {
		return apply.Remove(ctx, instance)
	}
	return fmt.Errorf("unknown bundle kind: %s", instance.Kind)
}
