package controller_test

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
	// temporary cache dir created for tests
	testCacheDir  string
	controllerRun chan error
)

// decide whether to allow online tests
var enableOnline, _ = strconv.ParseBool(os.Getenv("ENABLE_ONLINE_TESTS"))

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

// global BeforeEach: skip specs labeled "online" when online tests are disabled
var _ = BeforeEach(func() {
	if enableOnline {
		return
	}
	rep := CurrentSpecReport()
	for _, l := range rep.Labels() {
		if l == "online" {
			Skip("online tests disabled; set ENABLE_ONLINE_TESTS=1 to enable")
		}
	}
})

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

	// create a dedicated temp dir for cache to avoid interference between tests
	tmpCacheDir, err := os.MkdirTemp("", "installer-test-cache-")
	Expect(err).NotTo(HaveOccurred())
	testCacheDir = tmpCacheDir

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"envtest": {
				Server:                   cfg.Host,
				CertificateAuthorityData: cfg.CAData,
				InsecureSkipTLSVerify:    cfg.Insecure,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"envtest": {
				ClientCertificateData: cfg.CertData,
				ClientKeyData:         cfg.KeyData,
				Token:                 cfg.BearerToken,
				Username:              cfg.Username,
				Password:              cfg.Password,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"envtest": {Cluster: "envtest", AuthInfo: "envtest"},
		},
		CurrentContext: "envtest",
	}
	kubeconfigData, err := clientcmd.Write(kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	kubeconfigPath := filepath.Join(testCacheDir, "kubeconfig")
	Expect(os.WriteFile(kubeconfigPath, kubeconfigData, 0o600)).To(Succeed())
	Expect(os.Setenv("KUBECONFIG", kubeconfigPath)).To(Succeed())

	controllerOptions := controller.NewDefaultOptions()
	controllerOptions.CacheDir = testCacheDir
	controllerOptions.MetricsAddr = "0"
	controllerOptions.ProbeAddr = "0"

	controllerRun = make(chan error, 1)
	go func() {
		controllerRun <- controller.Run(ctx, controllerOptions)
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	if controllerRun != nil {
		select {
		case err := <-controllerRun:
			Expect(err).NotTo(HaveOccurred())
		case <-time.After(30 * time.Second):
			Fail("controller manager did not stop")
		}
	}
	if testEnv != nil && cfg != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
	// cleanup test cache dir if created
	if testCacheDir != "" {
		// limit safety: only remove temp dirs created under os.TempDir()
		if strings.HasPrefix(testCacheDir, os.TempDir()) {
			os.RemoveAll(testCacheDir)
		}
	}
})
