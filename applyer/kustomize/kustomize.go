package kustomize

import (
	"context"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func KustomizeBuildFunc(ctx context.Context, instance *appsv1.Instance, dir string) ([]byte, error) {
	return KustomizeBuild(ctx, dir)
}

func KustomizeBuild(ctx context.Context, dir string) ([]byte, error) {
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	m, err := k.Run(filesys.MakeFsOnDisk(), dir)
	if err != nil {
		return nil, err
	}
	yml, err := m.AsYaml()
	if err != nil {
		return nil, err
	}
	return []byte(yml), nil
}
