package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller"
)

var _ = Describe("Basic Plugin tests", func() {
	It("create remote git helm plugin", Label("online"), func() {
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
				URL:     "file://" + testhelmdir,
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
				URL:     "file://" + testdatadir,
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

	It("create a remote kustomize plugin", Label("online"), func() {
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
