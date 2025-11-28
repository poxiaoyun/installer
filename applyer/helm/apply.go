package helm

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/utils"
)

type Apply struct {
	Config *rest.Config
}

func New(config *rest.Config) *Apply {
	return &Apply{Config: config}
}

func (r *Apply) Template(ctx context.Context, instance *appsv1.Instance, dir string) ([]byte, error) {
	rls := r.getPreRelease(instance)
	return TemplateChart(ctx, rls.Name, rls.Namespace, dir, nil)
}

func (r *Apply) Apply(ctx context.Context, instance *appsv1.Instance, into string) error {
	rls := r.getPreRelease(instance)
	applyedRelease, err := ApplyChart(ctx, r.Config, rls.Name, rls.Namespace, into, rls.Config)
	if err != nil {
		return err
	}
	instance.Status.Resources = ParseResourceReferences([]byte(applyedRelease.Manifest))
	if applyedRelease.Info.Status != release.StatusDeployed {
		return fmt.Errorf("apply not finished:%s", applyedRelease.Info.Description)
	}
	instance.Status.Phase = appsv1.PhaseInstalled
	instance.Status.Message = applyedRelease.Info.Notes
	instance.Status.Namespace = applyedRelease.Namespace
	instance.Status.CreationTimestamp = convtime(applyedRelease.Info.FirstDeployed.Time)
	instance.Status.UpgradeTimestamp = convtime(applyedRelease.Info.LastDeployed.Time)
	instance.Status.Values = appsv1.Values{Object: applyedRelease.Config}
	instance.Status.Version = applyedRelease.Chart.Metadata.Version
	instance.Status.AppVersion = applyedRelease.Chart.Metadata.AppVersion
	return nil
}

func ParseResourceReferences(resources []byte) []appsv1.ManagedResource {
	ress, _ := utils.SplitYAML(resources)
	managedResources := make([]appsv1.ManagedResource, len(ress))
	for i, res := range ress {
		managedResources[i] = appsv1.GetReference(res)
	}
	return managedResources
}

// https://github.com/golang/go/issues/19502
// metav1.Time and time.Time are not comparable directly
func convtime(t time.Time) metav1.Time {
	t, _ = time.Parse(time.RFC3339, t.Format(time.RFC3339))
	return metav1.Time{Time: t}
}

type RemoveOptions struct {
	DryRun bool
}

func (r *Apply) Remove(ctx context.Context, bundle *appsv1.Instance) error {
	log := logr.FromContextOrDiscard(ctx)
	if bundle.Status.Phase == appsv1.PhaseDisabled {
		log.Info("already removed or not installed")
		return nil
	}
	rls := r.getPreRelease(bundle)
	// uninstall
	removedRelease, err := RemoveChart(ctx, r.Config, rls.Name, rls.Namespace)
	if err != nil {
		return err
	}
	log.Info("removed")
	if removedRelease == nil {
		bundle.Status.Phase = appsv1.PhaseDisabled
		bundle.Status.Message = "plugin not install"
		return nil
	}
	bundle.Status.Phase = appsv1.PhaseDisabled
	bundle.Status.Message = removedRelease.Info.Description
	return nil
}

func (r Apply) getPreRelease(bundle *appsv1.Instance) *release.Release {
	return &release.Release{Name: bundle.Name, Namespace: bundle.Namespace, Config: bundle.Spec.Values.Object}
}
