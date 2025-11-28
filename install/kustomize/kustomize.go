package kustomize

import (
	"context"

	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"xiaoshiai.cn/installer/install"
)

func KustomizeBuildFunc(ctx context.Context, instance install.Instance) ([]byte, error) {
	return KustomizeBuild(ctx, instance.Location)
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
