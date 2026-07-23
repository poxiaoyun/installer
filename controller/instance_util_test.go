package controller

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
)

type countingInstaller struct {
	applyCount int
}

func (c *countingInstaller) Apply(_ context.Context, instance install.Instance) (*install.InstanceStatus, error) {
	c.applyCount++
	return &install.InstanceStatus{
		Values:     instance.Values,
		Version:    "1.0.0",
		AppVersion: "2.0.0",
	}, nil
}

func (c *countingInstaller) Remove(context.Context, install.Instance) error {
	return nil
}

func (c *countingInstaller) Template(context.Context, install.Instance) ([]byte, error) {
	return nil, nil
}

func TestSyncInstallAppliesOncePerDesiredState(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
	}).Build()
	applier := &countingInstaller{}
	reconciler := &InstanceReconciler{
		Client:                       cli,
		Applier:                      applier,
		AllowClusterScopedNamespaces: map[string]struct{}{},
	}
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", Generation: 1},
		Spec: appsv1.InstanceSpec{
			Kind:   appsv1.InstanceKindHelm,
			URL:    "oci://example.test/demo",
			Values: appsv1.Values{Object: map[string]any{"replicas": int64(1)}},
		},
	}

	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("first syncInstall() error = %v", err)
	}
	if applier.applyCount != 1 {
		t.Fatalf("Apply() calls after first sync = %d, want 1", applier.applyCount)
	}
	if instance.Status.Version != "1.0.0" || instance.Status.AppVersion != "2.0.0" {
		t.Fatalf("status versions = %q/%q, want 1.0.0/2.0.0", instance.Status.Version, instance.Status.AppVersion)
	}
	// Reconcile records the generation after the sync succeeds.
	instance.Status.ObservedGeneration = instance.Generation

	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("unchanged syncInstall() error = %v", err)
	}
	if applier.applyCount != 1 {
		t.Fatalf("Apply() calls after unchanged sync = %d, want 1", applier.applyCount)
	}

	// Runtime expression annotations are evaluated from Instance metadata and do
	// not change the desired Helm release state.
	instance.Annotations = map[string]string{
		appsv1.AnnotationSummaryExpression: `{"source":"instance"}`,
	}
	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("annotation-only syncInstall() error = %v", err)
	}
	if applier.applyCount != 1 {
		t.Fatalf("Apply() calls after annotation-only change = %d, want 1", applier.applyCount)
	}
	if err := reconciler.checkAnnotations(context.Background(), instance, nil); err != nil {
		t.Fatalf("checkAnnotations() error = %v", err)
	}
	if got := instance.Status.Summary["source"]; got != "instance" {
		t.Fatalf("summary source = %q, want instance", got)
	}

	delete(instance.Annotations, appsv1.AnnotationSummaryExpression)
	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("annotation removal syncInstall() error = %v", err)
	}
	if applier.applyCount != 1 {
		t.Fatalf("Apply() calls after annotation removal = %d, want 1", applier.applyCount)
	}
	if err := reconciler.checkAnnotations(context.Background(), instance, nil); err != nil {
		t.Fatalf("checkAnnotations() after annotation removal error = %v", err)
	}
	if instance.Status.Summary != nil {
		t.Fatalf("summary after annotation removal = %#v", instance.Status.Summary)
	}

	// Extensions affect post-rendering even when chart version and values stay the same.
	instance.Generation++
	instance.Spec.Extensions = []appsv1.Extension{{Name: "common-metadata", Kind: "CommonMetadata"}}
	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("extension change syncInstall() error = %v", err)
	}
	if applier.applyCount != 2 {
		t.Fatalf("Apply() calls after extension change = %d, want 2", applier.applyCount)
	}
	instance.Status.ObservedGeneration = instance.Generation

	if err := reconciler.syncInstall(context.Background(), instance); err != nil {
		t.Fatalf("unchanged extension syncInstall() error = %v", err)
	}
	if applier.applyCount != 2 {
		t.Fatalf("Apply() calls after unchanged extension sync = %d, want 2", applier.applyCount)
	}
}

func TestExecutionUpToDate(t *testing.T) {
	base := func() *appsv1.Instance {
		return &appsv1.Instance{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Spec: appsv1.InstanceSpec{
				Kind:       appsv1.InstanceKindHelm,
				URL:        "oci://example.test/chart",
				Version:    "repository-tag",
				Values:     appsv1.Values{Object: map[string]any{"replicas": int64(1)}},
				Extensions: []appsv1.Extension{{Name: "common-metadata", Kind: "CommonMetadata"}},
			},
			Status: appsv1.InstanceStatus{
				ObservedGeneration: 2,
				Version:            "1.0.0",
				Values:             appsv1.Values{Object: map[string]any{"replicas": int64(1)}},
				Extensions:         []appsv1.Extension{{Name: "common-metadata", Kind: "CommonMetadata"}},
				Conditions: []metav1.Condition{{
					Type:   appsv1.ConditionInstalled,
					Status: metav1.ConditionTrue,
				}},
			},
		}
	}
	values := map[string]any{"replicas": int64(1)}

	t.Run("same desired and actual configuration", func(t *testing.T) {
		if !executionUpToDate(base(), values) {
			t.Fatal("executionUpToDate() = false, want true")
		}
	})
	t.Run("failed installation", func(t *testing.T) {
		instance := base()
		instance.Status.Conditions[0].Status = metav1.ConditionFalse
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("new generation", func(t *testing.T) {
		instance := base()
		instance.Generation++
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("resolved values changed", func(t *testing.T) {
		if executionUpToDate(base(), map[string]any{"replicas": int64(2)}) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("extensions changed", func(t *testing.T) {
		instance := base()
		instance.Spec.Extensions = nil
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("matching artifact digest", func(t *testing.T) {
		instance := base()
		instance.Spec.URL = ""
		instance.Spec.Version = ""
		instance.Spec.Artifact = &appsv1.Artifact{Digest: "sha256:expected"}
		instance.Status.Artifact = &appsv1.ArtifactStatus{Digest: "sha256:expected"}
		if !executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = false, want true")
		}
	})
	t.Run("artifact digest changed", func(t *testing.T) {
		instance := base()
		instance.Spec.URL = ""
		instance.Spec.Artifact = &appsv1.Artifact{Digest: "sha256:new"}
		instance.Status.Artifact = &appsv1.ArtifactStatus{Digest: "sha256:old"}
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("optional artifact digest", func(t *testing.T) {
		instance := base()
		instance.Spec.URL = ""
		instance.Spec.Artifact = &appsv1.Artifact{}
		instance.Status.Artifact = &appsv1.ArtifactStatus{Digest: "sha256:actual"}
		if !executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = false, want true")
		}
	})
	t.Run("actual artifact but desired legacy source", func(t *testing.T) {
		instance := base()
		instance.Status.Artifact = &appsv1.ArtifactStatus{Digest: "sha256:actual"}
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
	t.Run("desired artifact but actual legacy source", func(t *testing.T) {
		instance := base()
		instance.Spec.URL = ""
		instance.Spec.Version = ""
		instance.Spec.Artifact = &appsv1.Artifact{}
		if executionUpToDate(instance, values) {
			t.Fatal("executionUpToDate() = true, want false")
		}
	})
}

func TestValidateInstanceSource(t *testing.T) {
	validArtifact := func() *appsv1.Artifact {
		return &appsv1.Artifact{
			SecretRef: appsv1.ArtifactSecretRef{Name: "demo-1.0.0", Key: "chart.tgz"},
			Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}
	}
	tests := []struct {
		name    string
		spec    appsv1.InstanceSpec
		wantErr bool
	}{
		{name: "legacy URL", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, URL: "oci://example.test/chart"}},
		{name: "artifact", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact()}},
		{name: "default helm artifact", spec: appsv1.InstanceSpec{Artifact: validArtifact()}},
		{name: "missing source", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm}, wantErr: true},
		{name: "artifact for kustomize", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindKustomize, Artifact: validArtifact()}, wantErr: true},
		{name: "artifact with URL", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), URL: "oci://example.test/chart"}, wantErr: true},
		{name: "artifact with version", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), Version: "1.0.0"}, wantErr: true},
		{name: "artifact with auth", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), Auth: &appsv1.RepositoryAuth{}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInstanceSource(&appsv1.Instance{Spec: tt.spec})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateInstanceSource() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanNilValues(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name: "flat map with nil",
			input: map[string]any{
				"a": 1,
				"b": nil,
				"c": "foo",
			},
			expected: map[string]any{
				"a": 1,
				"c": "foo",
			},
		},
		{
			name: "nested map with nil",
			input: map[string]any{
				"a": 1,
				"b": map[string]any{
					"b1": nil,
					"b2": "bar",
				},
				"c": nil,
			},
			expected: map[string]any{
				"a": 1,
				"b": map[string]any{
					"b2": "bar",
				},
			},
		},
		{
			name: "deeply nested map with nil",
			input: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": nil,
						"d": 100,
					},
				},
			},
			expected: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"d": 100,
					},
				},
			},
		},
		{
			name: "no nil values",
			input: map[string]any{
				"a": 1,
				"b": "foo",
				"c": map[string]any{"d": 2},
			},
			expected: map[string]any{
				"a": 1,
				"b": "foo",
				"c": map[string]any{"d": 2},
			},
		},
		{
			name:     "empty map",
			input:    map[string]any{},
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanNilValues(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("cleanNilValues() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMergeMaps(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]any
		b        map[string]any
		expected map[string]any
	}{
		{
			name:     "flat merge",
			a:        map[string]any{"a": 1, "b": 2},
			b:        map[string]any{"b": 3, "c": 4},
			expected: map[string]any{"a": 1, "b": 3, "c": 4},
		},
		{
			name:     "nested merge",
			a:        map[string]any{"a": map[string]any{"a1": 1}},
			b:        map[string]any{"a": map[string]any{"a2": 2}},
			expected: map[string]any{"a": map[string]any{"a1": 1, "a2": 2}},
		},
		{
			name:     "recursive nested merge",
			a:        map[string]any{"a": map[string]any{"b": map[string]any{"c1": 1}}},
			b:        map[string]any{"a": map[string]any{"b": map[string]any{"c2": 2}}},
			expected: map[string]any{"a": map[string]any{"b": map[string]any{"c1": 1, "c2": 2}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeMaps(tt.a, tt.b)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("mergeMaps() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMergeInto(t *testing.T) {
	base := map[string]any{
		"foo": "bar",
	}
	err := mergeInto("a.b.c", "val", base)
	if err != nil {
		t.Fatalf("mergeInto failed: %v", err)
	}

	expected := map[string]any{
		"foo": "bar",
		"a": map[string]any{
			"b": map[string]any{
				"c": "val",
			},
		},
	}

	if !reflect.DeepEqual(base, expected) {
		t.Errorf("mergeInto() = %v, want %v", base, expected)
	}
}
