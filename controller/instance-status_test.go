package controller

import (
	"reflect"
	"testing"

	k8sappsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

func newEmptyStatusReconciler(t *testing.T) *InstanceReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return &InstanceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
}

func TestExpressionResultsAreMappedPermissively(t *testing.T) {
	data := CELData{Values: map[string]any{"url": "service-name"}}

	states, err := checkStates(`[{"name":"worker","kind":"Deployment","status":"Running"}]`, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Kind != "Deployment" {
		t.Fatalf("states = %#v", states)
	}
	states, err = checkStates(`[{"name":1,"kind":2,"status":"","message":3},"ignored"]`, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != (appsv1.State{}) {
		t.Fatalf("permissive states = %#v", states)
	}

	endpoints, err := checkEndpoints(`[{"name":"upstream","url":values.url,"kind":"","relation":"Consumes"}]`, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 ||
		endpoints[0].URL != "service-name" ||
		endpoints[0].Kind != appsv1.EndpointKind("") ||
		endpoints[0].Relation != appsv1.EndpointRelation("Consumes") {
		t.Fatalf("endpoints = %#v", endpoints)
	}
	endpoints, err = checkEndpoints(`[{"name":1,"url":"","kind":1,"relation":1,"urls":["one",2]},"ignored"]`, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 ||
		endpoints[0].Name != "" ||
		endpoints[0].URL != "" ||
		endpoints[0].Kind != "" ||
		endpoints[0].Relation != "" ||
		!reflect.DeepEqual(endpoints[0].URLs, []string{"one"}) {
		t.Fatalf("permissive endpoints = %#v", endpoints)
	}

	summary, err := checkSummary(`{"replicas": 2, "name": "worker"}`, data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary, map[string]string{"name": "worker"}) {
		t.Fatalf("permissive summary = %#v", summary)
	}
}

func TestCheckAnnotationsUsesInstanceExpressions(t *testing.T) {
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo",
			Namespace:  "default",
			Generation: 3,
			Annotations: map[string]string{
				apps.AnnotationSummaryExpression:             `{"source":"instance"}`,
				apps.AnnotationEndpointsExpression:           `[{"name":"z-base","url":"https://base.example.com","kind":"External"}]`,
				apps.AnnotationAdditionalEndpointsExpression: `[{"name":"a-dependency","url":"redis://redis.default:6379","kind":"Cluster","relation":"ReadsFrom"}]`,
			},
		},
	}
	r := newEmptyStatusReconciler(t)
	if err := r.checkAnnotations(t.Context(), instance, nil); err != nil {
		t.Fatal(err)
	}
	if got := instance.Status.Summary["source"]; got != "instance" {
		t.Fatalf("summary source = %q", got)
	}
	if len(instance.Status.Endpoints) != 2 {
		t.Fatalf("endpoints = %#v", instance.Status.Endpoints)
	}
	if instance.Status.Endpoints[0].Name != "z-base" || instance.Status.Endpoints[1].Name != "a-dependency" {
		t.Fatalf("additional endpoint did not remain after base result: %#v", instance.Status.Endpoints)
	}
	if !meta.IsStatusConditionTrue(instance.Status.Conditions, appsv1.ConditionExpressionsReady) {
		t.Fatalf("conditions = %#v", instance.Status.Conditions)
	}

	instance.Annotations[apps.AnnotationSummaryExpression] = `{`
	instance.Status.Summary = map[string]string{"stale": "value"}
	if err := r.checkAnnotations(t.Context(), instance, nil); err == nil {
		t.Fatal("expected invalid summary expression result")
	}
	if instance.Status.Summary != nil {
		t.Fatalf("stale summary was retained: %#v", instance.Status.Summary)
	}
	condition := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionExpressionsReady)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != ReasonExpressionEvaluationFailed {
		t.Fatalf("condition = %#v", condition)
	}
}

func TestSyncStatusKeepsExpressionFailureSeparateFromRuntimePhase(t *testing.T) {
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
			Annotations: map[string]string{
				apps.AnnotationStatesExpression: `[`,
			},
		},
	}
	r := newEmptyStatusReconciler(t)
	if err := r.syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if instance.Status.Phase != appsv1.PhaseInstalled {
		t.Fatalf("phase = %q", instance.Status.Phase)
	}
	ready := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("ready condition = %#v", ready)
	}
	expressionsReady := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionExpressionsReady)
	if expressionsReady == nil || expressionsReady.Status != metav1.ConditionFalse || expressionsReady.Reason != ReasonExpressionEvaluationFailed {
		t.Fatalf("expressions ready condition = %#v", expressionsReady)
	}
}

func TestSyncStatusUsesIndependentPausedValue(t *testing.T) {
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Status: appsv1.InstanceStatus{Values: appsv1.Values{Object: map[string]any{
			"global": map[string]any{"replicas": int32(3), "paused": true},
		}}},
	}

	if err := newEmptyStatusReconciler(t).syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if instance.Status.Phase != appsv1.PhasePaused {
		t.Fatalf("phase = %q, want %q", instance.Status.Phase, appsv1.PhasePaused)
	}
	if instance.Status.Replicas != 0 {
		t.Fatalf("status replicas = %d, want 0", instance.Status.Replicas)
	}
	if instance.Status.Selector != "app.kubernetes.io/instance=demo" {
		t.Fatalf("status selector = %q", instance.Status.Selector)
	}
	ready := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonPaused {
		t.Fatalf("ready condition = %#v", ready)
	}
}

func TestSyncStatusDoesNotTreatZeroReplicasAsPaused(t *testing.T) {
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Status: appsv1.InstanceStatus{Values: appsv1.Values{Object: map[string]any{
			"global": map[string]any{"replicas": int32(0), "paused": false},
		}}},
	}

	if err := newEmptyStatusReconciler(t).syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if instance.Status.Phase != appsv1.PhaseInstalled {
		t.Fatalf("phase = %q, want %q", instance.Status.Phase, appsv1.PhaseInstalled)
	}
	if instance.Status.Replicas != 0 {
		t.Fatalf("status replicas = %d, want 0", instance.Status.Replicas)
	}
}

func TestSyncStatusCountsPodsMatchingDefaultScaleSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "running",
				Namespace: "default",
				Labels:    map[string]string{apps.LabelInstance: "demo"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "completed",
				Namespace: "default",
				Labels:    map[string]string{apps.LabelInstance: "demo"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "another-instance",
				Namespace: "default",
				Labels:    map[string]string{apps.LabelInstance: "other"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	).Build()
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Status: appsv1.InstanceStatus{Values: appsv1.Values{Object: map[string]any{
			"global": map[string]any{"replicas": int32(5), "paused": false},
		}}},
	}

	if err := (&InstanceReconciler{Client: cli}).syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if instance.Status.Selector != "app.kubernetes.io/instance=demo" {
		t.Fatalf("status selector = %q", instance.Status.Selector)
	}
	if instance.Status.Replicas != 1 {
		t.Fatalf("status replicas = %d, want 1", instance.Status.Replicas)
	}
}

func TestSyncStatusNarrowsPodsWithAdditionalScaleSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "worker",
				Namespace: "default",
				Labels: map[string]string{
					apps.LabelInstance:            "demo",
					"app.kubernetes.io/component": "worker",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api",
				Namespace: "default",
				Labels: map[string]string{
					apps.LabelInstance:            "demo",
					"app.kubernetes.io/component": "api",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "another-instance-worker",
				Namespace: "default",
				Labels: map[string]string{
					apps.LabelInstance:            "other",
					"app.kubernetes.io/component": "worker",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	).Build()
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
			Annotations: map[string]string{
				apps.AnnotationScalePodSelector: "app.kubernetes.io/component=worker",
			},
		},
	}

	if err := (&InstanceReconciler{Client: cli}).syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if instance.Status.Selector != "app.kubernetes.io/component=worker,app.kubernetes.io/instance=demo" {
		t.Fatalf("status selector = %q", instance.Status.Selector)
	}
	if instance.Status.Replicas != 1 {
		t.Fatalf("status replicas = %d, want 1", instance.Status.Replicas)
	}
}

func TestSyncStatusRejectsInvalidAdditionalScaleSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
			Annotations: map[string]string{
				apps.AnnotationScalePodSelector: "app.kubernetes.io/component in (",
			},
		},
		Status: appsv1.InstanceStatus{Replicas: 3, Selector: "stale=true"},
	}

	if err := (&InstanceReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}).syncStatus(t.Context(), instance); err != nil {
		t.Fatalf("syncStatus() error = %v", err)
	}
	if instance.Status.Selector != "" || instance.Status.Replicas != 0 {
		t.Fatalf("scale status = replicas %d, selector %q", instance.Status.Replicas, instance.Status.Selector)
	}
	condition := meta.FindStatusCondition(instance.Status.Conditions, "AutoscalingReady")
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != ReasonInvalidScalePodSelector {
		t.Fatalf("autoscaling condition = %#v", condition)
	}
}

func TestSyncStatusReportsScaledToZeroDeploymentAsHealthy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := k8sappsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	deployment := &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       k8sappsv1.DeploymentSpec{Replicas: &zero},
		Status:     k8sappsv1.DeploymentStatus{Replicas: 0, ReadyReplicas: 0},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment).Build()
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Status: appsv1.InstanceStatus{
			Values: appsv1.Values{Object: map[string]any{
				"global": map[string]any{"replicas": int32(0), "paused": false},
			}},
			Resources: []appsv1.ManagedResource{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Namespace:  "default",
				Name:       "web",
			}},
		},
	}

	if err := (&InstanceReconciler{Client: cli}).syncStatus(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if len(instance.Status.States) != 1 || instance.Status.States[0].Status != "ScaledToZero" {
		t.Fatalf("states = %#v, want ScaledToZero", instance.Status.States)
	}
	if instance.Status.Phase != appsv1.PhaseHealthy {
		t.Fatalf("phase = %q, want %q", instance.Status.Phase, appsv1.PhaseHealthy)
	}
	ready := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("ready condition = %#v", ready)
	}
}

func TestDefaultDeploymentStateReportsScalingBeforeDesiredReplicasExist(t *testing.T) {
	two := int32(2)
	deployment := &k8sappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       k8sappsv1.DeploymentSpec{Replicas: &two},
		Status:     k8sappsv1.DeploymentStatus{Replicas: 0, ReadyReplicas: 0},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(deployment)
	if err != nil {
		t.Fatal(err)
	}
	resource := &unstructured.Unstructured{Object: object}
	resource.SetAPIVersion("apps/v1")
	resource.SetKind("Deployment")

	states := GetDefaultStates([]*unstructured.Unstructured{resource})
	if len(states) != 1 || states[0].Status != "Scaling" {
		t.Fatalf("states = %#v, want Scaling", states)
	}
}

func TestComputeRuntimePhaseUsesStatesBeforeResourceKind(t *testing.T) {
	tests := []struct {
		name      string
		resources []appsv1.ManagedResource
		states    []appsv1.State
		want      appsv1.Phase
	}{
		{name: "no states is installed", resources: []appsv1.ManagedResource{{APIVersion: "apps/v1", Kind: "Deployment"}}, want: appsv1.PhaseInstalled},
		{name: "custom resource state is evaluated", resources: []appsv1.ManagedResource{{APIVersion: "example.io/v1", Kind: "Database"}}, states: []appsv1.State{{Name: "db", Status: apps.StateStatusRunning}}, want: appsv1.PhaseHealthy},
		{name: "mixed jobs and workloads are evaluated", resources: []appsv1.ManagedResource{{APIVersion: "batch/v1", Kind: "Job"}, {APIVersion: "apps/v1", Kind: "Deployment"}}, states: []appsv1.State{{Name: "web", Status: apps.StateStatusRunning}}, want: appsv1.PhaseHealthy},
		{name: "workload pending is degraded", resources: []appsv1.ManagedResource{{APIVersion: "apps/v1", Kind: "Deployment"}}, states: []appsv1.State{{Name: "web", Status: apps.StateStatusPending}}, want: appsv1.PhaseDegraded},
		{name: "unknown status is degraded", resources: []appsv1.ManagedResource{{APIVersion: "apps/v1", Kind: "Deployment"}}, states: []appsv1.State{{Name: "web", Status: "Starting"}}, want: appsv1.PhaseDegraded},
		{name: "unknown job status is degraded", resources: []appsv1.ManagedResource{{APIVersion: "batch/v1", Kind: "Job"}}, states: []appsv1.State{{Name: "job", Status: "Starting"}}, want: appsv1.PhaseDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, _, _ := computeRuntimePhase(tt.resources, tt.states)
			if phase != tt.want {
				t.Fatalf("phase = %q, want %q", phase, tt.want)
			}
		})
	}
}

func TestDetectInstanceWorkloadTypeTreatsMixedResourcesAsWorkload(t *testing.T) {
	resources := []appsv1.ManagedResource{
		{APIVersion: "batch/v1", Kind: "Job"},
		{APIVersion: "apps/v1", Kind: "Deployment"},
	}
	if got := detectInstanceWorkloadType(resources); got != InstanceWorkloadTypeWorkload {
		t.Fatalf("workload type = %q, want %q", got, InstanceWorkloadTypeWorkload)
	}
}

func TestDefaultEndpoints(t *testing.T) {
	className := "nginx"
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Name: className, Annotations: map[string]string{apps.AnnotationIngressPorts: "http:30080,https:30443"}},
	}).Build()
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &className,
			TLS:              []networkingv1.IngressTLS{{Hosts: []string{"secure.example.com"}}},
			Rules: []networkingv1.IngressRule{
				{Host: "secure.example.com", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{}}},
				{Host: "plain.example.com", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{}}},
			},
		},
	}
	got := getIngressEndpointsWithClient(t.Context(), cli, ingress)
	want := []appsv1.Endpoint{
		{Name: "web", URL: "https://secure.example.com:30443", Kind: appsv1.EndpointKindExternal},
		{Name: "web", URL: "http://plain.example.com:30080", Kind: appsv1.EndpointKindExternal},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress endpoints = %#v, want %#v", got, want)
	}

	appProtocol := "mysql"
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: "default"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "db.example.com", Ports: []corev1.ServicePort{
			{Name: "mysql", Port: 1234, AppProtocol: &appProtocol},
			{Name: "prometheus", Port: 9100},
		}},
	}
	serviceEndpoints := getServiceEndpoints(service)
	if len(serviceEndpoints) != 1 || serviceEndpoints[0].URL != "mysql://db.example.com:1234" || serviceEndpoints[0].Kind != appsv1.EndpointKindExternal {
		t.Fatalf("service endpoints = %#v", serviceEndpoints)
	}
}

func TestNodeIPEndpointExpansionAndSSHAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ready", Labels: map[string]string{apps.LabelExposeNodeIP: "true"}}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.2"}, {Type: corev1.NodeExternalIP, Address: "203.0.113.2"}},
		}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "not-ready", Labels: map[string]string{apps.LabelExposeNodeIP: "true"}}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.3"}},
		}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "unmarked"}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}},
		}},
	).Build()
	got := resolveNodeIPEndpoints(t.Context(), cli, []appsv1.Endpoint{{
		Name: "api", URL: "http://{NodeIP}:30080", Kind: appsv1.EndpointKindInternal,
	}})
	wantURLs := []string{"http://10.0.0.2:30080", "http://203.0.113.2:30080"}
	if !reflect.DeepEqual(got[0].URLs, wantURLs) {
		t.Fatalf("node URLs = %#v, want %#v", got[0].URLs, wantURLs)
	}
	if got[0].URL != "http://{NodeIP}:30080" {
		t.Fatalf("template URL changed to %q", got[0].URL)
	}

	access := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ssh.xiaoshiai.cn/v1",
		"kind":       "Access",
		"status": map[string]any{"endpoints": []any{
			map[string]any{"address": "ssh-b.example.com:22", "username": "demo"},
			map[string]any{"address": "ssh-a.example.com:22", "username": "demo"},
		}},
	}}
	ssh := getKubeSSHEndpoints(access)
	if len(ssh) != 1 || ssh[0].URL != "ssh://demo@ssh-a.example.com:22" || len(ssh[0].URLs) != 2 {
		t.Fatalf("SSH endpoints = %#v", ssh)
	}
}
