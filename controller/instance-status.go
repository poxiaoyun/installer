package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
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
	"xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

const (
	StateStatusDegraded = "Degraded"
	StateStatusUpdating = "Updating"
	StateStatusScaling  = "Scaling"

	StateStatusPaused  = "Paused"
	StateStatusUnknown = "Unknown"

	StateStatusPending          = "Pending"
	StateStatusCrashLoopBackOff = "CrashLoopBackOff"
	StateStatusFailed           = "Failed"
	StateStatusUnhealthy        = "Unhealthy"
	StateStatusError            = "Error"

	StateStatusSucceeded = "Succeeded"
	StateStatusActive    = "Active"
	StateStatusHealthy   = "Healthy"
	StateCompleted       = "Completed"
	StateStatusRunning   = "Running"
)

const (
	AnnotationIngressPorts = "cloud.xiaoshiai.cn/ingress-ports"
	LabelExposeNodeIP      = "cloud.xiaoshiai.cn/expose-node-ip"
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

	expressionErr := r.checkAnnotations(ctx, instance, resources)
	if expressionErr != nil {
		logr.FromContextOrDiscard(ctx).Error(expressionErr, "check annotations failed")
	}

	paused := getmap(instance.Status.Values.Object, "global", "paused")
	if paused == true || paused == "true" {
		instance.Status.Phase = appsv1.PhasePaused
		instance.Status.Message = ""
		r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionFalse, "Paused", "Instance is paused")
	} else {
		var ready bool
		instance.Status.Phase, ready, instance.Status.Message = computeRuntimePhase(instance.Status.Resources, instance.Status.States)
		if ready {
			r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionTrue, "Ready", "Instance is ready")
		} else {
			r.setCondition(instance, appsv1.ConditionReady, metav1.ConditionFalse, string(instance.Status.Phase), instance.Status.Message)
		}
	}

	return nil
}

func computeRuntimePhase(resources []appsv1.ManagedResource, states []appsv1.State) (appsv1.Phase, bool, string) {
	if len(states) == 0 {
		return appsv1.PhaseInstalled, true, ""
	}
	switch detectInstanceWorkloadType(resources) {
	case InstanceWorkloadTypeJobOnly:
		return computeJobPhase(states)
	case InstanceWorkloadTypeWorkload, InstanceWorkloadTypeConfig:
		// Config includes CustomResources whose runtime states are supplied by
		// an expression. Once states exist, they use workload phase semantics.
		return computeWorkloadPhase(states)
	default:
		// Keep custom states fail-closed if another workload type is introduced
		// without defining dedicated phase semantics.
		return computeWorkloadPhase(states)
	}
}

func computeJobPhase(states []appsv1.State) (appsv1.Phase, bool, string) {
	hasFailed := false
	hasSucceeded := false
	hasRunning := false
	hasPending := false
	hasUnknown := false
	for _, s := range states {
		switch s.Status {
		case StateStatusFailed,
			StateStatusError,
			StateStatusCrashLoopBackOff,
			StateStatusUnhealthy:
			hasFailed = true
		case StateStatusSucceeded,
			StateCompleted:
			hasSucceeded = true
		case StateStatusRunning,
			StateStatusHealthy,
			StateStatusActive:
			hasRunning = true
		case StateStatusPending:
			hasPending = true
		default:
			hasUnknown = true
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
	if hasUnknown {
		return appsv1.PhaseDegraded, false, getUnhealthyMessage(states)
	}
	if hasRunning {
		return appsv1.PhaseRunning, true, ""
	}
	return appsv1.PhasePending, true, ""
}

func computeWorkloadPhase(states []appsv1.State) (appsv1.Phase, bool, string) {
	hasUnhealthy := false
	hasDegraded := false
	for _, s := range states {
		switch s.Status {
		case StateStatusFailed,
			StateStatusError,
			StateStatusCrashLoopBackOff,
			StateStatusUnhealthy:
			hasUnhealthy = true
		case StateStatusDegraded,
			StateStatusUpdating,
			StateStatusScaling,
			StateStatusPending,
			StateStatusPaused,
			StateStatusUnknown:
			hasDegraded = true
		case StateStatusRunning,
			StateStatusHealthy,
			StateStatusActive,
			StateStatusSucceeded,
			StateCompleted:
			// Explicitly healthy.
		default:
			// Preserve custom status strings for display, but never infer Healthy
			// from a status the installer does not understand.
			hasDegraded = true
		}
	}
	if hasUnhealthy {
		return appsv1.PhaseUnhealthy, false, getUnhealthyMessage(states)
	}
	if hasDegraded {
		return appsv1.PhaseDegraded, false, getUnhealthyMessage(states)
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
	// Workload means the instance has a long-running workload, possibly with jobs.
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
	if hasWorkload {
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
	case StateStatusRunning, StateStatusHealthy, StateStatusActive, StateStatusSucceeded, StateCompleted:
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
		case schema.GroupKind{Group: apps.GroupName, Kind: "Instance"}:
			state = getInstanceState(resource)
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
		if c.Type == batchv1.JobSuspended && c.Status == corev1.ConditionTrue {
			state.Status = StateStatusPaused
			return state
		}
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

func getInstanceState(resource *unstructured.Unstructured) appsv1.State {
	instance := &appsv1.Instance{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, instance); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{Name: instance.Name, Kind: "Instance"}
	state.Status = string(instance.Status.Phase)
	return state
}

func getDeploymentState(resource *unstructured.Unstructured) appsv1.State {
	deployment := &k8sappsv1.Deployment{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, deployment); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{
		Name:   deployment.Name,
		Kind:   "Deployment",
		Status: calcReplicasState(deployment.Status.Replicas, deployment.Status.ReadyReplicas),
	}
	messages := []string{}
	for _, c := range deployment.Status.Conditions {
		if c.Type == k8sappsv1.DeploymentAvailable && c.Status == corev1.ConditionFalse {
			messages = append(messages, c.Message)
		}
		if c.Type == k8sappsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
			messages = append(messages, c.Message)
		}
	}
	if len(messages) > 0 {
		state.Message = strings.Join(messages, "\n")
	}
	return state
}

func getStatefulSetState(resource *unstructured.Unstructured) appsv1.State {
	statefulset := &k8sappsv1.StatefulSet{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, statefulset); err != nil {
		return appsv1.State{}
	}
	state := appsv1.State{
		Name:   statefulset.Name,
		Kind:   "StatefulSet",
		Status: calcReplicasState(statefulset.Status.Replicas, statefulset.Status.ReadyReplicas),
	}
	return state
}

func calcReplicasState(desired int32, ready int32) string {
	if desired == 0 {
		return StateStatusPaused
	}
	if ready == desired {
		return StateStatusRunning
	}
	if ready == 0 {
		return StateStatusUnhealthy
	}
	return StateStatusDegraded
}

func getDaemonSetState(resource *unstructured.Unstructured) appsv1.State {
	daemonset := &k8sappsv1.DaemonSet{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, daemonset); err != nil {
		return appsv1.State{}
	}
	return appsv1.State{
		Name:   daemonset.Name,
		Kind:   "DaemonSet",
		Status: calcReplicasState(daemonset.Status.DesiredNumberScheduled, daemonset.Status.NumberReady),
	}
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

// GetDefaultEndpoints extracts endpoints from managed resources and uses the
// Kubernetes client for cluster-backed metadata such as IngressClass settings.
func GetDefaultEndpoints(ctx context.Context, cli client.Client, resources []*unstructured.Unstructured) []appsv1.Endpoint {
	endpoints := []appsv1.Endpoint{}
	for _, resource := range resources {
		if resource.GetKind() == "Access" && resource.GetAPIVersion() == "ssh.xiaoshiai.cn/v1" {
			endpoints = append(endpoints, getKubeSSHEndpoints(resource)...)
			continue
		}
		if resource.GetKind() == "Ingress" && resource.GetAPIVersion() == networkingv1.SchemeGroupVersion.String() {
			ingress := &networkingv1.Ingress{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, ingress); err != nil {
				continue
			}
			endpoints = append(endpoints, getIngressEndpointsWithClient(ctx, cli, ingress)...)
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
	return normalizeEndpoints(endpoints)
}

func getKubeSSHEndpoints(access *unstructured.Unstructured) []appsv1.Endpoint {
	addresses, found, err := unstructured.NestedSlice(access.Object, "status", "endpoints")
	if err != nil || !found {
		return nil
	}
	urls := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, item := range addresses {
		endpoint, ok := item.(map[string]any)
		if !ok {
			continue
		}
		address, _ := endpoint["address"].(string)
		username, _ := endpoint["username"].(string)
		address, username = strings.TrimSpace(address), strings.TrimSpace(username)
		if address == "" || username == "" {
			continue
		}
		endpointURL := "ssh://" + username + "@" + address
		if _, ok := seen[endpointURL]; ok {
			continue
		}
		seen[endpointURL] = struct{}{}
		urls = append(urls, endpointURL)
	}
	if len(urls) == 0 {
		return nil
	}
	sort.Strings(urls)
	return []appsv1.Endpoint{{Name: "SSH", URL: urls[0], URLs: urls, Kind: appsv1.EndpointKindExternal}}
}

func getIngressEndpointsWithClient(ctx context.Context, cli client.Client, ingress *networkingv1.Ingress) []appsv1.Endpoint {
	ports := map[string]int32{}
	if cli != nil && ingress.Spec.IngressClassName != nil {
		ingressClass := &networkingv1.IngressClass{}
		if err := client.IgnoreNotFound(cli.Get(ctx, client.ObjectKey{Name: *ingress.Spec.IngressClassName}, ingressClass)); err != nil {
			logr.FromContextOrDiscard(ctx).Error(err, "get ingress class for endpoints", "name", *ingress.Spec.IngressClassName)
		} else {
			ports = parseIngressPorts(ingressClass.Annotations[AnnotationIngressPorts])
		}
	}
	tlsHosts := map[string]struct{}{}
	for _, tls := range ingress.Spec.TLS {
		for _, host := range tls.Hosts {
			tlsHosts[host] = struct{}{}
		}
	}
	var endpoints []appsv1.Endpoint
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" || rule.HTTP == nil {
			continue
		}
		scheme := "http"
		if _, ok := tlsHosts[rule.Host]; ok {
			scheme = "https"
		}
		host := rule.Host
		if port, ok := ports[scheme]; ok {
			host = fmt.Sprintf("%s:%d", host, port)
		}
		endpoint := appsv1.Endpoint{
			Name: ingress.Name,
			URL:  fmt.Sprintf("%s://%s", scheme, host),
			Kind: appsv1.EndpointKindExternal,
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func parseIngressPorts(value string) map[string]int32 {
	ports := map[string]int32{}
	for item := range strings.SplitSeq(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) != 2 {
			continue
		}
		port, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
		if err == nil && port > 0 && port <= 65535 {
			ports[strings.ToLower(strings.TrimSpace(parts[0]))] = int32(port)
		}
	}
	return ports
}

func getServiceEndpoints(svc *corev1.Service) []appsv1.Endpoint {
	if len(svc.Spec.Ports) == 0 {
		return nil
	}
	var endpoints []appsv1.Endpoint
	for _, svcport := range svc.Spec.Ports {
		portName := strings.ToLower(svcport.Name)
		if svcport.Port == 9000 || svcport.Port == 9090 || strings.Contains(portName, "metrics") || strings.Contains(portName, "prometheus") {
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
				endpointPort := port
				host := ingress.IP
				if ingress.Hostname != "" {
					host = ingress.Hostname
				}
				if lbPort := findLoadBalancerPort(ingress.Ports, svcport.Port); lbPort != nil {
					endpointPort = lbPort.Port
				}
				if host == "" {
					continue
				}
				endpoint := appsv1.Endpoint{
					Name: name,
					URL:  fmt.Sprintf("%s://%s:%d", scheme, host, endpointPort),
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
		case corev1.ServiceTypeClusterIP, "":
			endpoint := appsv1.Endpoint{
				Name: name,
				Kind: appsv1.EndpointKindCluster,
				URL:  fmt.Sprintf("%s://%s.%s:%d", scheme, svc.Name, svc.Namespace, port),
			}
			endpoints = append(endpoints, endpoint)
		case corev1.ServiceTypeExternalName:
			if svc.Spec.ExternalName == "" {
				continue
			}
			endpoints = append(endpoints, appsv1.Endpoint{
				Name: name,
				Kind: appsv1.EndpointKindExternal,
				URL:  fmt.Sprintf("%s://%s:%d", scheme, svc.Spec.ExternalName, port),
			})
		}
	}
	return endpoints
}

func findLoadBalancerPort(ports []corev1.PortStatus, targetPort int32) *corev1.PortStatus {
	for idx := range ports {
		if ports[idx].Port == targetPort {
			return &ports[idx]
		}
	}
	return nil
}

func PortProtocolFromServicePort(port corev1.ServicePort) string {
	if port.Protocol != "" && port.Protocol != corev1.ProtocolTCP {
		return strings.ToLower(string(port.Protocol))
	}
	if port.AppProtocol != nil && *port.AppProtocol != "" {
		return strings.ToLower(*port.AppProtocol)
	}
	if port.Name != "" {
		name := strings.ToLower(port.Name)
		if strings.Contains(name, "https") {
			return "https"
		}
		if strings.Contains(name, "http") {
			return "http"
		}
		if strings.Contains(name, "ssh") {
			return "ssh"
		}
	}
	switch port.Port {
	case 80, 8080:
		return "http"
	case 443:
		return "https"
	case 22:
		return "ssh"
	case 21:
		return "ftp"
	case 25:
		return "smtp"
	case 110:
		return "pop3"
	case 143:
		return "imap"
	case 3306:
		return "mysql"
	case 5432:
		return "postgresql"
	case 6379:
		return "redis"
	case 27017:
		return "mongodb"
	case 2379:
		return "etcd"
	}
	return "tcp"
}

func (r *InstanceReconciler) checkAnnotations(ctx context.Context, instance *appsv1.Instance, resources []*unstructured.Unstructured) error {
	annotations := instance.GetAnnotations()
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

	var expressionErrors []error
	var endpoints []appsv1.Endpoint
	if endpointsexpression := annotations[appsv1.AnnotationEndpointsExpression]; endpointsexpression != "" {
		var err error
		endpoints, err = checkEndpoints(endpointsexpression, celdata)
		if err != nil {
			expressionErrors = append(expressionErrors, fmt.Errorf("endpoints expression: %w", err))
			endpoints = nil
		}
	} else {
		endpoints = GetDefaultEndpoints(ctx, r.Client, resources)
	}
	if expression := annotations[appsv1.AnnotationAdditionalEndpointsExpression]; expression != "" {
		additional, err := checkEndpoints(expression, celdata)
		if err != nil {
			expressionErrors = append(expressionErrors, fmt.Errorf("additional endpoints expression: %w", err))
		} else {
			endpoints = append(endpoints, additional...)
		}
	}
	// Preserve expression order and keep additional endpoints after the base
	// result while removing exact duplicates. Default-discovered endpoints are
	// already sorted by GetDefaultEndpoints.
	instance.Status.Endpoints = resolveNodeIPEndpoints(ctx, r.Client, dedupeEndpoints(endpoints))

	if statusexpression := annotations[appsv1.AnnotationStatesExpression]; statusexpression != "" {
		states, err := checkStates(statusexpression, celdata)
		if err != nil {
			expressionErrors = append(expressionErrors, fmt.Errorf("states expression: %w", err))
			instance.Status.States = nil
		} else {
			instance.Status.States = states
		}
	} else {
		instance.Status.States = GetDefaultStates(resources)
	}

	if summaryexpression := annotations[appsv1.AnnotationSummaryExpression]; summaryexpression != "" {
		summary, err := checkSummary(summaryexpression, celdata)
		if err != nil {
			expressionErrors = append(expressionErrors, fmt.Errorf("summary expression: %w", err))
			instance.Status.Summary = nil
		} else {
			instance.Status.Summary = summary
		}
	} else {
		instance.Status.Summary = nil
	}

	if err := errors.Join(expressionErrors...); err != nil {
		r.setCondition(instance, appsv1.ConditionExpressionsReady, metav1.ConditionFalse, "ExpressionEvaluationFailed", err.Error())
		return err
	}
	r.setCondition(instance, appsv1.ConditionExpressionsReady, metav1.ConditionTrue, "ExpressionsReady", "Configured expressions evaluated successfully")
	return nil
}

func checkStates(expr string, data CELData) ([]appsv1.State, error) {
	result, err := EvalCELExpression(expr, data)
	if err != nil {
		return nil, err
	}
	list, ok := result.([]any)
	if !ok {
		return nil, nil
	}
	states := make([]appsv1.State, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		state := appsv1.State{}
		if value, ok := m["name"].(string); ok {
			state.Name = value
		}
		if value, ok := m["kind"].(string); ok {
			state.Kind = value
		}
		if value, ok := m["status"].(string); ok {
			state.Status = value
		}
		if value, ok := m["message"].(string); ok {
			state.Message = value
		}
		states = append(states, state)
	}
	return states, nil
}

func checkEndpoints(expr string, data CELData) ([]appsv1.Endpoint, error) {
	result, err := EvalCELExpression(expr, data)
	if err != nil {
		return nil, err
	}
	list, ok := result.([]any)
	if !ok {
		return nil, nil
	}
	endpoints := make([]appsv1.Endpoint, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		endpoint := appsv1.Endpoint{}
		if value, ok := m["name"].(string); ok {
			endpoint.Name = value
		}
		if value, ok := m["url"].(string); ok {
			endpoint.URL = value
		}
		if value, ok := m["kind"].(string); ok {
			endpoint.Kind = appsv1.EndpointKind(value)
		}
		if value, ok := m["relation"].(string); ok {
			endpoint.Relation = appsv1.EndpointRelation(value)
		}
		if values, ok := m["urls"].([]any); ok {
			for _, value := range values {
				if endpointURL, ok := value.(string); ok {
					endpoint.URLs = append(endpoint.URLs, endpointURL)
				}
			}
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

func checkSummary(expr string, data CELData) (map[string]string, error) {
	result, err := EvalCELExpression(expr, data)
	if err != nil {
		return nil, err
	}
	m, ok := result.(map[string]any)
	if !ok {
		return nil, nil
	}
	summary := make(map[string]string, len(m))
	for key, value := range m {
		if stringValue, ok := value.(string); ok {
			summary[key] = stringValue
		}
	}
	return summary, nil
}

func normalizeEndpoints(endpoints []appsv1.Endpoint) []appsv1.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}
	sort.SliceStable(endpoints, func(i, j int) bool {
		a, b := endpoints[i], endpoints[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Relation < b.Relation
	})
	return dedupeEndpoints(endpoints)
}

func dedupeEndpoints(endpoints []appsv1.Endpoint) []appsv1.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}
	type endpointKey struct {
		name     string
		url      string
		kind     appsv1.EndpointKind
		relation appsv1.EndpointRelation
	}
	seen := make(map[endpointKey]struct{}, len(endpoints))
	result := endpoints[:0]
	for _, endpoint := range endpoints {
		key := endpointKey{endpoint.Name, endpoint.URL, endpoint.Kind, endpoint.Relation}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, endpoint)
	}
	return result
}

func resolveNodeIPEndpoints(ctx context.Context, cli client.Client, endpoints []appsv1.Endpoint) []appsv1.Endpoint {
	needsNodeIPs := false
	for _, endpoint := range endpoints {
		if strings.Contains(endpoint.URL, NodeIPPlaceholder) {
			needsNodeIPs = true
			break
		}
	}
	if !needsNodeIPs || cli == nil {
		return endpoints
	}
	nodes := &corev1.NodeList{}
	if err := cli.List(ctx, nodes, client.MatchingLabels{LabelExposeNodeIP: "true"}); err != nil {
		logr.FromContextOrDiscard(ctx).Error(err, "list nodes to resolve endpoint URLs")
		return endpoints
	}
	addresses := map[string]struct{}{}
	for _, node := range nodes.Items {
		if !nodeReady(&node) {
			continue
		}
		for _, address := range node.Status.Addresses {
			if (address.Type == corev1.NodeInternalIP || address.Type == corev1.NodeExternalIP) && address.Address != "" {
				addresses[address.Address] = struct{}{}
			}
		}
	}
	addressList := make([]string, 0, len(addresses))
	for address := range addresses {
		addressList = append(addressList, address)
	}
	sort.Strings(addressList)
	for idx := range endpoints {
		if !strings.Contains(endpoints[idx].URL, NodeIPPlaceholder) {
			continue
		}
		endpoints[idx].URLs = make([]string, 0, len(addressList))
		for _, address := range addressList {
			replacement := address
			if ip := net.ParseIP(address); ip != nil && strings.Contains(address, ":") {
				replacement = "[" + address + "]"
			}
			endpoints[idx].URLs = append(endpoints[idx].URLs, strings.ReplaceAll(endpoints[idx].URL, NodeIPPlaceholder, replacement))
		}
		if len(endpoints[idx].URLs) == 0 {
			endpoints[idx].URLs = nil
		}
	}
	return endpoints
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
