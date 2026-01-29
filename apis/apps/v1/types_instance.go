package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".status.version",description="Chart version"
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase",description="Current phase"
// +kubebuilder:printcolumn:name="APPVERSION",type="string",JSONPath=".status.appVersion",description="App version",priority=1
// +kubebuilder:printcolumn:name="UPDATE",type="date",JSONPath=".status.upgradeTimestamp",description="Last upgrade"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp",description="Creation time"
type Instance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstanceSpec   `json:"spec,omitempty"`
	Status InstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type InstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Instance `json:"items"`
}

type InstanceSpec struct {
	// Kind instance kind.
	// +kubebuilder:default=helm
	Kind InstanceKind `json:"kind,omitempty"`

	// URL is the URL of helm repository, git clone url, tarball url, s3 url, etc.
	// +kubebuilder:validation:Required
	URL string `json:"url,omitempty"`

	// Version is the version of helm chart, git revision, etc.
	Version string `json:"version,omitempty"`

	// Chart is the name of the chart to install.
	Chart string `json:"chart,omitempty"`

	// Path is the path in a tarball to the chart/kustomize.
	Path string `json:"path,omitempty"`

	// Dependencies is a list of instances that this instance depends on.
	// The instance will be installed after all dependencies are exists.
	Dependencies []corev1.ObjectReference `json:"dependencies,omitempty"`

	// Values is a nested map of helm values.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Optional
	Values Values `json:"values,omitempty"`

	// ValuesFiles is a list of references to helm values files.
	// Ref can be a configmap or secret.
	// +kubebuilder:validation:Optional
	ValuesFrom []ValuesFrom `json:"valuesFrom,omitempty"`

	// Options is a list of options to pass to the instance.
	// if passed to helm or other deployer.
	// +kubebuilder:validation:Optional
	Options []Option `json:"options,omitempty"`

	// Extensions is a list of extensions to extend the sync/remove logic.
	// +kubebuilder:validation:Optional
	Extensions []Extension `json:"extensions,omitempty"`
}

type Option struct {
	// Name is the name of the option.
	Name string `json:"name"`
	// Value is the value of the option.
	Value string `json:"value"`
}

type Extension struct {
	// Name is the name of the extension.
	Name string `json:"name"`
	// Kind is the kind of the extension.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// Params is the params of the extension.
	// +kubebuilder:validation:Optional
	Params map[string]string `json:"params,omitempty"`
}

type ValuesFrom struct {
	// Kind is the type of resource being referenced
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind"`
	// Name is the name of resource being referenced
	Name string `json:"name"`
	// An optional identifier to prepend to each key in the ConfigMap. Must be a C_IDENTIFIER.
	// +kubebuilder:validation:Optional
	Prefix string `json:"prefix,omitempty"`
	// Optional set to true to ignore references not found error
	Optional bool `json:"optional,omitempty"`
}

type InstanceStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is the current state of the release
	Phase Phase `json:"phase,omitempty"`

	// Message is the message associated with the status
	// Contains error message when phase is Failed, cleared on success.
	Message string `json:"message,omitempty"`

	// Note contains the rendered notes from helm chart
	Note string `json:"note,omitempty"`

	// Conditions represent the latest available observations of the instance's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// Values is a nested map of final helm values.
	// +kubebuilder:pruning:PreserveUnknownFields
	Values Values `json:"values,omitempty"`

	// Version is the version of the instance.
	// In helm, Version is the version of the chart.
	Version string `json:"version,omitempty"`

	// AppVersion is the app version of the instance.
	AppVersion string `json:"appVersion,omitempty"`

	// CreationTimestamp is the first creation timestamp of the instance.
	CreationTimestamp metav1.Time `json:"creationTimestamp,omitempty"`

	// UpgradeTimestamp is the time when the instance was last upgraded.
	UpgradeTimestamp metav1.Time `json:"upgradeTimestamp,omitempty"`

	// Resources is a list of resources created/managed by the instance.
	Resources []ManagedResource `json:"resources,omitempty"`

	// Endpoints contains access endpoints extracted from Services and Ingresses
	Endpoints []Endpoint `json:"endpoints,omitempty"`

	// States contains the status of each workload component (Deployment, StatefulSet, etc.)
	States []State `json:"states,omitempty"`

	// Summary is computed from summary-expression annotation
	// Used for displaying key business information in list views
	Summary map[string]string `json:"summary,omitempty"`
}

type ManagedResource struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

func GetReference(obj client.Object) ManagedResource {
	return ManagedResource{
		APIVersion: obj.GetObjectKind().GroupVersionKind().GroupVersion().String(),
		Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
	}
}

// IsAnAPIObject allows clients to preemptively get a reference to an API object and pass it to places that
// intend only to get a reference to that object. This simplifies the event recording interface.
func (obj *ManagedResource) SetGroupVersionKind(gvk schema.GroupVersionKind) {
	obj.APIVersion, obj.Kind = gvk.ToAPIVersionAndKind()
}

func (obj *ManagedResource) GroupVersionKind() schema.GroupVersionKind {
	return schema.FromAPIVersionAndKind(obj.APIVersion, obj.Kind)
}

func (obj *ManagedResource) GetObjectKind() schema.ObjectKind { return obj }

type Phase string

// +kubebuilder:validation:Enum=helm;kustomize;template
type InstanceKind string

const (
	InstanceKindHelm      InstanceKind = "helm"
	InstanceKindKustomize InstanceKind = "kustomize"
	InstanceKindTemplate  InstanceKind = "template"
)

const (
	// Lifecycle Phases
	PhaseReconciling Phase = "Reconciling" // Reconciling (Installing/Updating)
	PhaseTerminating Phase = "Terminating" // Terminating
	PhaseInstalled   Phase = "Installed"   // Installed (No workload)
	PhaseFailed      Phase = "Failed"      // Failed (Installation failed or runtime failed)

	// Control Phases
	PhasePaused Phase = "Paused" // Paused

	// Long-running Workload Phases (Deployment, StatefulSet, DaemonSet)
	PhaseHealthy   Phase = "Healthy"   // Healthy (All components healthy)
	PhaseDegraded  Phase = "Degraded"  // Degraded (Partial replicas available)
	PhaseUnhealthy Phase = "Unhealthy" // Unhealthy

	// Job Phases (Job, Pod)
	PhasePending       Phase = "Pending"       // Pending (Waiting for scheduling)
	PhaseRunning       Phase = "Running"       // Running
	PhaseSucceeded     Phase = "Succeeded"     // Succeeded (All succeeded)
	PhasePartialFailed Phase = "PartialFailed" // PartialFailed (Partially succeeded, partially failed)
)

// State represents the status of a workload component
type State struct {
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"` // Job, Deployment, StatefulSet, DaemonSet, Pod
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Endpoint represents an access endpoint for the instance
type Endpoint struct {
	Name string `json:"name"`
	// URL is the primary URL for this endpoint
	URL string `json:"url"`
	// URLs is multiple URLs for this endpoint
	URLs []string `json:"urls,omitempty"`
	// Kind of endpoint, e.g. Cluster, Internal, External
	Kind EndpointKind `json:"kind"`
}

// EndpointKind represents the accessibility level of an endpoint
type EndpointKind string

const (
	// EndpointKindCluster means the endpoint is only accessible within the cluster
	EndpointKindCluster EndpointKind = "Cluster"
	// EndpointKindInternal means the endpoint is accessible within the intranet
	EndpointKindInternal EndpointKind = "Internal"
	// EndpointKindExternal means the endpoint is accessible publicly
	EndpointKindExternal EndpointKind = "External"
)

// Condition types for Instance
const (
	// ConditionDependenciesReady indicates whether all dependencies are ready.
	ConditionDependenciesReady = "DependenciesReady"
	// ConditionInstalled indicates whether the instance has been successfully installed.
	ConditionInstalled = "Installed"
	// ConditionReady indicates whether the instance is ready and fully operational.
	ConditionReady = "Ready"
)
