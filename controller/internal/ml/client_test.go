package ml

import (
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

func TestSeverityFromScore(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{0.0, "warning"},
		{0.5, "warning"},
		{0.8, "warning"}, // boundary: 0.8 is NOT > 0.8
		{0.81, "critical"},
		{1.0, "critical"},
	}
	for _, c := range cases {
		got := severityFromScore(c.score)
		if got != c.want {
			t.Errorf("severityFromScore(%.2f): want %q, got %q", c.score, c.want, got)
		}
	}
}

func TestJoinMetrics_Empty(t *testing.T) {
	got := joinMetrics(nil)
	if got != "" {
		t.Errorf("empty metrics should return empty string, got %q", got)
	}
}

func TestJoinMetrics_Single(t *testing.T) {
	got := joinMetrics([]string{"cpu_ratio"})
	if got != "cpu_ratio" {
		t.Errorf("single metric: want cpu_ratio, got %q", got)
	}
}

func TestJoinMetrics_Multiple(t *testing.T) {
	got := joinMetrics([]string{"cpu_ratio", "memory_ratio", "restarts_5m"})
	want := "cpu_ratio,memory_ratio,restarts_5m"
	if got != want {
		t.Errorf("joinMetrics: want %q, got %q", want, got)
	}
}

func TestJoinMetrics_Two(t *testing.T) {
	got := joinMetrics([]string{"cpu", "mem"})
	if got != "cpu,mem" {
		t.Errorf("two metrics: want cpu,mem, got %q", got)
	}
}

// ─── Client lifecycle ─────────────────────────────────────────────────────────

func TestNew_Disabled_NoDialError(t *testing.T) {
	c, err := New(config.ML{Enabled: false})
	if err != nil {
		t.Fatalf("disabled ML client should not return error, got: %v", err)
	}
	if c == nil {
		t.Fatal("disabled ML client should not be nil")
	}
	defer c.Close()
}

func TestEnabled_Disabled(t *testing.T) {
	c, _ := New(config.ML{Enabled: false})
	defer c.Close()
	if c.Enabled() {
		t.Error("disabled client should return Enabled()=false")
	}
}

func TestClose_DisabledClient_NoOp(t *testing.T) {
	c, _ := New(config.ML{Enabled: false})
	// Close on a disabled client (no gRPC conn) should not panic
	c.Close()
}
