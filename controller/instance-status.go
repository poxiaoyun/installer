package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	k8sappsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

const (
	StateStatusPending  = "Pending"
	StateStatusDegraded = "Degraded"
	StateStatusUpdating = "Updating"
	StateStatusScaling  = "Scaling"

	StateStatusCrashLoopBackOff = "CrashLoopBackOff"
	StateStatusUnknown          = "Unknown"
	StateStatusFailed           = "Failed"
	StateStatusError            = "Error"

	StateStatusSucceeded = "Succeeded"
	StateStatusRunning   = "Running"
)

const (
	AnnotationEndpointsExpression = "app.kubernetes.io/endpoints-expression"
	AnnotationStatesExpression    = "app.kubernetes.io/states-expression"
)

const NodeIPPlaceholder = "{NodeIP}"

func (r *InstanceReconciler) syncStatus(ctx context.Context, instance *appsv1.Instance) error {
	resources := []*unstructured.Unstructured{}
	for _, ref := range instance.Status.Resources {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(ref.APIVersion)
		u.SetKind(ref.Kind)
		if err := r.Client.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, u); err != nil {
			// Resource might be deleted or not found, just skip it
			continue
		}
		resources = append(resources, u)
	}

	if err := checkAnnotations(ctx, instance, resources); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "check annotations failed")
	}

	paused := getmap(instance.Status.Values.Object, "global", "paused")
	if paused == true || paused == "true" {
		instance.Status.Phase = appsv1.PhasePaused
		instance.Status.Message = ""
		r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionFalse, "Paused", "Instance is paused")
	} else {
		var ready bool
		switch detectInstanceWorkloadType(instance.Status.Resources) {
		case InstanceWorkloadTypeJobOnly:
			instance.Status.Phase, ready, instance.Status.Message = computeJobPhase(instance.Status.States)
		case InstanceWorkloadTypeWorkload:
			instance.Status.Phase, ready, instance.Status.Message = computeWorkloadPhase(instance.Status.States)
		case InstanceWorkloadTypeConfig:
			instance.Status.Phase, ready, instance.Status.Message = appsv1.PhaseInstalled, true, ""
		}
		if ready {
			r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionTrue, "Ready", "Instance is ready")
		} else {
			r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionFalse, string(instance.Status.Phase), instance.Status.Message)
		}
	}

	return nil
}

func computeJobPhase(states []appsv1.State) (appsv1.Phase, bool, string) {
	hasFailed := false
	hasSucceeded := false
	hasRunning := false
	hasPending := false
	for _, s := range states {
		switch s.Status {
		case StateStatusFailed:
			hasFailed = true
		case StateStatusSucceeded:
			hasSucceeded = true
		case StateStatusRunning:
			hasRunning = true
		case StateStatusPending:
			hasPending = true
		}
	}
	allCompleted := !hasRunning && !hasPending
	if allCompleted {
		if hasFailed && hasSucceeded {
			return appsv1.PhasePartialFailed, false, getUnhealthyMessage(states)
		}
		if hasFailed {
			return appsv1.PhaseFailed, false, getUnhealthyMessage(states)
		}
		if hasSucceeded {
			return appsv1.PhaseSucceeded, true, ""
		}
	}
	if hasRunning {
		return appsv1.PhaseRunning, true, ""
	}
	return appsv1.PhasePending, true, ""
}

func computeWorkloadPhase(states []appsv1.State) (appsv1.Phase, bool, string) {
	hasFailed := false
	hasDegraded := false
	hasPending := false
	for _, s := range states {
		switch s.Status {
		case StateStatusFailed, StateStatusError, StateStatusCrashLoopBackOff:
			hasFailed = true
		case StateStatusDegraded, StateStatusUpdating, StateStatusScaling:
			hasDegraded = true
		case StateStatusPending:
			hasPending = true
		}
	}
	if hasFailed {
		return appsv1.PhaseFailed, false, getUnhealthyMessage(states)
	}
	if hasDegraded {
		return appsv1.PhaseDegraded, false, getUnhealthyMessage(states)
	}
	if hasPending {
		return appsv1.PhaseUnhealthy, false, getUnhealthyMessage(states)
	}
	return appsv1.PhaseHealthy, true, ""
}

func getmap(m map[string]any, keys ...string) any {
	if len(keys) == 0 {
		return m
	}
	if v, ok := m[keys[0]]; ok {
		if len(keys) == 1 {
			return v
		}
		if vm, ok := v.(map[string]any); ok {
			return getmap(vm, keys[1:]...)
		}
	}
	return nil
}

var JobKinds = []schema.GroupKind{
	{Group: "batch", Kind: "Job"},
	{Group: "batch", Kind: "CronJob"},
}

var WorkloadKinds = []schema.GroupKind{
	{Group: "apps", Kind: "Deployment"},
	{Group: "apps", Kind: "StatefulSet"},
	{Group: "apps", Kind: "DaemonSet"},
	{Group: "apps", Kind: "ReplicaSet"},
	{Group: "core", Kind: "Pod"},
}

type InstanceWorkloadType string

const (
	// JobOnly means the instance only has job workload
	InstanceWorkloadTypeJobOnly InstanceWorkloadType = "JobOnly"
	// Workload means the instance has workload (without job)
	InstanceWorkloadTypeWorkload InstanceWorkloadType = "Workload"
	// Config means the instance only has config (without workload and job)
	InstanceWorkloadTypeConfig InstanceWorkloadType = "Config"
)

func detectInstanceWorkloadType(resources []appsv1.ManagedResource) InstanceWorkloadType {
	if len(resources) == 0 {
		return InstanceWorkloadTypeConfig
	}
	hasJob := false
	hasWorkload := false
	for _, ref := range resources {
		if slices.Contains(JobKinds, ref.GroupVersionKind().GroupKind()) {
			hasJob = true
			continue
		}
		if slices.Contains(WorkloadKinds, ref.GroupVersionKind().GroupKind()) {
			hasWorkload = true
			continue
		}
	}
	if hasJob && !hasWorkload {
		return InstanceWorkloadTypeJobOnly
	}
	if !hasJob && hasWorkload {
		return InstanceWorkloadTypeWorkload
	}
	return InstanceWorkloadTypeConfig
}

func getUnhealthyMessage(states []appsv1.State) string {
	var messages []string
	for _, s := range states {
		if !isStateHealthy(s.Status) && s.Message != "" {
			messages = append(messages, s.Message)
		}
	}
	return strings.Join(messages, "\n")
}

func isStateHealthy(status string) bool {
	switch status {
	case "Running", "Healthy", "Active", "Succeeded":
		return true
	}
	return false
}

// GetDefaultStates returns default workload states for resources
func GetDefaultStates(resources []*unstructured.Unstructured) []appsv1.State {
	var states []appsv1.State
	for _, resource := range resources {
		var state appsv1.State
		switch resource.GroupVersionKind().GroupKind() {
		case schema.GroupKind{Group: "batch", Kind: "Job"}:
			state = getJobState(resource)
		case schema.GroupKind{Group: "apps", Kind: "Deployment"}:
			state = getDeploymentState(resource)
		case schema.GroupKind{Group: "apps", Kind: "StatefulSet"}:
			state = getStatefulSetState(resource)
		case schema.GroupKind{Group: "apps", Kind: "DaemonSet"}:
			state = getDaemonSetState(resource)
		case schema.GroupKind{Group: "core", Kind: "Pod"}:
			state = getPodState(resource)
		default:
			continue
		}
		states = append(states, state)
	}
	return states
}

func getJobState(resource *unstructured.Unstructured) appsv1.State {
	job := &batchv1.Job{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, job); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: job.Name, Kind: "Job"}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			state.Status = StateStatusSucceeded
			return state
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			state.Status = StateStatusFailed
			state.Message = c.Message
			return state
		}
	}
	state.Status = StateStatusRunning
	return state
}

func getDeploymentState(resource *unstructured.Unstructured) appsv1.State {
	deployment := &k8sappsv1.Deployment{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, deployment); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: deployment.Name, Kind: "Deployment"}
	if deployment.Status.ReadyReplicas == deployment.Status.Replicas {
		state.Status = StateStatusRunning
	} else {
		state.Status = StateStatusDegraded
	}
	return state
}

func getStatefulSetState(resource *unstructured.Unstructured) appsv1.State {
	statefulset := &k8sappsv1.StatefulSet{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, statefulset); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: statefulset.Name, Kind: "StatefulSet"}
	if statefulset.Status.ReadyReplicas == statefulset.Status.Replicas {
		state.Status = StateStatusRunning
	} else {
		state.Status = StateStatusDegraded
	}
	return state
}

func getDaemonSetState(resource *unstructured.Unstructured) appsv1.State {
	daemonset := &k8sappsv1.DaemonSet{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, daemonset); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: daemonset.Name, Kind: "DaemonSet"}
	if daemonset.Status.NumberReady == daemonset.Status.DesiredNumberScheduled {
		state.Status = StateStatusRunning
	} else {
		state.Status = StateStatusDegraded
	}
	return state
}

func getPodState(resource *unstructured.Unstructured) appsv1.State {
	pod := &corev1.Pod{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, pod); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: pod.Name, Kind: "Pod"}
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		state.Status = StateStatusSucceeded
	case corev1.PodFailed:
		state.Status = StateStatusFailed
		state.Message = pod.Status.Message
	case corev1.PodRunning:
		state.Status = StateStatusRunning
	default:
		state.Status = StateStatusPending
	}
	return state
}

func GetDefaultEndpoints(resources []*unstructured.Unstructured) []appsv1.Endpoint {
	endpoints := []appsv1.Endpoint{}
	for _, resource := range resources {
		if resource.GetKind() == "Ingress" && resource.GetAPIVersion() == networkingv1.SchemeGroupVersion.String() {
			ingress := &networkingv1.Ingress{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, ingress); err != nil {
				continue
			}
			endpoints = append(endpoints, getIngressEndpoints(ingress)...)
			continue
		}
		if resource.GetKind() == "Service" && resource.GetAPIVersion() == corev1.SchemeGroupVersion.String() {
			svc := &corev1.Service{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, svc); err != nil {
				continue
			}
			endpoints = append(endpoints, getServiceEndpoints(svc)...)
			continue
		}
	}
	return endpoints
}

func getIngressEndpoints(ingress *networkingv1.Ingress) []appsv1.Endpoint {
	// Simple ingress endpoint extraction
	var endpoints []appsv1.Endpoint
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" {
			continue
		}
		scheme := "http"
		if len(ingress.Spec.TLS) > 0 {
			scheme = "https"
		}
		endpoint := appsv1.Endpoint{
			Name: ingress.Name,
			URL:  fmt.Sprintf("%s://%s", scheme, rule.Host),
			Kind: appsv1.EndpointKindExternal, // Usually ingress is external
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func getServiceEndpoints(svc *corev1.Service) []appsv1.Endpoint {
	if len(svc.Spec.Ports) == 0 {
		return nil
	}
	var endpoints []appsv1.Endpoint
	for _, svcport := range svc.Spec.Ports {
		// skip metrics
		if svcport.Port == 9000 || svcport.Port == 9090 {
			continue
		}
		name := svc.GetName()
		if svcport.Name != "" {
			name += "-" + svcport.Name
		}
		scheme := PortProtocolFromServicePort(svcport)
		port := svcport.Port

		switch svc.Spec.Type {
		case corev1.ServiceTypeLoadBalancer:
			for _, ingress := range svc.Status.LoadBalancer.Ingress {
				host := ingress.IP
				if ingress.Hostname != "" {
					host = ingress.Hostname
				}
				endpoint := appsv1.Endpoint{
					Name: name,
					URL:  fmt.Sprintf("%s://%s:%d", scheme, host, port),
					Kind: appsv1.EndpointKindExternal,
				}
				endpoints = append(endpoints, endpoint)
			}
		case corev1.ServiceTypeNodePort:
			endpoint := appsv1.Endpoint{
				Name: name,
				Kind: appsv1.EndpointKindInternal, // NodePort is often internal/cluster-wide
				URL:  fmt.Sprintf("%s://%s:%d", scheme, NodeIPPlaceholder, svcport.NodePort),
			}
			endpoints = append(endpoints, endpoint)
		case corev1.ServiceTypeClusterIP:
			endpoint := appsv1.Endpoint{
				Name: name,
				Kind: appsv1.EndpointKindCluster,
				URL:  fmt.Sprintf("%s://%s.%s:%d", scheme, svc.Name, svc.Namespace, port),
			}
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func PortProtocolFromServicePort(port corev1.ServicePort) string {
	if port.AppProtocol != nil && *port.AppProtocol != "" {
		return strings.ToLower(*port.AppProtocol)
	}
	if port.Name != "" {
		if strings.Contains(port.Name, "https") {
			return "https"
		}
		if strings.Contains(port.Name, "http") {
			return "http"
		}
	}
	switch port.Port {
	case 80, 8080:
		return "http"
	case 443:
		return "https"
	}
	return "tcp"
}

func checkAnnotations(ctx context.Context, instance *appsv1.Instance, resources []*unstructured.Unstructured) error {
	annotations := instance.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	resourceslist := make([]map[string]any, len(resources))
	for idx, resource := range resources {
		resourceslist[idx] = resource.Object
	}
	instancedata, err := runtime.DefaultUnstructuredConverter.ToUnstructured(instance)
	if err != nil {
		return err
	}
	// Convert instance.Values to map[string]any for CEL
	// Use instance.Values (spec) as it contains the user-configured values
	var valuesdata map[string]any
	if instance.Status.Values.Object != nil {
		valuesdata = instance.Status.Values.Object
	}
	celdata := CELData{
		Instance:  instancedata,
		Resources: resourceslist,
		Values:    valuesdata,
	}

	if endpointsexpression := annotations[AnnotationEndpointsExpression]; endpointsexpression != "" {
		instance.Status.Endpoints = checkEndpoints(ctx, endpointsexpression, celdata)
	} else {
		// default endpoints
		instance.Status.Endpoints = GetDefaultEndpoints(resources)
	}

	if statusexpression := annotations[AnnotationStatesExpression]; statusexpression != "" {
		instance.Status.States = checkStates(ctx, statusexpression, celdata)
	} else {
		// default workload state detection
		instance.Status.States = GetDefaultStates(resources)
	}

	return nil
}

func checkStates(ctx context.Context, expr string, data CELData) []appsv1.State {
	log := logr.FromContextOrDiscard(ctx)
	result, err := EvalCELExpression(expr, data)
	if err != nil {
		log.Error(err, "evaluate status expression failed", "expression", expr)
		return nil
	}
	states := []appsv1.State{}
	if list, ok := result.([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				state := appsv1.State{}
				if name, ok := m["name"].(string); ok {
					state.Name = name
				}
				if status, ok := m["status"].(string); ok {
					state.Status = status
				}
				if message, ok := m["message"].(string); ok {
					state.Message = message
				}
				states = append(states, state)
			}
		}
		return states
	}
	log.Error(fmt.Errorf("expression result is not list"), "evaluate status expression failed", "expression", expr, "result", result)
	return nil
}

func checkEndpoints(ctx context.Context, expr string, data CELData) []appsv1.Endpoint {
	log := logr.FromContextOrDiscard(ctx)
	result, err := EvalCELExpression(expr, data)
	if err != nil {
		log.Error(err, "evaluate endpoints expression failed", "expression", expr)
		return nil
	}
	endpoints := []appsv1.Endpoint{}
	if list, ok := result.([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				endpoint := appsv1.Endpoint{}
				if name, ok := m["name"].(string); ok {
					endpoint.Name = name
				}
				if url, ok := m["url"].(string); ok {
					endpoint.URL = url
				}
				if endpoint.URL != "" {
					endpoints = append(endpoints, endpoint)
				}
			}
		}
		return endpoints
	}
	log.Error(fmt.Errorf("expression result is not list"), "evaluate endpoints expression failed", "expression", expr, "result", result)
	return nil
}
