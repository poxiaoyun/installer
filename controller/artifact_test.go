package controller_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
	"xiaoshiai.cn/installer/install/download"
	installerhelm "xiaoshiai.cn/installer/install/helm"
)

var _ = Describe("Chart Secret artifacts", func() {
	It("recovers a Helm install interrupted after creating its pending release", func() {
		const namespace = "default"
		const name = "pending-install-recovery"
		values := map[string]any{"value": "recovered"}
		loadedChart := recoveryChart("0.1.0", validRecoveryTemplate)
		helmConfig, err := installerhelm.NewHelmConfig(ctx, namespace, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(helmConfig.Releases.Create(&release.Release{
			Name:      name,
			Namespace: namespace,
			Chart:     loadedChart,
			Config:    values,
			Info: &release.Info{
				Status:      release.StatusPendingInstall,
				Description: "simulated interrupted install",
			},
			Version: 1,
		})).To(Succeed())

		result, err := installerhelm.ApplyChart(ctx, cfg, name, namespace, loadedChart, values, installerhelm.Options{}, nil, "pending-install-recovery")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Info.Status).To(Equal(release.StatusDeployed))
		DeferCleanup(func() {
			_, err := installerhelm.RemoveChart(ctx, cfg, name, namespace, installerhelm.Options{})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("recovers a Helm upgrade interrupted after creating its pending release", func() {
		const namespace = "default"
		instance := install.Instance{
			Name:      "pending-upgrade-recovery",
			Namespace: namespace,
			Kind:      appsv1.InstanceKindHelm,
			Location:  testhelmdir,
			Values: map[string]any{
				"global": map[string]any{"replicas": 1, "paused": false},
			},
		}
		applier := installerhelm.New(cfg)
		_, err := applier.Apply(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(applier.Remove(ctx, instance)).To(Succeed()) })

		helmConfig, err := installerhelm.NewHelmConfig(ctx, namespace, cfg)
		Expect(err).NotTo(HaveOccurred())
		current, err := action.NewGet(helmConfig).Run(instance.Name)
		Expect(err).NotTo(HaveOccurred())
		pending := *current
		pending.Version = current.Version + 1
		pending.Info = &release.Info{
			FirstDeployed: current.Info.FirstDeployed,
			LastDeployed:  current.Info.LastDeployed,
			Status:        release.StatusPendingUpgrade,
			Description:   "simulated interrupted upgrade",
		}
		Expect(helmConfig.Releases.Create(&pending)).To(Succeed())

		instance.Values = map[string]any{
			"global": map[string]any{"replicas": 2, "paused": false},
		}
		status, err := applier.Apply(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Values).To(Equal(instance.Values))
	})

	It("retries an initial install after rendering fails", func() {
		const namespace = "default"
		const name = "initial-render-recovery"
		values := map[string]any{"value": "recovered"}
		_, err := installerhelm.ApplyChart(
			ctx,
			cfg,
			name,
			namespace,
			recoveryChart("0.1.0", `{{ fail "simulated initial render failure" }}`),
			values,
			installerhelm.Options{},
			nil,
			"initial-render-failure",
		)
		Expect(err).To(MatchError(ContainSubstring("simulated initial render failure")))

		helmConfig, err := installerhelm.NewHelmConfig(ctx, namespace, cfg)
		Expect(err).NotTo(HaveOccurred())
		_, err = action.NewGet(helmConfig).Run(name)
		Expect(err).To(MatchError(ContainSubstring("release: not found")))

		result, err := installerhelm.ApplyChart(
			ctx,
			cfg,
			name,
			namespace,
			recoveryChart("0.1.0", validRecoveryTemplate),
			values,
			installerhelm.Options{},
			nil,
			"initial-render-recovered",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Version).To(Equal(1))
		DeferCleanup(func() {
			_, err := installerhelm.RemoveChart(ctx, cfg, name, namespace, installerhelm.Options{})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("retries an upgrade after rendering fails", func() {
		const namespace = "default"
		const name = "upgrade-render-recovery"
		initialValues := map[string]any{"value": "initial"}
		initial, err := installerhelm.ApplyChart(
			ctx,
			cfg,
			name,
			namespace,
			recoveryChart("0.1.0", validRecoveryTemplate),
			initialValues,
			installerhelm.Options{},
			nil,
			"upgrade-render-initial",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(initial.Version).To(Equal(1))
		DeferCleanup(func() {
			_, err := installerhelm.RemoveChart(ctx, cfg, name, namespace, installerhelm.Options{})
			Expect(err).NotTo(HaveOccurred())
		})

		updatedValues := map[string]any{"value": "updated"}
		_, err = installerhelm.ApplyChart(
			ctx,
			cfg,
			name,
			namespace,
			recoveryChart("0.2.0", `{{ fail "simulated upgrade render failure" }}`),
			updatedValues,
			installerhelm.Options{},
			nil,
			"upgrade-render-failure",
		)
		Expect(err).To(MatchError(ContainSubstring("simulated upgrade render failure")))

		helmConfig, err := installerhelm.NewHelmConfig(ctx, namespace, cfg)
		Expect(err).NotTo(HaveOccurred())
		current, err := action.NewGet(helmConfig).Run(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(current.Version).To(Equal(1))
		Expect(current.Info.Status).To(Equal(release.StatusDeployed))

		updated, err := installerhelm.ApplyChart(
			ctx,
			cfg,
			name,
			namespace,
			recoveryChart("0.2.0", validRecoveryTemplate),
			updatedValues,
			installerhelm.Options{},
			nil,
			"upgrade-render-recovered",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Version).To(Equal(2))
		Expect(updated.Config).To(Equal(updatedValues))
	})

	It("installs, upgrades, reports the digest, and uninstalls without deleting artifacts", func() {
		const namespace = "default"
		archiveV1 := controllerTestChartArchive("0.1.0", "one")
		digestV1 := controllerTestDigest(archiveV1)
		secretV1 := controllerChartSecret(namespace, "artifact-demo-0.1.0", archiveV1, digestV1)
		Expect(k8sClient.Create(ctx, secretV1)).To(Succeed())

		instance := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "artifact-demo", Namespace: namespace},
			Spec: appsv1.InstanceSpec{
				Kind: appsv1.InstanceKindHelm,
				Artifact: &appsv1.Artifact{
					SecretRef: appsv1.ArtifactSecretRef{Name: secretV1.Name, Key: apps.ChartSecretKey},
					Digest:    digestV1,
				},
			},
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		eventuallyInstalledArtifact(instance, digestV1, "0.1.0")

		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: namespace}}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(cm.Data["value"]).To(Equal("one"))
		releaseV1 := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.artifact-demo.v1", Namespace: namespace}}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(releaseV1), releaseV1)).To(Succeed())

		// Simulate Helm succeeding while the Instance status was not persisted.
		// The retry must recover from the Helm release without creating revision 2.
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		instance.Status.ObservedGeneration = 0
		meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:    appsv1.ConditionInstalled,
			Status:  metav1.ConditionFalse,
			Reason:  "SimulatedStatusWriteFailure",
			Message: "simulate a retry after Helm completed",
		})
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		eventuallyInstalledArtifact(instance, digestV1, "0.1.0")
		unexpectedReleaseV2 := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.artifact-demo.v2", Namespace: namespace}}
		Consistently(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(unexpectedReleaseV2), unexpectedReleaseV2)
			return apierrors.IsNotFound(err)
		}, 2*time.Second, 100*time.Millisecond).Should(BeTrue())

		invalidArchive := []byte("not a Helm chart")
		invalidDigest := controllerTestDigest(invalidArchive)
		invalidSecret := controllerChartSecret(namespace, "artifact-demo-invalid", invalidArchive, invalidDigest)
		Expect(k8sClient.Create(ctx, invalidSecret)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		instance.Spec.Artifact = &appsv1.Artifact{
			SecretRef: appsv1.ArtifactSecretRef{Name: invalidSecret.Name, Key: apps.ChartSecretKey},
			Digest:    invalidDigest,
		}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		Eventually(func() bool {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance); err != nil {
				return false
			}
			condition := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionInstalled)
			return instance.Status.Phase == appsv1.PhaseFailed &&
				instance.Status.Artifact != nil &&
				instance.Status.Artifact.Digest == digestV1 &&
				condition != nil && condition.Reason == download.ReasonArtifactLoadFailed
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())

		// Content changes must upgrade even when Chart.yaml version and Helm
		// values are unchanged.
		archiveV2 := controllerTestChartArchive("0.1.0", "two")
		digestV2 := controllerTestDigest(archiveV2)
		secretV2 := controllerChartSecret(namespace, "artifact-demo-0.2.0", archiveV2, digestV2)
		Expect(k8sClient.Create(ctx, secretV2)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		instance.Spec.Artifact = &appsv1.Artifact{
			SecretRef: appsv1.ArtifactSecretRef{Name: secretV2.Name, Key: apps.ChartSecretKey},
			Digest:    digestV2,
		}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		eventuallyInstalledArtifact(instance, digestV2, "0.1.0")

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(cm.Data["value"]).To(Equal("two"))
		releaseV2 := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.artifact-demo.v2", Namespace: namespace}}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(releaseV2), releaseV2)).To(Succeed())

		// A post-render-only change must not be hidden by matching Chart content
		// and values in the Helm-level idempotency check.
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
		instance.Spec.Extensions = []appsv1.Extension{{Name: "common-metadata", Kind: apps.ExtensionKindCommonMetadata}}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		eventuallyInstalledArtifact(instance, digestV2, "0.1.0")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(cm.Labels[apps.LabelInstance]).To(Equal(instance.Name))
		releaseV3 := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.artifact-demo.v3", Namespace: namespace}}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(releaseV3), releaseV3)).To(Succeed())

		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), &appsv1.Instance{})
			return apierrors.IsNotFound(err)
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
			return apierrors.IsNotFound(err)
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secretV1), &corev1.Secret{})).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secretV2), &corev1.Secret{})).To(Succeed())
		Expect(k8sClient.Delete(ctx, secretV1)).To(Succeed())
		Expect(k8sClient.Delete(ctx, secretV2)).To(Succeed())
		Expect(k8sClient.Delete(ctx, invalidSecret)).To(Succeed())
	})

	It("recovers when the referenced Secret is created later", func() {
		const namespace = "default"
		archive := controllerTestChartArchive("0.1.0", "late")
		digest := controllerTestDigest(archive)
		secret := controllerChartSecret(namespace, "artifact-late-0.1.0", archive, digest)
		secret.Annotations = nil
		secret.Data = map[string][]byte{"custom.bundle": archive}
		instance := &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "artifact-late", Namespace: namespace},
			Spec: appsv1.InstanceSpec{
				Kind: appsv1.InstanceKindHelm,
				Artifact: &appsv1.Artifact{
					SecretRef: appsv1.ArtifactSecretRef{Name: secret.Name, Key: "custom.bundle"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance)
			return instance.Status.Message
		}, 30*time.Second, 500*time.Millisecond).Should(ContainSubstring("get chart Secret"))

		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		eventuallyInstalledArtifact(instance, digest, "0.1.0")
		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), &appsv1.Instance{})
			return apierrors.IsNotFound(err)
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
	})
})

const validRecoveryTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  value: {{ .Values.value | quote }}
`

func recoveryChart(version, manifest string) *chart.Chart {
	return &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "recovery-test", Version: version},
		Values:   map[string]any{"value": "default"},
		Templates: []*chart.File{{
			Name: "templates/configmap.yaml",
			Data: []byte(manifest),
		}},
	}
}

func eventuallyInstalledArtifact(instance *appsv1.Instance, digest, version string) {
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), instance); err != nil {
			return false
		}
		return instance.Status.Phase == appsv1.PhaseInstalled &&
			instance.Status.ObservedGeneration == instance.Generation &&
			instance.Status.Artifact != nil &&
			instance.Status.Artifact.Digest == digest &&
			instance.Status.Version == version
	}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
}

func controllerChartSecret(namespace, name string, archive []byte, digest string) *corev1.Secret {
	immutable := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{apps.ContentDigestAnnotation: digest},
		},
		Immutable: &immutable,
		Type:      apps.ChartSecretType,
		Data:      map[string][]byte{apps.ChartSecretKey: archive},
	}
}

func controllerTestDigest(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func controllerTestChartArchive(version, value string) []byte {
	files := []struct{ name, content string }{
		{name: "artifact-test/Chart.yaml", content: fmt.Sprintf("apiVersion: v2\nname: artifact-test\nversion: %s\n", version)},
		{name: "artifact-test/values.yaml", content: fmt.Sprintf("value: %s\n", value)},
		{name: "artifact-test/templates/configmap.yaml", content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: '{{ .Release.Name }}'\ndata:\n  value: '{{ .Values.value }}'\n"},
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, file := range files {
		_ = tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.content))})
		_, _ = tw.Write([]byte(file.content))
	}
	_ = tw.Close()
	_ = gz.Close()
	return out.Bytes()
}
