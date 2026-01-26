package helm

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/client-go/rest"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/utils"
)

type Apply struct {
	Config *rest.Config
}

var _ install.Installer = &Apply{}

func New(config *rest.Config) *Apply {
	return &Apply{Config: config}
}

func (r *Apply) Template(ctx context.Context, instance install.Instance) ([]byte, error) {
	return TemplateChart(ctx, instance.Name, instance.Namespace, instance.Location, nil)
}

func (r *Apply) Apply(ctx context.Context, instance install.Instance) (*install.InstanceStatus, error) {
	log := logr.FromContextOrDiscard(ctx)

	log.Info("applying chart", "path", instance.Location)

	options, err := ParseOptions(instance.Options)
	if err != nil {
		return nil, fmt.Errorf("parse options: %w", err)
	}

	applyedRelease, err := ApplyChart(ctx, r.Config, instance.Name, instance.Namespace, instance.Location, instance.Values, options)
	if err != nil {
		return nil, err
	}
	if applyedRelease.Info.Status != release.StatusDeployed {
		return nil, fmt.Errorf("apply not finished:%s", applyedRelease.Info.Description)
	}
	result := &install.InstanceStatus{
		Note:              applyedRelease.Info.Notes,
		Namespace:         applyedRelease.Namespace,
		CreationTimestamp: applyedRelease.Info.FirstDeployed.Time,
		UpgradeTimestamp:  applyedRelease.Info.LastDeployed.Time,
		Values:            applyedRelease.Config,
		Version:           applyedRelease.Chart.Metadata.Version,
		AppVersion:        applyedRelease.Chart.Metadata.AppVersion,
		Resources:         ParseResourceReferences([]byte(applyedRelease.Manifest)),
	}
	return result, nil
}

func ParseOptions(options []install.Option) (Options, error) {
	option := Options{}
	for _, opt := range options {
		switch opt.Name {
		case "timeout":
			dur, err := time.ParseDuration(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse timeout: %w", err)
			}
			option.Timeout = dur
		case "maxHistory":
			i, err := strconv.Atoi(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse maxHistory: %w", err)
			}
			option.MaxHistory = i
		case "disableHooks":
			b, err := strconv.ParseBool(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse disableHooks: %w", err)
			}
			option.DisableHooks = b
		case "wait":
			b, err := strconv.ParseBool(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse wait: %w", err)
			}
			option.Wait = b
		case "waitForJobs":
			b, err := strconv.ParseBool(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse waitForJobs: %w", err)
			}
			option.WaitForJobs = b
		case "subNotes":
			b, err := strconv.ParseBool(opt.Value)
			if err != nil {
				return option, fmt.Errorf("parse subNotes: %w", err)
			}
			option.SubNotes = b
		default:
			return option, fmt.Errorf("unknown option: %s", opt.Name)
		}
	}
	return option, nil
}

func ParseResourceReferences(resources []byte) []appsv1.ManagedResource {
	ress, _ := utils.SplitYAML(resources)
	managedResources := make([]appsv1.ManagedResource, len(ress))
	for i, res := range ress {
		managedResources[i] = appsv1.GetReference(res)
	}
	return managedResources
}

type RemoveOptions struct {
	DryRun bool
}

func (r *Apply) Remove(ctx context.Context, instance install.Instance) error {
	log := logr.FromContextOrDiscard(ctx)

	options, err := ParseOptions(instance.Options)
	if err != nil {
		return err
	}
	// uninstall
	removedRelease, err := RemoveChart(ctx, r.Config, instance.Name, instance.Namespace, options)
	if err != nil {
		return err
	}
	log.Info("removed")
	_ = removedRelease
	return nil
}
