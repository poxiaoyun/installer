package helm

import (
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
)

func TestDesiredReleaseState(t *testing.T) {
	base := func() *chart.Chart {
		return &chart.Chart{
			Metadata:  &chart.Metadata{Name: "demo", Version: "1.0.0"},
			Values:    map[string]any{"value": "one"},
			Templates: []*chart.File{{Name: "templates/configmap.yaml", Data: []byte("value: one")}},
			Files:     []*chart.File{{Name: "dashboards/demo.json", Data: []byte("{}")}},
		}
	}

	want := desiredReleaseState(base(), "post-render-v1")
	if got := desiredReleaseState(base(), "post-render-v1"); got != want {
		t.Fatalf("desiredReleaseState() = %q, want stable value %q", got, want)
	}
	if got := desiredReleaseState(base(), "post-render-v2"); got == want {
		t.Fatal("desiredReleaseState() did not change with post-render identity")
	}
	changedChart := base()
	changedChart.Templates[0].Data = []byte("value: two")
	if got := desiredReleaseState(changedChart, "post-render-v1"); got == want {
		t.Fatal("desiredReleaseState() did not change with chart content")
	}
	if len(want) > 63 {
		t.Fatalf("desiredReleaseState() length = %d, exceeds Kubernetes label value limit", len(want))
	}
}

func TestEqualMapValues(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]interface{}
		b        map[string]interface{}
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "both empty",
			a:        map[string]interface{}{},
			b:        map[string]interface{}{},
			expected: true,
		},
		{
			name:     "one nil one empty",
			a:        nil,
			b:        map[string]interface{}{},
			expected: true,
		},
		{
			name:     "equal simple maps",
			a:        map[string]interface{}{"key": "value"},
			b:        map[string]interface{}{"key": "value"},
			expected: true,
		},
		{
			name:     "different values",
			a:        map[string]interface{}{"key": "value1"},
			b:        map[string]interface{}{"key": "value2"},
			expected: false,
		},
		{
			name:     "different keys",
			a:        map[string]interface{}{"key1": "value"},
			b:        map[string]interface{}{"key2": "value"},
			expected: false,
		},
		{
			name:     "nested maps equal",
			a:        map[string]interface{}{"nested": map[string]interface{}{"key": "value"}},
			b:        map[string]interface{}{"nested": map[string]interface{}{"key": "value"}},
			expected: true,
		},
		{
			name:     "nested maps different",
			a:        map[string]interface{}{"nested": map[string]interface{}{"key": "value1"}},
			b:        map[string]interface{}{"nested": map[string]interface{}{"key": "value2"}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := equalMapValues(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("equalMapValues(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
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
