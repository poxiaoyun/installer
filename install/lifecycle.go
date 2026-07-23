package install

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AnnotationUpgradeStrategy = "app.kubernetes.io/upgrade-strategy"
	AnnotationRemoveStrategy  = "app.kubernetes.io/remove-strategy"

	UpgradeStrategyRetain   = "Retain"
	UpgradeStrategyRecreate = "Recreate"
	RemoveStrategyRetain    = "Retain"
)

// UpgradeStrategy returns the validated upgrade strategy declared by obj.
// An empty value means that the installer's normal update behavior applies.
func UpgradeStrategy(obj metav1.Object) (string, error) {
	value := obj.GetAnnotations()[AnnotationUpgradeStrategy]
	switch value {
	case "", UpgradeStrategyRetain, UpgradeStrategyRecreate:
		return value, nil
	default:
		return "", fmt.Errorf("invalid %s %q on %s/%s: expected %q or %q",
			AnnotationUpgradeStrategy, value, obj.GetNamespace(), obj.GetName(),
			UpgradeStrategyRetain, UpgradeStrategyRecreate)
	}
}

// RemoveStrategy returns the validated remove strategy declared by obj.
// An empty value means that the installer's normal deletion behavior applies.
func RemoveStrategy(obj metav1.Object) (string, error) {
	value := obj.GetAnnotations()[AnnotationRemoveStrategy]
	switch value {
	case "", RemoveStrategyRetain:
		return value, nil
	default:
		return "", fmt.Errorf("invalid %s %q on %s/%s: expected %q",
			AnnotationRemoveStrategy, value, obj.GetNamespace(), obj.GetName(),
			RemoveStrategyRetain)
	}
}

// ValidateLifecycleStrategies validates all lifecycle annotations on obj.
func ValidateLifecycleStrategies(obj metav1.Object) error {
	if _, err := UpgradeStrategy(obj); err != nil {
		return err
	}
	_, err := RemoveStrategy(obj)
	return err
}
