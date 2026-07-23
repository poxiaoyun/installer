package helm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
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

	// Load chart once for both ApplyChart and dashboard injection
	loadedChart, err := loader.Load(instance.Location)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}

	helmPR := NewHelmPostRenderer(instance.PostRenderer, loadedChart)
	desiredState := desiredReleaseState(loadedChart, install.PostRendererIdentity(instance.PostRenderer))

	applyedRelease, err := ApplyChart(ctx, r.Config, instance.Name, instance.Namespace, loadedChart, instance.Values, options, helmPR, desiredState)
	if err != nil {
		return nil, err
	}
	if applyedRelease.Info.Status != release.StatusDeployed {
		return nil, fmt.Errorf("apply not finished:%s", applyedRelease.Info.Description)
	}
	return &install.InstanceStatus{
		Note:              applyedRelease.Info.Notes,
		Namespace:         applyedRelease.Namespace,
		CreationTimestamp: applyedRelease.Info.FirstDeployed.Time,
		UpgradeTimestamp:  applyedRelease.Info.LastDeployed.Time,
		Values:            applyedRelease.Config,
		Version:           applyedRelease.Chart.Metadata.Version,
		AppVersion:        applyedRelease.Chart.Metadata.AppVersion,
		Resources:         ParseResourceReferences([]byte(applyedRelease.Manifest)),
	}, nil
}

func desiredReleaseState(ch *chart.Chart, postRendererIdentity string) string {
	h := sha256.New224()
	writeChartState(h, ch)
	writeStateBytes(h, []byte(postRendererIdentity))
	return hex.EncodeToString(h.Sum(nil))
}

func writeChartState(h hash.Hash, ch *chart.Chart) {
	if ch == nil {
		writeStateBytes(h, nil)
		return
	}
	metadata, _ := json.Marshal(ch.Metadata)
	lock, _ := json.Marshal(ch.Lock)
	values, _ := json.Marshal(ch.Values)
	writeStateBytes(h, metadata)
	writeStateBytes(h, lock)
	writeStateBytes(h, values)
	writeStateBytes(h, ch.Schema)
	writeChartFiles(h, ch.Templates)
	writeChartFiles(h, ch.Files)

	dependencies := append([]*chart.Chart(nil), ch.Dependencies()...)
	sort.Slice(dependencies, func(i, j int) bool {
		return dependencies[i].ChartFullPath() < dependencies[j].ChartFullPath()
	})
	for _, dependency := range dependencies {
		writeChartState(h, dependency)
	}
}

func writeChartFiles(h hash.Hash, files []*chart.File) {
	files = append([]*chart.File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	for _, file := range files {
		writeStateBytes(h, []byte(file.Name))
		writeStateBytes(h, file.Data)
	}
}

func writeStateBytes(h hash.Hash, data []byte) {
	_, _ = fmt.Fprintf(h, "%d:", len(data))
	_, _ = h.Write(data)
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
