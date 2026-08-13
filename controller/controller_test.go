package controller

import (
	"testing"
	"time"
)

func TestDefaultReconciliationTimeout(t *testing.T) {
	options := NewDefaultOptions()

	if options.ReconciliationTimeout != 15*time.Minute {
		t.Fatalf("ReconciliationTimeout = %s, want 15m", options.ReconciliationTimeout)
	}
}
