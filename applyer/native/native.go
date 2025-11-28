package native

import (
	"context"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/utils"
)

type TemplateFun func(ctx context.Context, instance *appsv1.Instance, into string) ([]byte, error)

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

func (p *Apply) Template(ctx context.Context, instance *appsv1.Instance, into string) ([]byte, error) {
	return p.TemplateFun(ctx, instance, into)
}

func (p *Apply) Apply(ctx context.Context, instance *appsv1.Instance, into string) error {
	log := logr.FromContextOrDiscard(ctx)

	rendered, err := p.Template(ctx, instance, into)
	if err != nil {
		return err
	}
	resources, err := utils.SplitYAML(rendered)
	if err != nil {
		return err
	}

	ns := instance.Namespace
	diffresult := DiffWithDefaultNamespace(p.Cli.Client, ns, instance.Status.Resources, resources)
	if instance.Status.Phase == appsv1.PhaseInstalled &&
		instance.Spec.Version == instance.Status.Version &&
		utils.EqualMapValues(instance.Status.Values.Object, instance.Spec.Values.Object) &&
		len(diffresult.Creats) == 0 &&
		len(diffresult.Removes) == 0 {
		log.Info("all resources are already applied")
		return nil
	}
	managedResources, err := p.Cli.SyncDiff(ctx, diffresult, NewDefaultSyncOptions())
	if err != nil {
		return err
	}
	instance.Status.Resources = managedResources
	instance.Status.Values = appsv1.Values{Object: instance.Spec.Values.Object}
	instance.Status.Phase = appsv1.PhaseInstalled
	instance.Status.Version = instance.Spec.Version
	instance.Status.Namespace = ns
	now := metav1.Now()
	instance.Status.UpgradeTimestamp = now
	if instance.Status.CreationTimestamp.IsZero() {
		instance.Status.CreationTimestamp = now
	}
	instance.Status.Message = ""
	return nil
}

func (p *Apply) Remove(ctx context.Context, instance *appsv1.Instance) error {
	ns := instance.Namespace
	if ns == "" {
		ns = instance.Namespace
	}
	managedResources, err := p.Cli.Sync(ctx, ns, instance.Status.Resources, nil, NewDefaultSyncOptions())
	if err != nil {
		return err
	}
	instance.Status.Resources = managedResources
	instance.Status.Phase = appsv1.PhaseDisabled
	instance.Status.Message = ""
	return nil
}
