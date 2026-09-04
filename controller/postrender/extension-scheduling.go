package postrender

import (
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	appbase "xiaoshiai.cn/installer/apis/apps"
	appsv1 "xiaoshiai.cn/installer/apis/apps/v1"
)

const (
	SchedulingModeDefault = "default"
	SchedulingModeVolcano = "volcano"
	SchedulingModeGang    = "gang"

	SchedulingPriorityDefault = "default"
	SchedulingPriorityLow     = "low"
	SchedulingPriorityMedium  = "medium"
	SchedulingPriorityHigh    = "high"

	VolcanoSchedulerName        = "volcano"
	LowPriorityClassName        = "lower-priority"
	MediumPriorityClassName     = "medium-priority"
	HighPriorityClassName       = "high-priority"
	PodGroupReferenceAnnotation = "scheduling.k8s.io/group-name"
)

// SchedulingHandler projects the user-selected scheduler and priority onto
// Chart-defined workloads and PodGroups. Charts remain responsible for
// rendering PodGroups, their minCount, and workload group references.
type SchedulingHandler struct{}

type schedulingProfile struct {
	mode              string
	priority          string
	priorityClassName string
	minCount          int64
}

// SchedulingRenderer extracts the single platform Scheduling extension and
// applies it after all ordinary extensions have finished adding or changing
// rendered objects.
type SchedulingRenderer struct {
	Extensions []appsv1.Extension
	Handler    *SchedulingHandler
}

func (r *SchedulingRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	extension, err := singleSchedulingExtension(r.Extensions)
	if err != nil {
		return nil, err
	}
	if extension == nil {
		return objects, nil
	}
	if r.Handler == nil {
		return nil, fmt.Errorf("Scheduling extension handler is unavailable")
	}
	return r.Handler.Handle(objects, *extension)
}

// SchedulingValues returns platform-owned global.scheduling values. An absent
// extension is the explicit Kubernetes/default-priority profile, so Chart
// defaults cannot accidentally opt an API-created Instance into Gang mode.
func SchedulingValues(extensions []appsv1.Extension) (map[string]any, error) {
	extension, err := singleSchedulingExtension(extensions)
	if err != nil {
		return nil, err
	}
	params := map[string]string{}
	if extension != nil {
		params = extension.Params
	}
	profile, err := parseSchedulingProfile(params)
	if err != nil {
		return nil, err
	}
	values := map[string]any{
		"mode":     profile.mode,
		"priority": profile.priority,
	}
	if profile.mode == SchedulingModeGang {
		values["minCount"] = float64(profile.minCount)
	}
	return values, nil
}

func singleSchedulingExtension(extensions []appsv1.Extension) (*appsv1.Extension, error) {
	var scheduling *appsv1.Extension
	for i := range extensions {
		if extensions[i].Kind != appbase.ExtensionKindScheduling {
			continue
		}
		if scheduling != nil {
			return nil, fmt.Errorf("only one %s extension is allowed", appbase.ExtensionKindScheduling)
		}
		scheduling = &extensions[i]
	}
	return scheduling, nil
}

func (h *SchedulingHandler) Handle(objects []*unstructured.Unstructured, ext appsv1.Extension) ([]*unstructured.Unstructured, error) {
	profile, err := parseSchedulingProfile(ext.Params)
	if err != nil {
		return nil, err
	}

	targets, referencedGroups, err := schedulingTargets(objects, profile.mode)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		if profile.mode == SchedulingModeDefault && profile.priorityClassName == "" {
			return objects, nil
		}
		return nil, fmt.Errorf("no supported scheduling workload found")
	}

	wantScheduler := ""
	if profile.mode == SchedulingModeVolcano || profile.mode == SchedulingModeGang {
		wantScheduler = VolcanoSchedulerName
	}
	for _, target := range targets {
		path, _ := workloadPodSpecPath(target)
		if err := setOwnedNestedString(target, append(path, "schedulerName"), wantScheduler, []string{VolcanoSchedulerName}); err != nil {
			return nil, err
		}
		if err := setOwnedNestedString(target, append(path, "priorityClassName"), profile.priorityClassName, []string{
			LowPriorityClassName,
			MediumPriorityClassName,
			HighPriorityClassName,
		}); err != nil {
			return nil, err
		}
	}

	if profile.mode != SchedulingModeGang {
		if len(referencedGroups) > 0 {
			return nil, fmt.Errorf("non-gang scheduling target still references a Chart-defined PodGroup")
		}
		return objects, nil
	}
	if len(referencedGroups) == 0 {
		return nil, fmt.Errorf("gang scheduling requires Chart-defined %s annotations", PodGroupReferenceAnnotation)
	}
	if err := validateAndPrioritizePodGroups(objects, referencedGroups, profile.priorityClassName, profile.minCount); err != nil {
		return nil, err
	}
	return objects, nil
}

func parseSchedulingProfile(params map[string]string) (schedulingProfile, error) {
	profile := schedulingProfile{
		mode:     params[appbase.ExtensionParamSchedulingMode],
		priority: params[appbase.ExtensionParamSchedulingPriority],
	}
	if profile.mode == "" {
		profile.mode = SchedulingModeDefault
	}
	if profile.priority == "" {
		if profile.mode == SchedulingModeDefault {
			profile.priority = SchedulingPriorityDefault
		} else {
			profile.priority = SchedulingPriorityHigh
		}
	}
	switch profile.mode {
	case SchedulingModeDefault, SchedulingModeVolcano:
		if params[appbase.ExtensionParamGangMinCount] != "" {
			return schedulingProfile{}, fmt.Errorf("minCount is only valid for gang scheduling")
		}
	case SchedulingModeGang:
		minCount, err := strconv.ParseInt(params[appbase.ExtensionParamGangMinCount], 10, 32)
		if err != nil || minCount < 1 {
			return schedulingProfile{}, fmt.Errorf("gang scheduling requires minCount >= 1")
		}
		profile.minCount = minCount
	default:
		return schedulingProfile{}, fmt.Errorf("unsupported scheduling mode %q", profile.mode)
	}
	switch profile.priority {
	case SchedulingPriorityDefault:
		profile.priorityClassName = ""
	case SchedulingPriorityLow:
		profile.priorityClassName = LowPriorityClassName
	case SchedulingPriorityMedium:
		profile.priorityClassName = MediumPriorityClassName
	case SchedulingPriorityHigh:
		profile.priorityClassName = HighPriorityClassName
	default:
		return schedulingProfile{}, fmt.Errorf("unsupported scheduling priority %q", profile.priority)
	}
	return profile, nil
}

func schedulingTargets(objects []*unstructured.Unstructured, mode string) ([]*unstructured.Unstructured, map[string]struct{}, error) {
	var eligible []*unstructured.Unstructured
	var marked []*unstructured.Unstructured
	referencedGroups := map[string]struct{}{}
	hasMarkers := false
	for _, obj := range objects {
		if _, ok := workloadPodSpecPath(obj); !ok {
			continue
		}
		eligible = append(eligible, obj)
		annotations := obj.GetAnnotations()
		targetValue, targetDeclared := annotations[appbase.AnnotationSchedulingTarget]
		existingGroup, alreadyGrouped := annotations[PodGroupReferenceAnnotation]
		_, derivedGroupReference, err := unstructured.NestedString(obj.Object, "spec", "template", "metadata", "annotations", PodGroupReferenceAnnotation)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s on workload %s/%s: %w", PodGroupReferenceAnnotation, obj.GetKind(), obj.GetName(), err)
		}
		if derivedGroupReference {
			return nil, nil, fmt.Errorf("workload %s/%s must declare %s on metadata.annotations; the Pod-template annotation is Scheduling-derived", obj.GetKind(), obj.GetName(), PodGroupReferenceAnnotation)
		}
		if targetDeclared || alreadyGrouped {
			hasMarkers = true
		}
		if targetDeclared && targetValue != "true" && targetValue != "false" {
			return nil, nil, fmt.Errorf("workload %s/%s has invalid %s value %q", obj.GetKind(), obj.GetName(), appbase.AnnotationSchedulingTarget, targetValue)
		}
		if targetValue == "false" && alreadyGrouped {
			return nil, nil, fmt.Errorf("workload %s/%s cannot opt out of scheduling while declaring a scheduling group", obj.GetKind(), obj.GetName())
		}
		if alreadyGrouped && existingGroup != "" {
			referencedGroups[existingGroup] = struct{}{}
		}
		if targetValue == "true" || alreadyGrouped {
			marked = append(marked, obj)
		}
	}
	if hasMarkers {
		return marked, referencedGroups, nil
	}
	if mode == SchedulingModeGang && len(eligible) > 1 {
		return nil, nil, fmt.Errorf("gang scheduling found %d workloads; Chart must mark its members", len(eligible))
	}
	return eligible, referencedGroups, nil
}

func workloadPodSpecPath(obj *unstructured.Unstructured) ([]string, bool) {
	gvk := obj.GroupVersionKind()
	switch {
	case gvk.Group == "apps" && (gvk.Kind == "Deployment" || gvk.Kind == "StatefulSet" || gvk.Kind == "DaemonSet"):
		return []string{"spec", "template", "spec"}, true
	case gvk.Group == "batch" && gvk.Kind == "Job":
		return []string{"spec", "template", "spec"}, true
	default:
		return nil, false
	}
}

func validateAndPrioritizePodGroups(objects []*unstructured.Unstructured, referencedGroups map[string]struct{}, priorityClassName string, expectedMinCount int64) error {
	found := map[string]struct{}{}
	for _, obj := range objects {
		if !isPublicPodGroup(obj) {
			continue
		}
		if _, referenced := referencedGroups[obj.GetName()]; !referenced {
			continue
		}
		if _, duplicate := found[obj.GetName()]; duplicate {
			return fmt.Errorf("multiple Chart-defined PodGroups are named %q", obj.GetName())
		}
		minCount, present, err := unstructured.NestedInt64(obj.Object, "spec", "schedulingPolicy", "gang", "minCount")
		if err != nil || !present || minCount < 1 {
			return fmt.Errorf("Chart-defined PodGroup %q requires spec.schedulingPolicy.gang.minCount >= 1", obj.GetName())
		}
		if minCount != expectedMinCount {
			return fmt.Errorf("Chart-defined PodGroup %q minCount is %d, want platform value %d", obj.GetName(), minCount, expectedMinCount)
		}
		if err := setOwnedNestedString(obj, []string{"spec", "priorityClassName"}, priorityClassName, []string{
			LowPriorityClassName,
			MediumPriorityClassName,
			HighPriorityClassName,
		}); err != nil {
			return err
		}
		found[obj.GetName()] = struct{}{}
	}
	for name := range referencedGroups {
		if _, ok := found[name]; !ok {
			return fmt.Errorf("Chart-defined PodGroup %q was not rendered", name)
		}
	}
	return nil
}

func isPublicPodGroup(obj *unstructured.Unstructured) bool {
	gvk := obj.GroupVersionKind()
	return gvk.Kind == "PodGroup" && (gvk.Group == "scheduling.k8s.io" || gvk.Group == "scheduling.xiaoshiai.cn")
}

func setOwnedNestedString(obj *unstructured.Unstructured, path []string, desired string, ownedValues []string) error {
	existing, found, err := unstructured.NestedString(obj.Object, path...)
	if err != nil {
		return fmt.Errorf("read %s on %s/%s: %w", path[len(path)-1], obj.GetKind(), obj.GetName(), err)
	}
	if found && existing != "" && existing != desired && !containsString(ownedValues, existing) {
		return fmt.Errorf("resource %s/%s has conflicting %s %q", obj.GetKind(), obj.GetName(), path[len(path)-1], existing)
	}
	if desired == "" {
		if found && containsString(ownedValues, existing) {
			unstructured.RemoveNestedField(obj.Object, path...)
		}
		return nil
	}
	if err := unstructured.SetNestedField(obj.Object, desired, path...); err != nil {
		return fmt.Errorf("set %s on %s/%s: %w", path[len(path)-1], obj.GetKind(), obj.GetName(), err)
	}
	return nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
