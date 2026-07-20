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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install/download"
)

var _ = Describe("Chart Secret artifacts", func() {
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
					SecretRef: appsv1.ArtifactSecretRef{Name: secretV1.Name, Key: download.ChartSecretKey},
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
			SecretRef: appsv1.ArtifactSecretRef{Name: invalidSecret.Name, Key: download.ChartSecretKey},
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
			SecretRef: appsv1.ArtifactSecretRef{Name: secretV2.Name, Key: download.ChartSecretKey},
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
		instance.Spec.Extensions = []appsv1.Extension{{Name: "labels", Kind: "Labels"}}
		Expect(k8sClient.Update(ctx, instance)).To(Succeed())
		eventuallyInstalledArtifact(instance, digestV2, "0.1.0")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		Expect(cm.Labels["app.kubernetes.io/instance"]).To(Equal(instance.Name))
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
			Annotations: map[string]string{download.ContentDigestAnnotation: digest},
		},
		Immutable: &immutable,
		Type:      download.ChartSecretType,
		Data:      map[string][]byte{download.ChartSecretKey: archive},
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
