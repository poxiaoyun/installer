package controller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

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
				appsv1.AnnotationSummaryExpression:             `{"source":"instance"}`,
				appsv1.AnnotationEndpointsExpression:           `[{"name":"z-base","url":"https://base.example.com","kind":"External"}]`,
				appsv1.AnnotationAdditionalEndpointsExpression: `[{"name":"a-dependency","url":"redis://redis.default:6379","kind":"Cluster","relation":"ReadsFrom"}]`,
			},
		},
	}
	r := &InstanceReconciler{}
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

	instance.Annotations[appsv1.AnnotationSummaryExpression] = `{`
	instance.Status.Summary = map[string]string{"stale": "value"}
	if err := r.checkAnnotations(t.Context(), instance, nil); err == nil {
		t.Fatal("expected invalid summary expression result")
	}
	if instance.Status.Summary != nil {
		t.Fatalf("stale summary was retained: %#v", instance.Status.Summary)
	}
	condition := meta.FindStatusCondition(instance.Status.Conditions, appsv1.ConditionExpressionsReady)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "ExpressionEvaluationFailed" {
		t.Fatalf("condition = %#v", condition)
	}
}

func TestSyncStatusKeepsExpressionFailureSeparateFromRuntimePhase(t *testing.T) {
	instance := &appsv1.Instance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "default",
			Annotations: map[string]string{
				appsv1.AnnotationStatesExpression: `[`,
			},
		},
	}
	r := &InstanceReconciler{}
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
	if expressionsReady == nil || expressionsReady.Status != metav1.ConditionFalse || expressionsReady.Reason != "ExpressionEvaluationFailed" {
		t.Fatalf("expressions ready condition = %#v", expressionsReady)
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
		{name: "custom resource state is evaluated", resources: []appsv1.ManagedResource{{APIVersion: "example.io/v1", Kind: "Database"}}, states: []appsv1.State{{Name: "db", Status: StateStatusRunning}}, want: appsv1.PhaseHealthy},
		{name: "mixed jobs and workloads are evaluated", resources: []appsv1.ManagedResource{{APIVersion: "batch/v1", Kind: "Job"}, {APIVersion: "apps/v1", Kind: "Deployment"}}, states: []appsv1.State{{Name: "web", Status: StateStatusRunning}}, want: appsv1.PhaseHealthy},
		{name: "workload pending is degraded", resources: []appsv1.ManagedResource{{APIVersion: "apps/v1", Kind: "Deployment"}}, states: []appsv1.State{{Name: "web", Status: StateStatusPending}}, want: appsv1.PhaseDegraded},
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
		ObjectMeta: metav1.ObjectMeta{Name: className, Annotations: map[string]string{AnnotationIngressPorts: "http:30080,https:30443"}},
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
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "ready", Labels: map[string]string{LabelExposeNodeIP: "true"}}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.2"}, {Type: corev1.NodeExternalIP, Address: "203.0.113.2"}},
		}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "not-ready", Labels: map[string]string{LabelExposeNodeIP: "true"}}, Status: corev1.NodeStatus{
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
