package controller_test

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment

	// use SIGTERM which is catchable; os.Kill cannot be trapped
	ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// absolute path to local testdata (initialized in BeforeSuite)
	testdatadir string
	testhelmdir string
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")

	// reuse downloaded envtest binaries across runs to avoid re-downloading
	binaryDir := filepath.Join(os.Getenv("HOME"), ".cache", "envtest")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{"../deploy/installer/crds"},
		CRDInstallOptions: envtest.CRDInstallOptions{
			CleanUpAfterUse: true,
		},
		BinaryAssetsDirectory: binaryDir,
		DownloadBinaryAssets:  true,
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = appsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = apiextensionsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// initialize absolute path to local testdata for file:// tests
	testdatadir, err = filepath.Abs(filepath.Join("..", "testdata", "kustomize-test"))
	Expect(err).NotTo(HaveOccurred())

	testhelmdir, err = filepath.Abs(filepath.Join("..", "testdata", "helm-test"))
	Expect(err).NotTo(HaveOccurred())

	// setup ctrl manager
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:           scheme.Scheme,
		LeaderElectionID: apps.GroupName,
	})
	Expect(err).NotTo(HaveOccurred())

	// register controller
	err = controller.Setup(ctx, mgr, controller.NewDefaultOptions())
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).ToNot(HaveOccurred())
	}()
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel() // stop mgr
	if testEnv != nil && cfg != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = Describe("Basic Plugin tests", func() {
	It("create remote git helm plugin", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "local-path-provisioner",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				URL:     "https://github.com/rancher/local-path-provisioner.git",
				Path:    "deploy/chart",
				Version: "v0.0.21", // tag or branch
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		waitPhaseSet(ctx, plugin)

		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseInstalled))
		Expect(plugin.Finalizers).To(Equal([]string{controller.FinalizerName}))
		Expect(plugin.Status.Version).To(Equal("0.0.21"))
	})

	// testdatadir is initialized in BeforeSuite
	It("creates a local helm plugin", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "testdata/helm-test",
				URL:     "file:///" + testhelmdir,
				Version: "v0.0.0",
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		waitPhaseSet(ctx, plugin)

		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseInstalled))
	})

	It("create a local kustomization plugin", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kustomize-test",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindKustomize,
				URL:     "file:///" + testdatadir,
				Version: "v0.0.0",
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		waitPhaseSet(ctx, plugin)

		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseInstalled))

		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "kustomize-test", Namespace: "default"}}
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)
		Expect(err).NotTo(HaveOccurred())
	})

	It("create a remote kustomize plugin", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "external-snapshotter",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindKustomize,
				URL:     "https://github.com/kubernetes-csi/external-snapshotter.git",
				Path:    "client/config/crd",
				Version: "v5.0.0",
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		waitPhaseSet(ctx, plugin)

		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseInstalled))

		crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "volumesnapshots.snapshot.storage.k8s.io"}}
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), crd)
		Expect(err).NotTo(HaveOccurred())
	})

	It("wait all plugins removed", func() {
		plugins := &appsv1.InstanceList{}
		err := k8sClient.List(ctx, plugins)
		Expect(err).NotTo(HaveOccurred())
		for _, plugin := range plugins.Items {
			_ = k8sClient.Delete(ctx, &plugin)
		}
		err = waitAllRemoved(ctx)
		Expect(err).NotTo(HaveOccurred())
	})
})

func waitPhaseSet(ctx context.Context, bundle *appsv1.Instance) error {
	return wait.PollUntilContextCancel(ctx, time.Second, false, func(ctx context.Context) (done bool, err error) {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), bundle); err != nil {
			return false, err
		}
		if bundle.Status.Phase == "" {
			return false, nil
		}
		return true, nil
	})
}

func waitAllRemoved(ctx context.Context) error {
	return wait.PollUntilContextCancel(ctx, time.Second, false, func(ctx context.Context) (done bool, err error) {
		bundles := &appsv1.InstanceList{}
		if err := k8sClient.List(ctx, bundles, client.InNamespace("default")); err != nil {
			return false, err
		}
		if len(bundles.Items) == 0 {
			return true, nil
		}
		return false, nil
	})
}
