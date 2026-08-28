package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v4/pkg/action"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube"
	postrender "helm.sh/helm/v4/pkg/postrenderer"
	releaseapi "helm.sh/helm/v4/pkg/release"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/filesystem"
)

type Options struct {
	Timeout      time.Duration
	MaxHistory   int
	DisableHooks bool
	Wait         bool
	WaitForJobs  bool
	SubNotes     bool
}

const (
	DefaultTimeout  = 10 * time.Minute
	MaxHistoryLimit = 3
)

type ReleaseManager struct {
	Config *rest.Config
}

func NewHelmConfig(ctx context.Context, namespace string, cfg *rest.Config) (*action.Configuration, error) {
	operationConfig := rest.CopyConfig(cfg)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if operationConfig.Timeout == 0 || remaining < operationConfig.Timeout {
			operationConfig.Timeout = remaining
		}
	}

	cligetter := genericclioptions.NewConfigFlags(true)
	cligetter.WrapConfigFn = func(*rest.Config) *rest.Config {
		return operationConfig
	}

	config := action.NewConfiguration()
	if err := config.Init(cligetter, namespace, ""); err != nil { // release storage namespace
		return nil, err
	}
	if kc, ok := config.KubeClient.(*kube.Client); ok {
		kc.Namespace = namespace // install to namespace
	}
	config.KubeClient = newLifecycleKubeClient(config.KubeClient)
	return config, nil
}

func TemplateChart(ctx context.Context, rlsname, namespace string, location filesystem.Location, values map[string]any) ([]byte, error) {
	chart, err := LoadChart(location)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}
	return templateChart(ctx, rlsname, namespace, chart, values)
}

func templateChart(ctx context.Context, rlsname, namespace string, chart *chart.Chart, values map[string]any) ([]byte, error) {
	install := action.NewInstall(action.NewConfiguration())
	install.ReleaseName, install.Namespace = rlsname, namespace
	install.DryRunStrategy, install.DisableHooks = action.DryRunClient, true
	rlsValue, err := install.RunWithContext(ctx, chart, values)
	if err != nil {
		return nil, err
	}
	rls, err := asV1Release(rlsValue)
	if err != nil {
		return nil, err
	}
	return []byte(rls.Manifest), nil
}

func ApplyChart(ctx context.Context, cfg *rest.Config, rlsname, namespace string, loadedChart *chart.Chart, values map[string]any, options Options, pr postrender.PostRenderer) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	if rlsname == "" {
		rlsname = loadedChart.Name()
	}
	helmcfg, err := NewHelmConfig(ctx, namespace, cfg)
	if err != nil {
		return nil, err
	}
	if client, ok := helmcfg.KubeClient.(*lifecycleKubeClient); ok {
		client.timeout = Or(options.Timeout, DefaultTimeout)
	}
	pr = newLifecyclePostRenderer(pr)
	existValue, err := action.NewGet(helmcfg).Run(rlsname)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return nil, err
		}
		// not install, install it now
		return installChart(ctx, helmcfg, loadedChart, rlsname, namespace, values, options, pr)
	}
	existRelease, err := asV1Release(existValue)
	if err != nil {
		return nil, err
	}

	// Remove the incomplete revision left when a Helm process was interrupted.
	// A pending install has no earlier release and starts again, while an
	// interrupted upgrade or rollback resumes from the preceding revision.
	if existRelease.Info.Status.IsPending() {
		log.Info("release in pending state, attempting recovery", "status", existRelease.Info.Status)
		if err := recoverPendingRelease(ctx, helmcfg, rlsname, existRelease); err != nil {
			return nil, fmt.Errorf("failed to recover from pending state: %w", err)
		}
		existValue, err = action.NewGet(helmcfg).Run(rlsname)
		if err != nil {
			if !errors.Is(err, driver.ErrReleaseNotFound) {
				return nil, err
			}
			return installChart(ctx, helmcfg, loadedChart, rlsname, namespace, values, options, pr)
		}
		existRelease, err = asV1Release(existValue)
		if err != nil {
			return nil, err
		}
	}

	// Handle states that affect the next operation.
	switch existRelease.Info.Status {
	case releasecommon.StatusUninstalling:
		log.Info("release is uninstalling, waiting for completion")
		return nil, fmt.Errorf("release is being uninstalled, please retry later")

	case releasecommon.StatusFailed:
		// Failed releases can be upgraded to recover
		log.Info("release in failed state, attempting upgrade to recover")
		// Fall through to upgrade logic
	}

	log.Info("upgrading", "old", existRelease.Config, "new", values)
	return upgradeChart(ctx, helmcfg, loadedChart, rlsname, namespace, values, options, pr)
}

// installChart performs a fresh helm install
func installChart(ctx context.Context, helmcfg *action.Configuration, loadedChart *chart.Chart, rlsname, namespace string, values map[string]interface{}, options Options, pr postrender.PostRenderer) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	log.Info("installing")

	install := action.NewInstall(helmcfg)
	install.ReleaseName = rlsname
	install.Namespace = namespace
	install.CreateNamespace = true
	install.Timeout = Or(options.Timeout, DefaultTimeout)
	install.DisableHooks = options.DisableHooks
	install.WaitStrategy = helm4WaitStrategy(options.Wait)
	install.WaitForJobs = options.WaitForJobs
	install.SubNotes = options.SubNotes
	install.PostRenderer = pr
	install.ServerSideApply = false
	releaseValue, err := install.RunWithContext(ctx, loadedChart, values)
	if err != nil {
		return nil, err
	}
	return asV1Release(releaseValue)
}

// upgradeChart performs a helm upgrade
func upgradeChart(ctx context.Context, helmcfg *action.Configuration, loadedChart *chart.Chart, rlsname, namespace string, values map[string]interface{}, options Options, pr postrender.PostRenderer) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	log.Info("upgrading release")

	upgrade := action.NewUpgrade(helmcfg)
	upgrade.Namespace = namespace
	upgrade.ResetValues = true
	upgrade.MaxHistory = Or(options.MaxHistory, MaxHistoryLimit)
	upgrade.Timeout = Or(options.Timeout, DefaultTimeout)
	upgrade.DisableHooks = options.DisableHooks
	upgrade.WaitStrategy = helm4WaitStrategy(options.Wait)
	upgrade.WaitForJobs = options.WaitForJobs
	upgrade.SubNotes = options.SubNotes
	upgrade.PostRenderer = pr
	upgrade.ServerSideApply = "false"
	releaseValue, err := upgrade.RunWithContext(ctx, rlsname, loadedChart, values)
	if err != nil {
		return nil, err
	}
	return asV1Release(releaseValue)
}

// recoverPendingRelease attempts to recover a release stuck in pending state
// by removing the pending release record from Helm storage without touching actual resources
func recoverPendingRelease(ctx context.Context, helmcfg *action.Configuration, rlsname string, existRelease *release.Release) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "status", existRelease.Info.Status)
	log.Info("removing pending release record from helm storage")

	// Only delete the pending release record from Helm storage
	// This does NOT delete/rollback any actual Kubernetes resources
	_, err := helmcfg.Releases.Delete(rlsname, existRelease.Version)
	if err != nil {
		return fmt.Errorf("failed to delete pending release record: %w", err)
	}

	log.Info("successfully removed pending release record")
	return nil
}

func RemoveChart(ctx context.Context, cfg *rest.Config, rlsname, namespace string, options Options) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	helmcfg, err := NewHelmConfig(ctx, namespace, cfg)
	if err != nil {
		return nil, err
	}
	existValue, err := action.NewGet(helmcfg).Run(rlsname)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return nil, err
		}
		return nil, nil
	}
	exist, err := asV1Release(existValue)
	if err != nil {
		return nil, err
	}

	uninstall := action.NewUninstall(helmcfg)
	uninstall.DisableHooks = options.DisableHooks
	uninstall.WaitStrategy = helm4WaitStrategy(options.Wait)
	uninstall.Timeout = Or(options.Timeout, DefaultTimeout)

	// For pending states, disable hooks to force cleanup
	if exist.Info.Status.IsPending() {
		log.Info("force uninstalling pending release", "status", exist.Info.Status)
		uninstall.DisableHooks = true
	}

	log.Info("uninstalling")
	uninstalledRelease, err := uninstall.Run(exist.Name)
	if err != nil {
		return nil, err
	}
	return asV1Release(uninstalledRelease.Release)
}

func helm4WaitStrategy(wait bool) kube.WaitStrategy {
	if wait {
		return kube.LegacyStrategy
	}
	return kube.HookOnlyStrategy
}

func asV1Release(value releaseapi.Releaser) (*release.Release, error) {
	switch typed := value.(type) {
	case *release.Release:
		return typed, nil
	case release.Release:
		return &typed, nil
	default:
		return nil, fmt.Errorf("unsupported Helm release type %T", value)
	}
}

func Or[T comparable](a, b T) T {
	var zero T
	if a == zero {
		return b
	}
	return a
}

// NewHelmPostRenderer adapts an install.PostRenderer to Helm's
// postrender.PostRenderer by binding the chart.
func NewHelmPostRenderer(renderer install.PostRenderer, ch *chart.Chart) postrender.PostRenderer {
	if renderer == nil {
		return nil
	}
	return &helmPostRendererAdapter{renderer: renderer, chart: ch}
}

type helmPostRendererAdapter struct {
	renderer install.PostRenderer
	chart    *chart.Chart
}

func (a *helmPostRendererAdapter) Run(in *bytes.Buffer) (*bytes.Buffer, error) {
	return a.renderer.Run(in, a.chart)
}
