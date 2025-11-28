package native

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/utils"
)

type TemplateFun func(ctx context.Context, instance install.Instance) ([]byte, error)

type Apply struct {
	TemplateFun TemplateFun
	Cli         *ClientApply
}

func New(cli client.Client, fun TemplateFun) *Apply {
	return &Apply{
		TemplateFun: fun,
		Cli:         &ClientApply{Client: cli},
	}
}

func (p *Apply) Template(ctx context.Context, instance install.Instance) ([]byte, error) {
	return p.TemplateFun(ctx, instance)
}

func (p *Apply) Apply(ctx context.Context, instance install.Instance) (*install.InstanceStatus, error) {
	log := logr.FromContextOrDiscard(ctx)

	rendered, err := p.Template(ctx, instance)
	if err != nil {
		return nil, err
	}
	resources, err := utils.SplitYAML(rendered)
	if err != nil {
		return nil, err
	}

	ns := instance.Namespace
	diffresult := DiffWithDefaultNamespace(p.Cli.Client, ns, instance.Resources, resources)
	if len(diffresult.Creats) == 0 &&
		len(diffresult.Removes) == 0 {
		log.Info("all resources are already applied")
		return &install.InstanceStatus{
			Resources:         instance.Resources,
			Values:            instance.Values,
			Version:           instance.Version,
			Namespace:         ns,
			CreationTimestamp: instance.CreationTimestamp,
			UpgradeTimestamp:  instance.UpgradeTimestamp,
		}, nil
	}
	managedResources, err := p.Cli.SyncDiff(ctx, diffresult, NewDefaultSyncOptions())
	if err != nil {
		return nil, err
	}
	return &install.InstanceStatus{
		Resources:         managedResources,
		Values:            instance.Values,
		Version:           instance.Version,
		Namespace:         ns,
		CreationTimestamp: instance.CreationTimestamp,
		UpgradeTimestamp:  time.Now(),
	}, nil
}

func (p *Apply) Remove(ctx context.Context, instance install.Instance) error {
	ns := instance.Namespace
	if ns == "" {
		ns = instance.Namespace
	}
	managedResources, err := p.Cli.Sync(ctx, ns, instance.Resources, nil, NewDefaultSyncOptions())
	if err != nil {
		return err
	}
	_ = managedResources
	return nil
}
