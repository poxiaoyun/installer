package helm

import (
	"context"
	"testing"
	"time"

	release "helm.sh/helm/v4/pkg/release/common"
	"k8s.io/client-go/rest"
)

func TestNewHelmConfigLimitsRequestsToContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	helmConfig, err := NewHelmConfig(ctx, "default", &rest.Config{Host: "https://example.invalid"})
	if err != nil {
		t.Fatalf("NewHelmConfig() error = %v", err)
	}
	config, err := helmConfig.RESTClientGetter.ToRESTConfig()
	if err != nil {
		t.Fatalf("ToRESTConfig() error = %v", err)
	}
	if config.Timeout <= 0 || config.Timeout > time.Minute {
		t.Fatalf("REST timeout = %s, want a positive duration no greater than the context deadline", config.Timeout)
	}
}

func TestReleaseStatusIsPending(t *testing.T) {
	// Test that IsPending() correctly identifies pending states
	pendingStates := []release.Status{
		release.StatusPendingInstall,
		release.StatusPendingUpgrade,
		release.StatusPendingRollback,
	}

	for _, status := range pendingStates {
		if !status.IsPending() {
			t.Errorf("Expected %s to be pending, but IsPending() returned false", status)
		}
	}

	// Test non-pending states
	nonPendingStates := []release.Status{
		release.StatusDeployed,
		release.StatusFailed,
		release.StatusUninstalled,
		release.StatusSuperseded,
	}

	for _, status := range nonPendingStates {
		if status.IsPending() {
			t.Errorf("Expected %s to not be pending, but IsPending() returned true", status)
		}
	}
}
