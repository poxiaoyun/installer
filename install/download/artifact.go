package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/chart/loader"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

const (
	ChartSecretType         corev1.SecretType = "apps.xiaoshiai.cn/helm-chart.v1"
	ContentDigestAnnotation                   = "apps.xiaoshiai.cn/content-digest"
	ChartSecretKey                            = "chart.tgz"

	ReasonArtifactSecretNotFound = "ArtifactSecretNotFound"
	ReasonArtifactSecretInvalid  = "ArtifactSecretInvalid"
	ReasonArtifactDigestMismatch = "ArtifactDigestMismatch"
	ReasonArtifactLoadFailed     = "ArtifactLoadFailed"
)

// ArtifactLoader verifies a chart Secret and exposes it as a temporary archive.
type ArtifactLoader struct {
	Client   client.Client
	CacheDir string
}

func NewArtifactLoader(cli client.Client, cacheDir string) *ArtifactLoader {
	return &ArtifactLoader{Client: cli, CacheDir: cacheDir}
}

// Load reads and verifies an artifact from the Instance namespace. The caller
// must invoke the returned cleanup function after the chart consumer finishes.
func (l *ArtifactLoader) Load(ctx context.Context, namespace string, artifact *appsv1.Artifact) (string, string, func(), error) {
	if err := validateArtifact(artifact); err != nil {
		return "", "", func() {}, err
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: namespace, Name: artifact.SecretRef.Name}
	if err := l.Client.Get(ctx, key, secret); err != nil {
		reason := ReasonArtifactSecretInvalid
		if apierrors.IsNotFound(err) {
			reason = ReasonArtifactSecretNotFound
		}
		return "", "", func() {}, artifactError(reason, "get chart Secret %s/%s: %v", namespace, artifact.SecretRef.Name, err)
	}
	if secret.Type != ChartSecretType {
		return "", "", func() {}, artifactError(ReasonArtifactSecretInvalid, "chart Secret %s/%s has type %q, expected %q", namespace, secret.Name, secret.Type, ChartSecretType)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		return "", "", func() {}, artifactError(ReasonArtifactSecretInvalid, "chart Secret %s/%s must be immutable", namespace, secret.Name)
	}

	archive, ok := secret.Data[artifact.SecretRef.Key]
	if !ok || len(archive) == 0 {
		return "", "", func() {}, artifactError(ReasonArtifactSecretInvalid, "chart Secret %s/%s does not contain non-empty data key %q", namespace, secret.Name, artifact.SecretRef.Key)
	}
	annotationDigest := secret.Annotations[ContentDigestAnnotation]
	actualDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	if artifact.Digest != "" && actualDigest != artifact.Digest {
		return "", "", func() {}, artifactError(ReasonArtifactDigestMismatch, "chart Secret %s/%s digest mismatch: expected %s, actual %s", namespace, secret.Name, artifact.Digest, actualDigest)
	}
	if annotationDigest != "" && actualDigest != annotationDigest {
		return "", "", func() {}, artifactError(ReasonArtifactDigestMismatch, "chart Secret %s/%s annotation digest mismatch: expected %s, actual %s", namespace, secret.Name, annotationDigest, actualDigest)
	}
	if _, err := loader.LoadArchive(bytes.NewReader(archive)); err != nil {
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "load chart from Secret %s/%s: %v", namespace, secret.Name, err)
	}

	dir := l.CacheDir
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "create artifact temporary directory: %v", err)
	}
	f, err := os.CreateTemp(dir, "chart-*.tgz")
	if err != nil {
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "create artifact temporary file: %v", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "secure artifact temporary file: %v", err)
	}
	if _, err := f.Write(archive); err != nil {
		_ = f.Close()
		cleanup()
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "write artifact temporary file: %v", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", "", func() {}, artifactError(ReasonArtifactLoadFailed, "close artifact temporary file: %v", err)
	}
	return path, actualDigest, cleanup, nil
}

func validateArtifact(artifact *appsv1.Artifact) error {
	if artifact == nil {
		return artifactError(ReasonArtifactSecretInvalid, "artifact is required")
	}
	if artifact.SecretRef.Name == "" {
		return artifactError(ReasonArtifactSecretInvalid, "artifact Secret name is required")
	}
	if artifact.SecretRef.Key == "" {
		return artifactError(ReasonArtifactSecretInvalid, "artifact Secret key is required")
	}
	return nil
}

func artifactError(reason string, format string, args ...any) error {
	code := int32(422)
	switch reason {
	case ReasonArtifactSecretNotFound:
		code = 404
	case ReasonArtifactDigestMismatch:
		code = 409
	}
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReason(reason),
		Message:  fmt.Sprintf(format, args...),
		Code:     code,
		Details: &metav1.StatusDetails{
			Group: "apps.xiaoshiai.cn",
			Kind:  "Artifact",
		},
	}}
}
