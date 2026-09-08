package postrender

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// Public wire contract owned by Scheduling, carried in resolved Flavor values.
const numaPolicyAnnotation = "scheduling.xiaoshiai.cn/numa-policy"

func applyFlavorNUMA(object *unstructured.Unstructured, podSpecPath []string, flavor resolvedFlavorRuntime) error {
	if flavor.NUMAPolicy == "" {
		return nil
	}
	if flavor.NUMAPolicy != "single-numa-node" {
		return fmt.Errorf("workload %s/%s: unsupported Flavor NUMA policy %q", object.GetKind(), object.GetName(), flavor.NUMAPolicy)
	}
	gvk := object.GroupVersionKind()
	supported := gvk.Group == "apps" && (gvk.Kind == "Deployment" || gvk.Kind == "StatefulSet" || gvk.Kind == "DaemonSet") || gvk.Group == "batch" && gvk.Kind == "Job"
	if !supported {
		return fmt.Errorf("workload %s/%s: CPU NUMA requires Deployment, StatefulSet, DaemonSet or Job supported by Scheduling admission", object.GetKind(), object.GetName())
	}
	if flavor.SchedulerName != "volcano" {
		return fmt.Errorf("workload %s/%s: CPU NUMA Flavor requires schedulerName volcano", object.GetKind(), object.GetName())
	}
	if err := setFlavorString(object, append(podSpecPath, "schedulerName"), flavor.SchedulerName); err != nil {
		return err
	}
	raw, _, err := unstructured.NestedMap(object.Object, podSpecPath...)
	if err != nil {
		return err
	}
	if _, exists := raw["resources"]; exists {
		return fmt.Errorf("workload %s/%s: CPU NUMA currently requires container resources, not Pod-level resources", object.GetKind(), object.GetName())
	}
	var spec corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &spec); err != nil {
		return fmt.Errorf("decode CPU NUMA PodSpec: %w", err)
	}
	if len(spec.Containers) == 0 {
		return fmt.Errorf("CPU NUMA workload must contain at least one container")
	}
	for _, containers := range [][]corev1.Container{spec.Containers, spec.InitContainers} {
		for _, container := range containers {
			for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
				request, limit := container.Resources.Requests[name], container.Resources.Limits[name]
				if request.Sign() <= 0 || request.Cmp(limit) != 0 {
					return fmt.Errorf("CPU NUMA container %q requires positive, equal %s requests and limits (Guaranteed QoS)", container.Name, name)
				}
				if name == corev1.ResourceCPU {
					if request.CmpInt64(request.Value()) != 0 {
						return fmt.Errorf("CPU NUMA container %q requires integer CPU requests", container.Name)
					}
				}
			}
		}
	}
	// Scheduling owns backend annotation projection; Installer does not write
	// Volcano Queue, Gang or backend topology keys.
	return setFlavorString(object, []string{"metadata", "annotations", numaPolicyAnnotation}, flavor.NUMAPolicy)
}
