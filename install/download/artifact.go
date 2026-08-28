package download

import (
	"context"
	"crypto/sha256"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install/filesystem"
	"xiaoshiai.cn/installer/install/filesystem/memoryfs"
)

const (
	ReasonArtifactSecretNotFound = "ArtifactSecretNotFound"
	ReasonArtifactSecretInvalid  = "ArtifactSecretInvalid"
	ReasonArtifactDigestMismatch = "ArtifactDigestMismatch"
)

// ArtifactLoader verifies a Secret-backed artifact and exposes its selected data
// through a read-only in-memory filesystem.
type ArtifactLoader struct {
	Client client.Client
}

func NewArtifactLoader(cli client.Client) *ArtifactLoader {
	return &ArtifactLoader{Client: cli}
}

// Load reads and verifies an artifact from the Instance namespace.
func (l *ArtifactLoader) Load(ctx context.Context, namespace string, artifact *appsv1.Artifact) (filesystem.Location, string, error) {
	if err := validateArtifact(artifact); err != nil {
		return filesystem.Location{}, "", err
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: namespace, Name: artifact.SecretRef.Name}
	if err := l.Client.Get(ctx, key, secret); err != nil {
		reason := ReasonArtifactSecretInvalid
		if apierrors.IsNotFound(err) {
			reason = ReasonArtifactSecretNotFound
		}
		return filesystem.Location{}, "", artifactError(reason, "get artifact Secret %s/%s: %v", namespace, artifact.SecretRef.Name, err)
	}
	if secret.Immutable == nil || !*secret.Immutable {
		return filesystem.Location{}, "", artifactError(ReasonArtifactSecretInvalid, "artifact Secret %s/%s must be immutable", namespace, secret.Name)
	}

	data, ok := secret.Data[artifact.SecretRef.Key]
	if !ok || len(data) == 0 {
		return filesystem.Location{}, "", artifactError(ReasonArtifactSecretInvalid, "artifact Secret %s/%s does not contain non-empty data key %q", namespace, secret.Name, artifact.SecretRef.Key)
	}
	annotationDigest := secret.Annotations[apps.ContentDigestAnnotation]
	actualDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if artifact.Digest != "" && actualDigest != artifact.Digest {
		return filesystem.Location{}, "", artifactError(ReasonArtifactDigestMismatch, "artifact Secret %s/%s digest mismatch: expected %s, actual %s", namespace, secret.Name, artifact.Digest, actualDigest)
	}
	if annotationDigest != "" && actualDigest != annotationDigest {
		return filesystem.Location{}, "", artifactError(ReasonArtifactDigestMismatch, "artifact Secret %s/%s annotation digest mismatch: expected %s, actual %s", namespace, secret.Name, annotationDigest, actualDigest)
	}

	return filesystem.Location{
		FS: memoryfs.New(map[string]memoryfs.File{
			artifact.SecretRef.Key: {Data: data, Mode: 0o444},
		}),
		Path: artifact.SecretRef.Key,
	}, actualDigest, nil
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
