package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
	"xiaoshiai.cn/installer/install"
)

type blockingInstaller struct {
	started chan struct{}
	removed chan struct{}
}

func (i *blockingInstaller) Apply(ctx context.Context, _ install.Instance) (*install.InstanceStatus, error) {
	close(i.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (i *blockingInstaller) Remove(context.Context, install.Instance) error {
	close(i.removed)
	return nil
}

func (i *blockingInstaller) Template(context.Context, install.Instance) ([]byte, error) {
	return nil, nil
}

func TestReconciliationTimeoutAllowsPendingDeletionToProceed(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}

	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "blocked",
			Namespace:  "default",
			Finalizers: []string{apps.FinalizerName},
		},
		Spec: appsv1.InstanceSpec{
			Kind: appsv1.InstanceKindHelm,
			URL:  "oci://example.test/blocked",
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
	applier := &blockingInstaller{started: make(chan struct{}), removed: make(chan struct{})}
	reconciler := &InstanceReconciler{
		Client:                       cli,
		Scheme:                       scheme,
		Applier:                      applier,
		AllowClusterScopedNamespaces: map[string]struct{}{},
	}
	controller, err := ctrlcontroller.NewUnmanaged("timeout-test", ctrlcontroller.Options{
		Reconciler:            reconciler,
		ReconciliationTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)}
	result := make(chan error, 1)
	go func() {
		_, err := controller.Reconcile(t.Context(), request)
		result <- err
	}()

	select {
	case <-applier.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}
	current := &appsv1.Instance{}
	if err := cli.Get(t.Context(), request.NamespacedName, current); err != nil {
		t.Fatalf("get instance before delete: %v", err)
	}
	if err := cli.Delete(t.Context(), current); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("first Reconcile() error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Reconcile() did not time out")
	}

	if _, err := controller.Reconcile(t.Context(), request); err != nil {
		t.Fatalf("deletion Reconcile() error = %v", err)
	}
	select {
	case <-applier.removed:
	default:
		t.Fatal("deletion Reconcile() did not call Remove()")
	}
}

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

func TestMissingDependencyWaitsWithoutReconcileError(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}

	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "dependent",
			Namespace:  "default",
			Generation: 1,
			Finalizers: []string{apps.FinalizerName},
		},
		Spec: appsv1.InstanceSpec{
			Dependencies: []corev1.ObjectReference{{Name: "database"}},
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&appsv1.Instance{}).
		WithObjects(instance).
		Build()
	applier := &countingInstaller{}
	reconciler := &InstanceReconciler{Client: cli, Scheme: scheme, Applier: applier}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(instance)}

	result, err := reconciler.Reconcile(t.Context(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want expected dependency wait", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Fatalf("Reconcile() RequeueAfter = %s, want %s", result.RequeueAfter, time.Minute)
	}
	if applier.applyCount != 0 {
		t.Fatalf("Apply() calls = %d, want 0", applier.applyCount)
	}

	current := &appsv1.Instance{}
	if err := cli.Get(t.Context(), request.NamespacedName, current); err != nil {
		t.Fatalf("get Instance: %v", err)
	}
	if current.Status.Phase != appsv1.PhaseWaiting {
		t.Fatalf("phase = %q, want %q", current.Status.Phase, appsv1.PhaseWaiting)
	}
	if current.Status.ObservedGeneration != current.Generation {
		t.Fatalf("observed generation = %d, want %d", current.Status.ObservedGeneration, current.Generation)
	}
	condition := meta.FindStatusCondition(current.Status.Conditions, appsv1.ConditionDependenciesReady)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != ReasonDependencyNotReady {
		t.Fatalf("DependenciesReady condition = %#v, want False/%s", condition, ReasonDependencyNotReady)
	}
}

func TestInstalledInstanceIgnoresUnreadyDependency(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}

	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "dependent", Namespace: "default", Generation: 1},
		Spec: appsv1.InstanceSpec{
			URL:          "oci://example.test/dependent",
			Dependencies: []corev1.ObjectReference{{Name: "database"}},
		},
		Status: appsv1.InstanceStatus{
			ObservedGeneration: 1,
			Phase:              appsv1.PhaseInstalled,
			Values: appsv1.Values{Object: map[string]any{
				"global": map[string]any{"replicas": float64(1)},
			}},
			Conditions: []metav1.Condition{{
				Type:               appsv1.ConditionInstalled,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             ReasonInstalled,
			}},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	applier := &countingInstaller{}
	reconciler := &InstanceReconciler{Client: cli, Scheme: scheme, Applier: applier}

	if err := reconciler.Sync(t.Context(), instance); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if applier.applyCount != 0 {
		t.Fatalf("Apply() calls = %d, want 0", applier.applyCount)
	}
	if instance.Status.Phase != appsv1.PhaseInstalled {
		t.Fatalf("phase = %q, want %q", instance.Status.Phase, appsv1.PhaseInstalled)
	}
}

func TestDependencyEventRequestsOnlyDependentsPendingInstallation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	dependent := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "apps"},
		Spec: appsv1.InstanceSpec{
			Dependencies: []corev1.ObjectReference{{Name: "database", Namespace: "data"}},
		},
		Status: appsv1.InstanceStatus{Phase: appsv1.PhaseWaiting},
	}
	installed := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "frontend", Namespace: "apps", Generation: 1},
		Spec: appsv1.InstanceSpec{
			Dependencies: []corev1.ObjectReference{{Name: "database", Namespace: "data"}},
		},
		Status: appsv1.InstanceStatus{Conditions: []metav1.Condition{{
			Type:               appsv1.ConditionInstalled,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		}}},
	}
	unrelated := &appsv1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "apps"}}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&appsv1.Instance{}, instanceDependencyIndexField, instanceDependencyIndexValues).
		WithObjects(dependent, installed, unrelated).
		Build()
	handler := dependencyEventHandler{Client: cli}
	dependency := &appsv1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "data"}}

	requests := handler.requestsFor(t.Context(), dependency)
	want := []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(dependent)}}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
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
		apps.AnnotationSummaryExpression: `{"source":"instance"}`,
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

	delete(instance.Annotations, apps.AnnotationSummaryExpression)
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
	instance.Spec.Extensions = []appsv1.Extension{{Name: "common-metadata", Kind: apps.ExtensionKindCommonMetadata}}
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
				Extensions: []appsv1.Extension{{Name: "common-metadata", Kind: apps.ExtensionKindCommonMetadata}},
			},
			Status: appsv1.InstanceStatus{
				ObservedGeneration: 2,
				Version:            "1.0.0",
				Values:             appsv1.Values{Object: map[string]any{"replicas": int64(1)}},
				Extensions:         []appsv1.Extension{{Name: "common-metadata", Kind: apps.ExtensionKindCommonMetadata}},
				Conditions: []metav1.Condition{{
					Type:               appsv1.ConditionInstalled,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: 2,
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

func TestInstallerInstanceFromTLS(t *testing.T) {
	tlsConfig := &install.ResolvedTLS{CAData: []byte("ca"), CertData: []byte("cert"), KeyData: []byte("key"), InsecureSkipVerify: true}
	got := installerInstanceFrom(&appsv1.Instance{}, nil, nil, tlsConfig)
	if got.TLS != tlsConfig {
		t.Fatalf("TLS = %#v, want %#v", got.TLS, tlsConfig)
	}
}

func TestResolveAuthToken(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-auth", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token":    []byte("secret-token"),
			"username": []byte("secret-user"),
			"password": []byte("secret-password"),
		},
	}
	reconciler := &InstanceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()}
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: appsv1.InstanceSpec{Auth: &appsv1.RepositoryAuth{
			Token:     "inline-token",
			SecretRef: &corev1.LocalObjectReference{Name: secret.Name},
		}},
	}

	resolved, err := reconciler.resolveAuth(t.Context(), instance)
	if err != nil {
		t.Fatalf("resolveAuth() error = %v", err)
	}
	if resolved.Token != "inline-token" || resolved.Username != "secret-user" || resolved.Password != "secret-password" {
		t.Fatalf("resolveAuth() = %#v", resolved)
	}

	instance.Spec.Auth.Token = ""
	resolved, err = reconciler.resolveAuth(t.Context(), instance)
	if err != nil {
		t.Fatalf("resolveAuth() Secret token error = %v", err)
	}
	if resolved.Token != "secret-token" {
		t.Fatalf("resolveAuth() Secret token = %q, want secret-token", resolved.Token)
	}

	dockerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-auth", Namespace: "default"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"registry.example.com":{"registrytoken":"registry-token"}}}`),
		},
	}
	if err := reconciler.Client.Create(t.Context(), dockerSecret); err != nil {
		t.Fatalf("create Docker config Secret: %v", err)
	}
	instance.Spec.URL = "oci://registry.example.com/charts/demo"
	instance.Spec.Auth.SecretRef.Name = dockerSecret.Name
	resolved, err = reconciler.resolveAuth(t.Context(), instance)
	if err != nil {
		t.Fatalf("resolveAuth() Docker token error = %v", err)
	}
	if resolved.Token != "registry-token" {
		t.Fatalf("resolveAuth() Docker token = %q, want registry-token", resolved.Token)
	}
}

func TestResolveTLS(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-ca", Namespace: "default"},
		Data: map[string][]byte{
			corev1.ServiceAccountRootCAKey: []byte("test-ca"),
			corev1.TLSCertKey:              []byte("test-cert"),
			corev1.TLSPrivateKeyKey:        []byte("test-key"),
		},
	}
	reconciler := &InstanceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()}
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: appsv1.InstanceSpec{TLS: &appsv1.RepositoryTLS{
			SecretRef:          &corev1.LocalObjectReference{Name: "repository-ca"},
			InsecureSkipVerify: true,
		}},
	}

	got, err := reconciler.resolveTLS(t.Context(), instance)
	if err != nil {
		t.Fatalf("resolveTLS() error = %v", err)
	}
	if string(got.CAData) != "test-ca" || string(got.CertData) != "test-cert" || string(got.KeyData) != "test-key" || !got.InsecureSkipVerify {
		t.Fatalf("resolveTLS() = %#v", got)
	}
	got.CAData[0] = 'X'
	if string(secret.Data[corev1.ServiceAccountRootCAKey]) != "test-ca" {
		t.Fatal("resolveTLS() returned Secret-backed CA data without copying it")
	}

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "source-tls", Namespace: "default"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("source-cert"),
			corev1.TLSPrivateKeyKey: []byte("source-key"),
		},
	}
	if err := reconciler.Client.Create(t.Context(), tlsSecret); err != nil {
		t.Fatalf("create standard TLS Secret: %v", err)
	}
	instance.Spec.TLS.SecretRef.Name = tlsSecret.Name
	tlsOnly, err := reconciler.resolveTLS(t.Context(), instance)
	if err != nil {
		t.Fatalf("resolveTLS() standard TLS Secret error = %v", err)
	}
	if string(tlsOnly.CAData) != "source-cert" || string(tlsOnly.CertData) != "source-cert" || string(tlsOnly.KeyData) != "source-key" {
		t.Fatalf("resolveTLS() standard TLS Secret = %#v", tlsOnly)
	}
	instance.Spec.TLS.SecretRef.Name = secret.Name

	delete(secret.Data, corev1.TLSPrivateKeyKey)
	if err := reconciler.Client.Update(t.Context(), secret); err != nil {
		t.Fatalf("update TLS Secret: %v", err)
	}
	if _, err := reconciler.resolveTLS(t.Context(), instance); err == nil {
		t.Fatal("resolveTLS() accepted an incomplete client certificate pair")
	}
}

func TestSourceOptionsAreIgnoredWhenNotApplicable(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "source-ca", Namespace: "default"},
		Data:       map[string][]byte{corev1.ServiceAccountRootCAKey: []byte("test-ca")},
	}
	reconciler := &InstanceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tlsSecret).Build()}
	missingSecret := &corev1.LocalObjectReference{Name: "does-not-exist"}

	nonHelm := &appsv1.Instance{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}, Spec: appsv1.InstanceSpec{
		Kind: appsv1.InstanceKindKustomize,
		TLS:  &appsv1.RepositoryTLS{SecretRef: &corev1.LocalObjectReference{Name: tlsSecret.Name}},
	}}
	if got, err := reconciler.resolveTLS(t.Context(), nonHelm); err != nil || got == nil || string(got.CAData) != "test-ca" {
		t.Fatalf("resolveTLS() for non-Helm Instance = %#v, %v; want resolved CA", got, err)
	}

	artifact := &appsv1.Instance{Spec: appsv1.InstanceSpec{
		Kind:     appsv1.InstanceKindHelm,
		Artifact: &appsv1.Artifact{},
		Auth:     &appsv1.RepositoryAuth{SecretRef: missingSecret},
		TLS:      &appsv1.RepositoryTLS{SecretRef: missingSecret},
	}}
	if got, err := reconciler.resolveAuth(t.Context(), artifact); err != nil || got != nil {
		t.Fatalf("resolveAuth() for Artifact source = %#v, %v; want nil, nil", got, err)
	}
	if got, err := reconciler.resolveTLS(t.Context(), artifact); err != nil || got != nil {
		t.Fatalf("resolveTLS() for Artifact source = %#v, %v; want nil, nil", got, err)
	}
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
		{name: "artifact for kustomize", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindKustomize, Artifact: validArtifact()}},
		{name: "artifact takes precedence over URL", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), URL: "oci://example.test/chart"}},
		{name: "artifact with ignored version", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), Version: "1.0.0"}},
		{name: "artifact with ignored chart and path", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), Chart: "ignored", Path: "ignored"}},
		{name: "artifact with ignored auth", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), Auth: &appsv1.RepositoryAuth{}}},
		{name: "artifact with ignored tls", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindHelm, Artifact: validArtifact(), TLS: &appsv1.RepositoryTLS{}}},
		{name: "kustomize with ignored tls", spec: appsv1.InstanceSpec{Kind: appsv1.InstanceKindKustomize, URL: "https://example.test/repo.git", TLS: &appsv1.RepositoryTLS{}}},
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

func TestResolveValuesInjectsInstanceRuntimeValues(t *testing.T) {
	replicas := func(value int32) *int32 { return &value }
	tests := []struct {
		name           string
		instance       *appsv1.Instance
		wantReplicas   int32
		wantPaused     bool
		wantGlobalKeep string
	}{
		{
			name:         "default replicas",
			instance:     &appsv1.Instance{},
			wantReplicas: 1,
		},
		{
			name: "zero replicas does not derive paused",
			instance: &appsv1.Instance{Spec: appsv1.InstanceSpec{
				Replicas: replicas(0),
			}},
			wantReplicas: 0,
		},
		{
			name: "instance replicas override user value and preserve paused",
			instance: &appsv1.Instance{Spec: appsv1.InstanceSpec{
				Replicas: replicas(3),
				Values: appsv1.Values{Object: map[string]any{
					"global": map[string]any{
						"replicas": int64(9),
						"paused":   true,
						"keep":     "value",
					},
				}},
			}},
			wantReplicas:   3,
			wantPaused:     true,
			wantGlobalKeep: "value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := (&InstanceReconciler{}).resolveValues(t.Context(), tt.instance)
			if err != nil {
				t.Fatal(err)
			}
			global, ok := values["global"].(map[string]any)
			if !ok {
				t.Fatalf("global values = %#v", values["global"])
			}
			if got := getGlobalReplicas(values); got != tt.wantReplicas {
				t.Fatalf("global.replicas = %d, want %d", got, tt.wantReplicas)
			}
			if _, ok := global["replicas"].(float64); !ok {
				t.Fatalf("global.replicas has type %T, want float64 JSON number", global["replicas"])
			}
			if got, _ := global["paused"].(bool); got != tt.wantPaused {
				t.Fatalf("global.paused = %v, want %v", got, tt.wantPaused)
			}
			if got, _ := global["keep"].(string); got != tt.wantGlobalKeep {
				t.Fatalf("global.keep = %q, want %q", got, tt.wantGlobalKeep)
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
