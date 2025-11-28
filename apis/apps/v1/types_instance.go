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
// +kubebuilder:printcolumn:name="APP",type="string",JSONPath=".status.appVersion",description="App version",priority=1
// +kubebuilder:printcolumn:name="PHASE",type="string",JSONPath=".status.phase",description="Current phase"
// +kubebuilder:printcolumn:name="UPDATE",type="date",JSONPath=".status.upgradeTimestamp",description="Last upgrade",priority=1
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
}

const (
	ValuesFromKindConfigmap = "ConfigMap"
	ValuesFromKindSecret    = "Secret"
)

type ValuesFrom struct {
	// Kind is the type of resource being referenced
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind"`
	// Name is the name of resource being referenced
	Name string `json:"name"`
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
	// An optional identifier to prepend to each key in the ConfigMap. Must be a C_IDENTIFIER.
	// +kubebuilder:validation:Optional
	Prefix string `json:"prefix,omitempty"`
	// Optional set to true to ignore references not found error
	Optional bool `json:"optional,omitempty"`
}

type InstanceStatus struct {
	// Phase is the current state of the release
	Phase Phase `json:"phase,omitempty"`

	// Message is the message associated with the status
	// In helm, it's the notes contents.
	Message string `json:"message,omitempty"`

	// Values is a nested map of final helm values.
	// +kubebuilder:pruning:PreserveUnknownFields
	Values Values `json:"values,omitempty"`

	// Version is the version of the instance.
	// In helm, Version is the version of the chart.
	Version string `json:"version,omitempty"`

	// AppVersion is the app version of the instance.
	AppVersion string `json:"appVersion,omitempty"`

	// Namespace is the namespace where the instance is installed.
	Namespace string `json:"namespace,omitempty"`

	// CreationTimestamp is the first creation timestamp of the instance.
	CreationTimestamp metav1.Time `json:"creationTimestamp,omitempty"`

	// UpgradeTimestamp is the time when the instance was last upgraded.
	UpgradeTimestamp metav1.Time `json:"upgradeTimestamp,omitempty"`

	// Resources is a list of resources created/managed by the instance.
	Resources []ManagedResource `json:"resources,omitempty"`
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
	PhaseDisabled  Phase = "Disabled"  // Instance is disabled. the .spce.disbaled field is set to true or DeletionTimestamp is set.
	PhaseFailed    Phase = "Failed"    // Failed on install.
	PhaseInstalled Phase = "Installed" // Instance is installed
)
