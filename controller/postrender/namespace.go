package postrender

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// NamespaceRenderer enforces namespace-scoped resource constraints.
// Cluster-scoped resources are only allowed when AllowClusterScoped is true.
// Cross-namespace resources are only allowed when AllowCrossNamespace is true.
type NamespaceRenderer struct {
	// Namespace is the target namespace for all namespace-scoped resources.
	Namespace string
	// AllowClusterScoped controls whether cluster-scoped resources are permitted.
	AllowClusterScoped bool
	// AllowCrossNamespace controls whether namespace-scoped resources may target
	// a namespace other than Namespace.
	AllowCrossNamespace bool
	// RESTMapper is used to determine whether a GVK is namespace-scoped or cluster-scoped.
	RESTMapper meta.RESTMapper
}

func (n *NamespaceRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	for _, obj := range objects {
		gvk := obj.GroupVersionKind()
		mapping, err := n.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			// Unknown GVK — skip silently; Helm/k8s will catch it later.
			continue
		}
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			// Namespace-scoped: reject cross-namespace resources unless cluster-scoped is allowed
			ns := obj.GetNamespace()
			if ns != "" && ns != n.Namespace && !n.AllowCrossNamespace {
				return nil, fmt.Errorf("resource %s %s/%s targets namespace %q, expected %q (cross-namespace not allowed)",
					gvk.Kind, ns, obj.GetName(), ns, n.Namespace)
			}
			if ns == "" {
				obj.SetNamespace(n.Namespace)
			}
		} else {
			// Cluster-scoped: check permission
			if !n.AllowClusterScoped {
				return nil, fmt.Errorf("cluster-scoped resource %s %s is not allowed (namespace %q not in allow list)",
					gvk.Kind, obj.GetName(), n.Namespace)
			}
		}
	}
	return objects, nil
}
