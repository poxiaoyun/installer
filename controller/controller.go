package controller

import (
	"context"
	"os"
	"path/filepath"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func GetScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	return scheme
}

type Options struct {
	MetricsAddr string `json:"metricsAddr,omitempty" description:"The address the metric endpoint binds to."`
	ProbeAddr   string `json:"probeAddr,omitempty" description:"The address the probe endpoint binds to."`

	LeaderElection   bool   `json:"leaderElection,omitempty" description:"Enable leader election for controller manager."`
	LeaderElectionID string `json:"leaderElectionID,omitempty" description:"The ID to use for leader election."`

	SkipNameValidation bool `json:"skipNameValidation,omitempty" description:"Skip validation of controller name."`

	CacheDir string `json:"cacheDir,omitempty" description:"The directory to cache downloaded bundle charts."`
}

func NewDefaultOptions() *Options {
	home, _ := os.UserHomeDir()
	return &Options{
		MetricsAddr:      ":9090",
		ProbeAddr:        ":8081",
		LeaderElection:   false,
		LeaderElectionID: "installer-leader-election",
		CacheDir:         filepath.Join(home, ".cache", "installer"),
	}
}

func Run(ctx context.Context, options *Options) error {
	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	setupLog := ctrl.Log.WithName("setup")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 GetScheme(),
		HealthProbeBindAddress: options.ProbeAddr,
		Metrics:                server.Options{BindAddress: options.MetricsAddr},
		LeaderElection:         options.LeaderElection,
		LeaderElectionID:       options.LeaderElectionID,
		Client: client.Options{
			Cache: &client.CacheOptions{
				// 对 Instance 资源禁用缓存，直接从 API Server 读取
				DisableFor: []client.Object{
					&appsv1.Instance{},
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		return err
	}

	// setup healthz
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	// setup controllers
	if err := Setup(ctx, mgr, options); err != nil {
		setupLog.Error(err, "unable to set up helm controller")
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}
	return nil
}
