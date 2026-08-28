package kustomize

import (
	"context"

	"sigs.k8s.io/kustomize/api/krusty"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
)

func KustomizeBuildFunc(ctx context.Context, instance install.Instance) ([]byte, error) {
	return KustomizeBuild(ctx, instance.Location)
}

func KustomizeBuild(ctx context.Context, location filesystem.Location) ([]byte, error) {
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	m, err := k.Run(newKustomizeFS(location.FS), location.Path)
	if err != nil {
		return nil, err
	}
	yml, err := m.AsYaml()
	if err != nil {
		return nil, err
	}
	return []byte(yml), nil
}
