package postrender

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// PausedRenderer modifies workload objects to achieve a "paused" state:
//   - Deployment/StatefulSet: sets spec.replicas = 0
//   - Job/CronJob: sets spec.suspend = true
//   - DaemonSet: adds an intentionally unsatisfiable required node affinity
type PausedRenderer struct {
	Paused bool
}

const PausedNodeAffinityKey = "apps.xiaoshiai.cn/paused"

func (p *PausedRenderer) ModifyObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	if !p.Paused {
		return objects, nil
	}
	for _, obj := range objects {
		switch {
		case IsGroupKind(obj, "apps", "Deployment"), IsGroupKind(obj, "apps", "StatefulSet"):
			_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "replicas")
		case IsGroupKind(obj, "batch", "Job"), IsGroupKind(obj, "batch", "CronJob"):
			_ = unstructured.SetNestedField(obj.Object, true, "spec", "suspend")
		case IsGroupKind(obj, "apps", "DaemonSet"):
			injectPausedNodeAffinity(obj.Object)
		}
	}
	return objects, nil
}

// injectPausedNodeAffinity makes every required node selector term
// unsatisfiable by requiring the same reserved key to both exist and not
// exist. Node selector terms are ORed and expressions within a term are ANDed,
// so the contradiction must be added to every existing term. Other affinity,
// node selector, matchFields and preferred scheduling rules are preserved.
func injectPausedNodeAffinity(object map[string]any) {
	podSpec, ok := nestedMap(object, false, "spec", "template", "spec")
	if !ok {
		return
	}
	affinity := ensureMapField(podSpec, "affinity")
	nodeAffinity := ensureMapField(affinity, "nodeAffinity")
	required := ensureMapField(nodeAffinity, "requiredDuringSchedulingIgnoredDuringExecution")

	terms, _ := required["nodeSelectorTerms"].([]any)
	if len(terms) == 0 {
		terms = []any{map[string]any{}}
	}
	for i, raw := range terms {
		term, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		expressions, _ := term["matchExpressions"].([]any)
		expressions = ensurePausedRequirement(expressions, "Exists")
		expressions = ensurePausedRequirement(expressions, "DoesNotExist")
		term["matchExpressions"] = expressions
		terms[i] = term
	}
	required["nodeSelectorTerms"] = terms
}

func ensureMapField(object map[string]any, field string) map[string]any {
	value, _ := object[field].(map[string]any)
	if value == nil {
		value = map[string]any{}
		object[field] = value
	}
	return value
}

func ensurePausedRequirement(expressions []any, operator string) []any {
	for _, raw := range expressions {
		expression, ok := raw.(map[string]any)
		if ok && expression["key"] == PausedNodeAffinityKey && expression["operator"] == operator {
			return expressions
		}
	}
	return append(expressions, map[string]any{
		"key":      PausedNodeAffinityKey,
		"operator": operator,
	})
}
