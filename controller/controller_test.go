package controller_test

import (
	"testing"
	"time"

	"xiaoshiai.cn/installer/controller"
)

func TestDefaultReconciliationTimeout(t *testing.T) {
	options := controller.NewDefaultOptions()

	if options.ReconciliationTimeout != 15*time.Minute {
		t.Fatalf("ReconciliationTimeout = %s, want 15m", options.ReconciliationTimeout)
	}
}

func TestDefaultOptionsRequireRuneSystemAnnotationForClusterScope(t *testing.T) {
	options := controller.NewDefaultOptions()

	for _, namespace := range options.AllowClusterScopedNamespaces {
		if namespace == "rune-system" {
			t.Fatal("rune-system has implicit cluster-scoped permission")
		}
	}
}
