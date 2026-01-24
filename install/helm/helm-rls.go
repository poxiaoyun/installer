package helm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"xiaoshiai.cn/installer/utils"
)

type ReleaseManager struct {
	Config *rest.Config
}

func NewHelmConfig(ctx context.Context, namespace string, cfg *rest.Config) (*action.Configuration, error) {
	baselog := logr.FromContextOrDiscard(ctx)
	logfunc := func(format string, v ...any) {
		baselog.Info(fmt.Sprintf(format, v...))
	}

	cligetter := genericclioptions.NewConfigFlags(true)
	cligetter.WrapConfigFn = func(*rest.Config) *rest.Config {
		return cfg
	}

	config := &action.Configuration{}
	config.Init(cligetter, namespace, "", logfunc) // release storage namespace
	if kc, ok := config.KubeClient.(*kube.Client); ok {
		kc.Namespace = namespace // install to namespace
	}
	return config, nil
}

func TemplateChart(ctx context.Context, rlsname, namespace string, chartPath string, values map[string]any) ([]byte, error) {
	chart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}
	install := action.NewInstall(&action.Configuration{})
	install.ReleaseName, install.Namespace = rlsname, namespace
	install.DryRun, install.DisableHooks, install.ClientOnly = true, true, true
	rls, err := install.RunWithContext(ctx, chart, values)
	if err != nil {
		return nil, err
	}
	return []byte(rls.Manifest), nil
}

func ApplyChart(ctx context.Context, cfg *rest.Config, rlsname, namespace string, chartPath string, values map[string]any) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	log.Info("loading chart")
	loadedChart, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}
	if rlsname == "" {
		rlsname = loadedChart.Name()
	}
	helmcfg, err := NewHelmConfig(ctx, namespace, cfg)
	if err != nil {
		return nil, err
	}
	existRelease, err := action.NewGet(helmcfg).Run(rlsname)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return nil, err
		}
		// not install, install it now
		return installChart(ctx, helmcfg, loadedChart, rlsname, namespace, values)
	}

	// Handle pending/failed states that may block operations
	switch existRelease.Info.Status {
	case release.StatusPendingInstall, release.StatusPendingUpgrade, release.StatusPendingRollback:
		log.Info("release in pending state, attempting recovery", "status", existRelease.Info.Status)
		if err := recoverPendingRelease(ctx, helmcfg, rlsname, existRelease); err != nil {
			return nil, fmt.Errorf("failed to recover from pending state: %w", err)
		}
		// After recovery, proceed with fresh install
		return installChart(ctx, helmcfg, loadedChart, rlsname, namespace, values)

	case release.StatusUninstalling:
		log.Info("release is uninstalling, waiting for completion")
		return nil, fmt.Errorf("release is being uninstalled, please retry later")

	case release.StatusFailed:
		// Failed releases can be upgraded to recover
		log.Info("release in failed state, attempting upgrade to recover")
		// Fall through to upgrade logic
	}

	// check should upgrade
	if existRelease.Info.Status == release.StatusDeployed &&
		existRelease.Chart.Metadata.Version == loadedChart.Metadata.Version &&
		utils.EqualMapValues(existRelease.Config, values) {
		log.Info("already uptodate", "values", values)
		return existRelease, nil
	}
	log.Info("upgrading", "old", existRelease.Config, "new", values)
	return upgradeChart(ctx, helmcfg, loadedChart, rlsname, namespace, values)
}

// installChart performs a fresh helm install
func installChart(ctx context.Context, helmcfg *action.Configuration, loadedChart *chart.Chart, rlsname, namespace string, values map[string]interface{}) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	log.Info("installing", "values", values)

	install := action.NewInstall(helmcfg)
	install.ReleaseName = rlsname
	install.Namespace = namespace
	install.CreateNamespace = true
	install.Timeout = 10 * time.Minute
	return install.RunWithContext(ctx, loadedChart, values)
}

// upgradeChart performs a helm upgrade
func upgradeChart(ctx context.Context, helmcfg *action.Configuration, loadedChart *chart.Chart, rlsname, namespace string, values map[string]interface{}) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	log.Info("upgrading release")

	upgrade := action.NewUpgrade(helmcfg)
	upgrade.Namespace = namespace
	upgrade.ResetValues = true
	upgrade.MaxHistory = 5
	upgrade.Timeout = 10 * time.Minute

	return upgrade.RunWithContext(ctx, rlsname, loadedChart, values)
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

func equalMapValues(a, b map[string]any) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}

func RemoveChart(ctx context.Context, cfg *rest.Config, rlsname, namespace string) (*release.Release, error) {
	log := logr.FromContextOrDiscard(ctx).WithValues("name", rlsname, "namespace", namespace)
	helmcfg, err := NewHelmConfig(ctx, namespace, cfg)
	if err != nil {
		return nil, err
	}
	exist, err := action.NewGet(helmcfg).Run(rlsname)
	if err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			return nil, err
		}
		return nil, nil
	}

	uninstall := action.NewUninstall(helmcfg)
	uninstall.Timeout = 5 * time.Minute

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
	return uninstalledRelease.Release, nil
}
