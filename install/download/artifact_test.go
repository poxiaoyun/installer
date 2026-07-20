package download

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func TestArtifactLoaderLoad(t *testing.T) {
	archive := testChartArchive(t, "0.1.0", "value: one")
	digest := digestOf(archive)
	immutable := true
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "demo-0.1.0",
			Namespace:   "default",
			Annotations: map[string]string{ContentDigestAnnotation: digest},
		},
		Immutable: &immutable,
		Type:      ChartSecretType,
		Data:      map[string][]byte{ChartSecretKey: archive},
	}
	artifact := &appsv1.Artifact{
		SecretRef: appsv1.ArtifactSecretRef{Name: secret.Name, Key: ChartSecretKey},
		Digest:    digest,
	}
	loader := newTestArtifactLoader(t, secret)

	path, actualDigest, cleanup, err := loader.Load(context.Background(), "default", artifact)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if actualDigest != digest {
		t.Fatalf("Load() digest = %s, want %s", actualDigest, digest)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat temporary chart: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("temporary chart mode = %o, want 600", got)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temporary chart: %v", err)
	}
	if !bytes.Equal(got, archive) {
		t.Fatal("temporary chart content does not match Secret data")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary chart still exists after cleanup: %v", err)
	}
}

func TestArtifactLoaderAllowsCustomKeyAndOptionalDigests(t *testing.T) {
	archive := testChartArchive(t, "0.1.0", "value: one")
	immutable := true
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Immutable:  &immutable,
		Type:       ChartSecretType,
		Data:       map[string][]byte{"custom.bundle": archive},
	}
	artifact := &appsv1.Artifact{
		SecretRef: appsv1.ArtifactSecretRef{Name: secret.Name, Key: "custom.bundle"},
	}
	path, actualDigest, cleanup, err := newTestArtifactLoader(t, secret).Load(context.Background(), "default", artifact)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer cleanup()
	if path == "" {
		t.Fatal("Load() returned an empty path")
	}
	if actualDigest != digestOf(archive) {
		t.Fatalf("Load() digest = %s, want %s", actualDigest, digestOf(archive))
	}
}

func TestArtifactLoaderRejectsInvalidSources(t *testing.T) {
	archive := testChartArchive(t, "0.1.0", "value: one")
	digest := digestOf(archive)
	newSecret := func() *corev1.Secret {
		immutable := true
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "demo-0.1.0",
				Namespace:   "default",
				Annotations: map[string]string{ContentDigestAnnotation: digest},
			},
			Immutable: &immutable,
			Type:      ChartSecretType,
			Data:      map[string][]byte{ChartSecretKey: archive},
		}
	}
	newArtifact := func() *appsv1.Artifact {
		return &appsv1.Artifact{
			SecretRef: appsv1.ArtifactSecretRef{Name: "demo-0.1.0", Key: ChartSecretKey},
			Digest:    digest,
		}
	}

	tests := []struct {
		name       string
		mutate     func(*appsv1.Artifact, *corev1.Secret)
		withSecret bool
		wantReason metav1.StatusReason
	}{
		{name: "missing Secret", withSecret: false, wantReason: ReasonArtifactSecretNotFound},
		{name: "wrong Secret type", withSecret: true, mutate: func(_ *appsv1.Artifact, s *corev1.Secret) { s.Type = corev1.SecretTypeOpaque }, wantReason: ReasonArtifactSecretInvalid},
		{name: "mutable Secret", withSecret: true, mutate: func(_ *appsv1.Artifact, s *corev1.Secret) { s.Immutable = nil }, wantReason: ReasonArtifactSecretInvalid},
		{name: "missing data key", withSecret: true, mutate: func(_ *appsv1.Artifact, s *corev1.Secret) { s.Data = nil }, wantReason: ReasonArtifactSecretInvalid},
		{name: "annotation digest mismatch", withSecret: true, mutate: func(_ *appsv1.Artifact, s *corev1.Secret) {
			s.Annotations[ContentDigestAnnotation] = digestOf([]byte("other"))
		}, wantReason: ReasonArtifactDigestMismatch},
		{name: "content digest mismatch", withSecret: true, mutate: func(a *appsv1.Artifact, s *corev1.Secret) {
			a.Digest = digestOf([]byte("other"))
			s.Annotations[ContentDigestAnnotation] = a.Digest
		}, wantReason: ReasonArtifactDigestMismatch},
		{name: "unmatched digest", withSecret: true, mutate: func(a *appsv1.Artifact, _ *corev1.Secret) { a.Digest = "sha256:nope" }, wantReason: ReasonArtifactDigestMismatch},
		{name: "empty key", withSecret: true, mutate: func(a *appsv1.Artifact, _ *corev1.Secret) { a.SecretRef.Key = "" }, wantReason: ReasonArtifactSecretInvalid},
		{name: "invalid chart", withSecret: true, mutate: func(a *appsv1.Artifact, s *corev1.Secret) {
			s.Data[ChartSecretKey] = []byte("not a chart")
			a.Digest = digestOf(s.Data[ChartSecretKey])
			s.Annotations[ContentDigestAnnotation] = a.Digest
		}, wantReason: ReasonArtifactLoadFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := newArtifact()
			secret := newSecret()
			if tt.mutate != nil {
				tt.mutate(artifact, secret)
			}
			var loader *ArtifactLoader
			if tt.withSecret {
				loader = newTestArtifactLoader(t, secret)
			} else {
				loader = newTestArtifactLoader(t)
			}
			_, _, _, err := loader.Load(context.Background(), "default", artifact)
			assertArtifactReason(t, err, tt.wantReason)
		})
	}
}

func newTestArtifactLoader(t *testing.T, objects ...runtime.Object) *ArtifactLoader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
	return NewArtifactLoader(cli, t.TempDir())
}

func assertArtifactReason(t *testing.T, err error, want metav1.StatusReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("Load() error = nil, want reason %s", want)
	}
	if got := apierrors.ReasonForError(err); got != want {
		t.Fatalf("Load() reason = %s, want %s (error: %v)", got, want, err)
	}
	statusErr, ok := err.(*apierrors.StatusError)
	if !ok {
		t.Fatalf("Load() error type = %T, want *apierrors.StatusError", err)
	}
	wantCode := int32(422)
	switch want {
	case ReasonArtifactSecretNotFound:
		wantCode = 404
	case ReasonArtifactDigestMismatch:
		wantCode = 409
	}
	status := statusErr.Status()
	if status.Code != wantCode {
		t.Fatalf("Load() status code = %d, want %d", status.Code, wantCode)
	}
	if status.Details == nil || status.Details.Group != "apps.xiaoshiai.cn" || status.Details.Kind != "Artifact" {
		t.Fatalf("Load() status details = %#v, want apps.xiaoshiai.cn/Artifact", status.Details)
	}
}

func digestOf(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func testChartArchive(t *testing.T, version, values string) []byte {
	t.Helper()
	files := map[string]string{
		"test-chart/Chart.yaml":               fmt.Sprintf("apiVersion: v2\nname: test-chart\nversion: %s\n", version),
		"test-chart/values.yaml":              values + "\n",
		"test-chart/templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: '{{ .Release.Name }}'\ndata:\n  value: '{{ .Values.value }}'\n",
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return out.Bytes()
}
