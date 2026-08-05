package controller_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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

		// Wait for installation to complete (not just phase to be set)
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

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

		// Wait for installation to complete (not just phase to be set)
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

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

		// Wait for installation to complete (not just phase to be set)
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

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

		// Wait for installation to complete (not just phase to be set)
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

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

var _ = Describe("Instance scale subresource", func() {
	It("scales the instance independently from the paused value", func() {
		instance := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "scale-test", Namespace: "default"},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "testdata/helm-test",
				URL:     "file://" + testhelmdir,
				Version: "v0.0.0",
			},
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		Expect(waitForPhase(ctx, instance, appsv1.PhaseInstalled)).To(Succeed())

		scale := &autoscalingv1.Scale{}
		Expect(k8sClient.SubResource("scale").Get(ctx, instance, scale)).To(Succeed())
		Expect(scale.Spec.Replicas).To(Equal(int32(1)))
		Expect(scale.Status.Replicas).To(Equal(int32(1)))
		Expect(scale.Status.Selector).To(Equal("app.kubernetes.io/instance=scale-test"))
		configMap := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "scale-test-cm"}, configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "1"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))

		scale.Spec.Replicas = 0
		Expect(k8sClient.SubResource("scale").Update(ctx, instance, client.WithSubResourceBody(scale))).To(Succeed())
		Eventually(func() int32 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
			return instance.Status.Replicas
		}, 30*time.Second, 500*time.Millisecond).Should(Equal(int32(0)))
		Expect(instance.Status.Phase).To(Equal(appsv1.PhaseInstalled))
		Expect(instance.Status.Replicas).To(Equal(int32(0)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "0"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))

		Expect(k8sClient.SubResource("scale").Get(ctx, instance, scale)).To(Succeed())
		scale.Spec.Replicas = 2
		Expect(k8sClient.SubResource("scale").Update(ctx, instance, client.WithSubResourceBody(scale))).To(Succeed())
		Eventually(func() int32 {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
			return instance.Status.Replicas
		}, 30*time.Second, 500*time.Millisecond).Should(Equal(int32(2)))
		Expect(instance.Status.Replicas).To(Equal(int32(2)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "2"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))

		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		Expect(waitAllRemoved(ctx)).To(Succeed())
	})
})

var _ = Describe("ObservedGeneration and Conditions tests", func() {
	It("should set observedGeneration after reconcile", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "obs-gen-test",
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

		// Wait for installation to complete
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

		// Verify observedGeneration is set
		Expect(plugin.Status.ObservedGeneration).To(Equal(plugin.Generation))
	})

	It("should set Ready condition when installed", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "condition-test",
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

		// Wait for installation to complete
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

		// Verify conditions are set
		readyCondition := meta.FindStatusCondition(plugin.Status.Conditions, appsv1.ConditionReady)
		Expect(readyCondition).NotTo(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
		Expect(readyCondition.Reason).To(Equal("Ready"))

		// Verify DependenciesReady condition
		depsCondition := meta.FindStatusCondition(plugin.Status.Conditions, appsv1.ConditionDependenciesReady)
		Expect(depsCondition).NotTo(BeNil())
		Expect(depsCondition.Status).To(Equal(metav1.ConditionTrue))
	})

	It("should transition through phases during installation", func() {
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "phase-transition-test",
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

		// Wait for phase to be set (should be Installing or Installed)
		err = waitPhaseSet(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		// Phase should eventually reach Installed
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should set Failed phase when dependency is not ready", func() {
		// Create a plugin with a non-existent dependency
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pending-dep-test",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "testdata/helm-test",
				URL:     "file://" + testhelmdir,
				Version: "v0.0.0",
				Dependencies: []corev1.ObjectReference{
					{
						Name:      "non-existent-dependency",
						Namespace: "default",
					},
				},
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		// Wait for phase to be set - should be Failed due to missing dependency
		err = waitPhaseSet(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		// Phase should be Failed
		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseFailed))

		// Verify DependenciesReady condition is false
		depsCondition := meta.FindStatusCondition(plugin.Status.Conditions, appsv1.ConditionDependenciesReady)
		Expect(depsCondition).NotTo(BeNil())
		Expect(depsCondition.Status).To(Equal(metav1.ConditionFalse))
	})

	It("should set Message when installation fails and clear on success", func() {
		// Create a plugin with invalid URL to trigger an error
		plugin := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "last-error-test",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "nonexistent-path",
				URL:     "file:///nonexistent/path",
				Version: "v0.0.0",
			},
		}
		err := k8sClient.Create(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		// Wait for phase to reach Failed (waitForPhase stops on Failed)
		err = waitForPhase(ctx, plugin, appsv1.PhaseFailed)
		Expect(err).NotTo(HaveOccurred())

		// Phase should be Failed
		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseFailed))

		// Verify Message contains error
		Expect(plugin.Status.Message).NotTo(BeEmpty())

		// Now fix the plugin by updating to valid path
		plugin.Spec.URL = "file://" + testhelmdir
		plugin.Spec.Path = "testdata/helm-test"
		err = k8sClient.Update(ctx, plugin)
		Expect(err).NotTo(HaveOccurred())

		// Wait for installation to complete
		err = waitForPhase(ctx, plugin, appsv1.PhaseInstalled)
		Expect(err).NotTo(HaveOccurred())

		// Verify Message is cleared on success
		Expect(plugin.Status.Phase).To(Equal(appsv1.PhaseInstalled))
		Expect(plugin.Status.Message).To(BeEmpty())
	})

	It("cleanup test instances", func() {
		instances := []string{"obs-gen-test", "condition-test", "phase-transition-test", "pending-dep-test", "last-error-test"}
		for _, name := range instances {
			plugin := &appsv1.Instance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
			}
			_ = k8sClient.Delete(ctx, plugin)
		}
		err := waitAllRemoved(ctx)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("Phase status tests", func() {
	It("should verify all phase constants are valid", func() {
		// Verify all phase constants exist and have expected values
		Expect(string(appsv1.PhaseInstalled)).To(Equal("Installed"))
		Expect(string(appsv1.PhaseFailed)).To(Equal("Failed"))
	})

	It("should verify all condition type constants are valid", func() {
		// Verify all condition type constants exist
		Expect(appsv1.ConditionReady).To(Equal("Ready"))
		Expect(appsv1.ConditionDependenciesReady).To(Equal("DependenciesReady"))
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

func waitForPhase(ctx context.Context, bundle *appsv1.Instance, phase appsv1.Phase) error {
	return wait.PollUntilContextCancel(ctx, time.Second, false, func(ctx context.Context) (done bool, err error) {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), bundle); err != nil {
			return false, err
		}
		if bundle.Status.Phase == phase {
			return true, nil
		}
		// If failed, stop waiting
		if bundle.Status.Phase == appsv1.PhaseFailed {
			return true, nil
		}
		return false, nil
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
