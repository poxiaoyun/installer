package controller_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sappsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/controller"
	"xiaoshiai.cn/installer/install"
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
		Expect(plugin.Finalizers).To(Equal([]string{apps.FinalizerName}))
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
		Expect(cm.Annotations).To(HaveKeyWithValue(apps.AnnotationInstanceName, plugin.Name))
		Expect(cm.Annotations).To(HaveKeyWithValue(apps.AnnotationInstanceNamespace, plugin.Namespace))
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

var _ = Describe("RawManifest extension", func() {
	It("adds ordered manifests and removes them with the extension", func() {
		instance := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "raw-manifest-test", Namespace: "default"},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "testdata/helm-test",
				URL:     "file://" + testhelmdir,
				Version: "v0.0.0",
				Values: appsv1.Values{Object: map[string]any{
					"global": map[string]any{"paused": true},
				}},
				Extensions: []appsv1.Extension{{
					Name: "workload",
					Kind: apps.ExtensionKindRawManifest,
					Params: map[string]string{apps.ExtensionParamManifest: `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: raw-manifest-workload
spec:
  replicas: 3
  selector:
    matchLabels:
      app: raw-manifest-workload
  template:
    metadata:
      labels:
        app: raw-manifest-workload
    spec:
      containers:
        - name: workload
          image: example.invalid/workload
`},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		Expect(waitForPhase(ctx, instance, appsv1.PhasePaused)).To(Succeed())

		deployment := &k8sappsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "raw-manifest-workload"}, deployment)).To(Succeed())
		Expect(deployment.Spec.Replicas).NotTo(BeNil())
		Expect(*deployment.Spec.Replicas).To(Equal(int32(0)))
		Expect(deployment.Annotations).To(HaveKeyWithValue("meta.helm.sh/release-name", instance.Name))
		Expect(deployment.Annotations).To(HaveKeyWithValue("meta.helm.sh/release-namespace", instance.Namespace))
		Expect(deployment.Spec.Template.Labels).NotTo(HaveKey(apps.LabelInstance))

		instance.Spec.Extensions = nil
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		Eventually(func() bool {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance); err != nil {
				return false
			}
			return instance.Status.ObservedGeneration == instance.Generation && len(instance.Status.Extensions) == 0
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "raw-manifest-workload"}, deployment)
			return apierrors.IsNotFound(err)
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
			return apierrors.IsNotFound(err)
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
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
		Expect(scale.Status.Replicas).To(Equal(int32(0)))
		Expect(scale.Status.Selector).To(Equal("app.kubernetes.io/instance=scale-test"))
		configMap := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "scale-test-cm"}, configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "1"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))
		firstPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "scale-test-worker-1",
				Namespace: "default",
				Labels:    map[string]string{apps.LabelInstance: "scale-test"},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: "example.invalid/worker"}}},
		}
		Expect(k8sClient.Create(ctx, firstPod)).To(Succeed())
		Eventually(func() int32 {
			_ = k8sClient.SubResource("scale").Get(ctx, instance, scale)
			return scale.Status.Replicas
		}, 30*time.Second, 500*time.Millisecond).Should(Equal(int32(1)))

		scale.Spec.Replicas = 0
		Expect(k8sClient.SubResource("scale").Update(ctx, instance, client.WithSubResourceBody(scale))).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
			return configMap.Data["global-replicas"]
		}, 30*time.Second, 500*time.Millisecond).Should(Equal("0"))
		Expect(k8sClient.Delete(ctx, firstPod)).To(Succeed())
		Eventually(func() int32 {
			_ = k8sClient.SubResource("scale").Get(ctx, instance, scale)
			return scale.Status.Replicas
		}, 30*time.Second, 500*time.Millisecond).Should(Equal(int32(0)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.Phase).To(Equal(appsv1.PhaseInstalled))
		Expect(instance.Status.Replicas).To(Equal(int32(0)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "0"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))

		Expect(k8sClient.SubResource("scale").Get(ctx, instance, scale)).To(Succeed())
		scale.Spec.Replicas = 2
		Expect(k8sClient.SubResource("scale").Update(ctx, instance, client.WithSubResourceBody(scale))).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)
			return configMap.Data["global-replicas"]
		}, 30*time.Second, 500*time.Millisecond).Should(Equal("2"))
		secondPod := firstPod.DeepCopy()
		secondPod.ResourceVersion = ""
		secondPod.Name = "scale-test-worker-2"
		thirdPod := firstPod.DeepCopy()
		thirdPod.ResourceVersion = ""
		thirdPod.Name = "scale-test-worker-3"
		Expect(k8sClient.Create(ctx, secondPod)).To(Succeed())
		Expect(k8sClient.Create(ctx, thirdPod)).To(Succeed())
		Eventually(func() int32 {
			_ = k8sClient.SubResource("scale").Get(ctx, instance, scale)
			return scale.Status.Replicas
		}, 30*time.Second, 500*time.Millisecond).Should(Equal(int32(2)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		Expect(instance.Status.Replicas).To(Equal(int32(2)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		Expect(configMap.Data).To(HaveKeyWithValue("global-replicas", "2"))
		Expect(configMap.Data).To(HaveKeyWithValue("global-paused", "false"))

		Expect(k8sClient.Delete(ctx, secondPod)).To(Succeed())
		Expect(k8sClient.Delete(ctx, thirdPod)).To(Succeed())
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
		Expect(readyCondition.Reason).To(Equal(controller.ReasonReady))

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

	It("resumes a waiting Instance after a dependency status-only Ready update", func() {
		dependency := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "status-dependency",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:         appsv1.InstanceKindHelm,
				Path:         "testdata/helm-test",
				URL:          "file://" + testhelmdir,
				Version:      "v0.0.0",
				Dependencies: []corev1.ObjectReference{{Name: "status-blocker"}},
			},
		}
		Expect(k8sClient.Create(ctx, dependency)).To(Succeed())
		Eventually(func() appsv1.Phase {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dependency), dependency); err != nil {
				return ""
			}
			return dependency.Status.Phase
		}, 30*time.Second, 100*time.Millisecond).Should(Equal(appsv1.PhaseWaiting))

		dependent := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "status-dependent",
				Namespace: "default",
			},
			Spec: appsv1.InstanceSpec{
				Kind:    appsv1.InstanceKindHelm,
				Path:    "testdata/helm-test",
				URL:     "file://" + testhelmdir,
				Version: "v0.0.0",
				Dependencies: []corev1.ObjectReference{
					{
						Name:      dependency.Name,
						Namespace: "default",
					},
				},
			},
		}
		err := k8sClient.Create(ctx, dependent)
		Expect(err).NotTo(HaveOccurred())

		err = waitForPhase(ctx, dependent, appsv1.PhaseWaiting)
		Expect(err).NotTo(HaveOccurred())
		Expect(dependent.Status.Message).To(BeEmpty())

		// Verify DependenciesReady condition is false
		depsCondition := meta.FindStatusCondition(dependent.Status.Conditions, appsv1.ConditionDependenciesReady)
		Expect(depsCondition).NotTo(BeNil())
		Expect(depsCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(depsCondition.Reason).To(Equal(controller.ReasonDependencyNotReady))
		Expect(depsCondition.Message).To(Equal("dependency default/status-dependency is not ready"))

		Consistently(func() appsv1.Phase {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dependent), dependent); err != nil {
				return ""
			}
			return dependent.Status.Phase
		}, time.Second, 100*time.Millisecond).Should(Equal(appsv1.PhaseWaiting))

		// Change only the dependency observation after all creation events have
		// settled, so this status update is the sole event that can unblock the dependent.
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(dependency), dependency)).To(Succeed())
		dependency.Status.ObservedGeneration = dependency.Generation
		dependency.Status.Phase = appsv1.PhaseInstalled
		dependency.Status.Message = ""
		dependency.Status.Values = appsv1.Values{Object: map[string]any{
			"global": map[string]any{"replicas": float64(1)},
		}}
		dependency.Status.Conditions = nil
		meta.SetStatusCondition(&dependency.Status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionInstalled,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: dependency.Generation,
			Reason:             controller.ReasonInstalled,
			Message:            "Instance is installed and ready",
		})
		meta.SetStatusCondition(&dependency.Status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: dependency.Generation,
			Reason:             controller.ReasonReady,
			Message:            "Instance is ready",
		})
		Expect(k8sClient.Status().Update(ctx, dependency)).To(Succeed())

		Eventually(func() appsv1.Phase {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dependent), dependent); err != nil {
				return ""
			}
			return dependent.Status.Phase
		}, 10*time.Second, 100*time.Millisecond).Should(Equal(appsv1.PhaseInstalled))
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
		instances := []string{"obs-gen-test", "condition-test", "phase-transition-test", "status-dependent", "status-dependency", "last-error-test"}
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
		Expect(string(appsv1.PhaseWaiting)).To(Equal("Waiting"))
		Expect(string(appsv1.PhaseFailed)).To(Equal("Failed"))
	})

	It("should verify all condition type constants are valid", func() {
		// Verify all condition type constants exist
		Expect(appsv1.ConditionReady).To(Equal("Ready"))
		Expect(appsv1.ConditionDependenciesReady).To(Equal("DependenciesReady"))
	})
})

type pauseDuringResumeInstaller struct {
	client        client.Client
	appliedPaused bool
	firstApply    bool
}

func (i *pauseDuringResumeInstaller) Apply(ctx context.Context, instance install.Instance) (*install.InstanceStatus, error) {
	global := instance.Values["global"].(map[string]any)
	i.appliedPaused, _ = global["paused"].(bool)
	if !i.firstApply {
		i.firstApply = true
		latest := &appsv1.Instance{}
		key := client.ObjectKey{Namespace: instance.Namespace, Name: instance.Name}
		if err := i.client.Get(ctx, key, latest); err != nil {
			return nil, err
		}
		// The fake client does not advance generation, so simulate the API
		// server behavior for the immediate pause spec update.
		latest.Generation++
		latest.Spec.Values = appsv1.Values{Object: map[string]any{
			"global": map[string]any{"paused": true},
		}}
		if err := i.client.Update(ctx, latest); err != nil {
			return nil, err
		}
	}
	return &install.InstanceStatus{Values: instance.Values}, nil
}

func (i *pauseDuringResumeInstaller) Remove(context.Context, install.Instance) error { return nil }

func (i *pauseDuringResumeInstaller) Template(context.Context, install.Instance) ([]byte, error) {
	return nil, nil
}

func TestImmediatePauseConvergesAfterResumeStatusConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add installer scheme: %v", err)
	}
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "pause-during-resume",
			Namespace:  "default",
			Generation: 2,
			Finalizers: []string{apps.FinalizerName},
		},
		Spec: appsv1.InstanceSpec{
			Kind: appsv1.InstanceKindHelm,
			URL:  "oci://example.test/pause-during-resume",
			Values: appsv1.Values{Object: map[string]any{
				"global": map[string]any{"paused": false},
			}},
		},
		Status: appsv1.InstanceStatus{
			ObservedGeneration: 1,
			Values: appsv1.Values{Object: map[string]any{
				"global": map[string]any{"paused": true, "replicas": float64(1)},
			}},
			Conditions: []metav1.Condition{{
				Type:   appsv1.ConditionInstalled,
				Status: metav1.ConditionTrue,
			}},
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&appsv1.Instance{}).
		WithObjects(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
			instance,
		).
		Build()
	installer := &pauseDuringResumeInstaller{client: cli}
	reconciler := &controller.InstanceReconciler{
		Client:                       cli,
		Scheme:                       scheme,
		Applier:                      installer,
		AllowClusterScopedNamespaces: map[string]struct{}{},
	}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)}

	result, err := reconciler.Reconcile(t.Context(), request)
	if err != nil {
		t.Fatalf("resume Reconcile() error = %v, want generation change to requeue without error", err)
	}
	if !result.Requeue {
		t.Fatal("resume Reconcile() did not immediately requeue after generation changed")
	}
	if installer.appliedPaused {
		t.Fatal("resume operation did not apply paused=false before the conflict")
	}
	if _, err := reconciler.Reconcile(t.Context(), request); err != nil {
		t.Fatalf("pause Reconcile() error = %v", err)
	}
	if !installer.appliedPaused {
		t.Fatal("backend remained resumed after the pause reconciliation")
	}

	current := &appsv1.Instance{}
	if err := cli.Get(t.Context(), request.NamespacedName, current); err != nil {
		t.Fatalf("get reconciled instance: %v", err)
	}
	if current.Status.ObservedGeneration != current.Generation {
		t.Fatalf("observed generation = %d, want %d", current.Status.ObservedGeneration, current.Generation)
	}
	global := current.Status.Values.Object["global"].(map[string]any)
	if paused, _ := global["paused"].(bool); !paused {
		t.Fatalf("status paused = %v, want true", global["paused"])
	}
}

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
