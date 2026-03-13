package postrender

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

// ExtensionRenderer applies extension-specific modifications to rendered objects.
// Currently supports NodePort extension.
type ExtensionRenderer struct {
	Extensions []appsv1.Extension
}

func (e *ExtensionRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	for _, ext := range e.Extensions {
		switch ext.Kind {
		case "NodePort":
			modified, err := applyNodePortExtension(objects, ext.Params)
			if err != nil {
				return nil, fmt.Errorf("apply NodePort extension %q: %w", ext.Name, err)
			}
			objects = modified
		}
	}
	return objects, nil
}

// applyNodePortExtension creates a new NodePort Service for each matching Service.
func applyNodePortExtension(objects []*unstructured.Unstructured, params map[string]string) ([]*unstructured.Unstructured, error) {
	svcname := getParamKey(params, "service", "svc", "name")
	portsStr := getParamKey(params, "ports", "port")
	if portsStr == "" {
		return nil, fmt.Errorf("ports must be specified for NodePort extension")
	}
	targetPorts, err := parseNodePortConfig(portsStr)
	if err != nil {
		return nil, err
	}

	var newObjects []*unstructured.Unstructured
	for _, obj := range objects {
		if obj.GetKind() != "Service" || obj.GetAPIVersion() != "v1" {
			continue
		}
		if svcname != "" && obj.GetName() != svcname {
			continue
		}

		// Convert to typed Service for easier manipulation
		svc := &corev1.Service{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, svc); err != nil {
			continue
		}

		newSvc := svc.DeepCopy()
		newSvc.Name = svc.Name + "-nodeport"
		newSvc.Spec.Type = corev1.ServiceTypeNodePort
		newSvc.Spec.ClusterIP = ""
		newSvc.Spec.ClusterIPs = nil
		newSvc.ResourceVersion = ""
		newSvc.UID = ""

		var newPorts []corev1.ServicePort
		for _, sp := range svc.Spec.Ports {
			if nodePort, ok := targetPorts[sp.Port]; ok {
				sp.NodePort = nodePort
				newPorts = append(newPorts, sp)
			}
		}
		if len(newPorts) == 0 {
			continue
		}
		newSvc.Spec.Ports = newPorts

		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(newSvc)
		if err != nil {
			return nil, fmt.Errorf("convert nodeport service: %w", err)
		}
		newObj := &unstructured.Unstructured{Object: u}
		newObj.SetAPIVersion("v1")
		newObj.SetKind("Service")
		newObjects = append(newObjects, newObj)
	}

	objects = append(objects, newObjects...)
	return objects, nil
}

// parseNodePortConfig parses port mappings like "8080" or "8080:30080,443:30443".
func parseNodePortConfig(portsStr string) (map[int32]int32, error) {
	targetPorts := make(map[int32]int32)
	for _, p := range strings.Split(portsStr, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		parts := strings.SplitN(p, ":", 2)
		port, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", p, err)
		}
		var nodePort int32
		if len(parts) == 2 {
			np, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid nodePort %q: %w", p, err)
			}
			nodePort = int32(np)
		}
		targetPorts[int32(port)] = nodePort
	}
	return targetPorts, nil
}

func getParamKey(params map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := params[k]; ok {
			return v
		}
	}
	return ""
}
