package correlation

import (
	"context"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
)

// fakeDedupStore is an in-memory implementation of dedupStore for testing.
type fakeDedupStore struct {
	data map[string]bool
	err  error
}

func newFakeDedup() *fakeDedupStore {
	return &fakeDedupStore{data: make(map[string]bool)}
}

func (f *fakeDedupStore) Exists(_ context.Context, key string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.data[key], nil
}

func (f *fakeDedupStore) SetWithTTL(_ context.Context, key, _ string, _ time.Duration) error {
	if f.err != nil {
		return f.err
	}
	f.data[key] = true
	return nil
}

// fakeEnricher always returns an empty bundle.
type fakeEnricher struct{}

func (f *fakeEnricher) Run(_ context.Context, id enrichment.Identity) enrichment.Bundle {
	return enrichment.Bundle{Identity: id}
}

func podAnomaly(ns, pod, detector, severity, signal string) detection.Anomaly {
	return detection.Anomaly{
		Labels:     map[string]string{"namespace": ns, "pod": pod},
		Detector:   detector,
		Severity:   severity,
		Signal:     signal,
		MetricName: "test_metric",
		Timestamp:  time.Now(),
	}
}

func newTestCorrelator(dedup *fakeDedupStore) *Correlator {
	return NewCorrelator(dedup, &fakeEnricher{}, 0, 5*time.Minute, 3)
}

func TestAdd_And_Flush_SingleAnomaly(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())
	c.Add(podAnomaly("prod", "api-abc-xyz", "adaptive", "warning", "metrics"))

	alerts := c.Flush(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Kind != KindPod {
		t.Errorf("expected KindPod, got %v", alerts[0].Kind)
	}
	if alerts[0].Severity != "warning" {
		t.Errorf("expected warning, got %q", alerts[0].Severity)
	}
}

func TestFlush_Empty_NoAlerts(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())
	alerts := c.Flush(context.Background())
	if len(alerts) != 0 {
		t.Errorf("expected no alerts on empty flush, got %d", len(alerts))
	}
}

func TestFlush_Dedup_SecondFlushSuppressed(t *testing.T) {
	dedup := newFakeDedup()
	c := newTestCorrelator(dedup)

	c.Add(podAnomaly("prod", "api-abc-xyz", "adaptive", "warning", "metrics"))
	first := c.Flush(context.Background())
	if len(first) != 1 {
		t.Fatalf("first flush: expected 1 alert, got %d", len(first))
	}

	// Add same identity again — dedup should suppress it
	c.Add(podAnomaly("prod", "api-abc-xyz", "adaptive", "warning", "metrics"))
	second := c.Flush(context.Background())
	if len(second) != 0 {
		t.Errorf("second flush: expected dedup to suppress, got %d alerts", len(second))
	}
}

func TestFlush_MultipleSignals_EscalatesToCritical(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())

	// Same pod, both metrics and logs signal
	c.Add(podAnomaly("prod", "api-xyz", "adaptive", "warning", "metrics"))
	c.Add(podAnomaly("prod", "api-xyz", "adaptive", "warning", "logs"))

	alerts := c.Flush(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("expected 1 grouped alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "critical" {
		t.Errorf("multi-signal should escalate to critical, got %q", alerts[0].Severity)
	}
}

func TestFlush_CriticalAnomaly_KeepsCritical(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())
	a := podAnomaly("prod", "api-abc", "static", "critical", "metrics")
	c.Add(a)

	alerts := c.Flush(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != "critical" {
		t.Errorf("critical anomaly should stay critical, got %q", alerts[0].Severity)
	}
}

func TestFlush_WorkloadPattern_ThreeSiblings(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())

	// Three pods of same workload (deployment pattern: name-hash-pod)
	c.Add(podAnomaly("prod", "api-6d9ff7b9-aaa11", "adaptive", "warning", "metrics"))
	c.Add(podAnomaly("prod", "api-6d9ff7b9-bbb22", "adaptive", "warning", "metrics"))
	c.Add(podAnomaly("prod", "api-6d9ff7b9-ccc33", "adaptive", "warning", "metrics"))

	alerts := c.Flush(context.Background())
	if len(alerts) != 1 {
		t.Fatalf("expected 1 workload-level alert, got %d: %+v", len(alerts), alerts)
	}
	if alerts[0].Kind != KindWorkload {
		t.Errorf("3 sibling pods should produce KindWorkload, got %v", alerts[0].Kind)
	}
	if alerts[0].AffectedReplicas != 3 {
		t.Errorf("expected AffectedReplicas=3, got %d", alerts[0].AffectedReplicas)
	}
}

func TestFlush_WorkloadPattern_TwoSiblings_NoPomotion(t *testing.T) {
	c := newTestCorrelator(newFakeDedup())

	// Only 2 pods — below threshold of 3
	c.Add(podAnomaly("prod", "api-6d9ff7b9-aaa11", "adaptive", "warning", "metrics"))
	c.Add(podAnomaly("prod", "api-6d9ff7b9-bbb22", "adaptive", "warning", "metrics"))

	alerts := c.Flush(context.Background())
	// Should produce 2 pod-level alerts, not a workload alert
	for _, a := range alerts {
		if a.Kind == KindWorkload {
			t.Error("2 sibling pods should NOT produce a workload-level alert")
		}
	}
}

func TestFlush_NilEnricher_DoesNotPanic(t *testing.T) {
	c := NewCorrelator(newFakeDedup(), nil, 0, 5*time.Minute, 3)
	c.Add(podAnomaly("prod", "api-abc-xyz", "adaptive", "warning", "metrics"))

	// Should not panic with nil enricher
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked with nil enricher: %v", r)
		}
	}()
	c.Flush(context.Background())
}

func TestFlush_DedupError_AlertStillEmitted(t *testing.T) {
	// Even if dedup check fails, alert should still be emitted (fail-open)
	dedup := newFakeDedup()
	c := newTestCorrelator(dedup)

	c.Add(podAnomaly("prod", "api-abc-xyz", "adaptive", "warning", "metrics"))
	// Set error after adding — dedup Exists check will fail
	dedup.err = context.DeadlineExceeded

	alerts := c.Flush(context.Background())
	// With error, dedup is skipped (slog.Warn), alert passes through
	if len(alerts) != 1 {
		t.Errorf("dedup error should not block alert, expected 1, got %d", len(alerts))
	}
}

func TestExtractWorkloadFromKey(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"prod/api", "api"},
		{"monitoring/prometheus", "prometheus"},
		{"noslash", "noslash"},
		{"", ""},
	}
	for _, c := range cases {
		got := ExtractWorkloadFromKey(c.key)
		if got != c.want {
			t.Errorf("ExtractWorkloadFromKey(%q): want %q, got %q", c.key, c.want, got)
		}
	}
}

func TestWorkloadKey_Pod(t *testing.T) {
	a := detection.Anomaly{
		Labels: map[string]string{"namespace": "prod", "pod": "api-abc"},
	}
	key := workloadKey(a)
	if key != "prod/api-abc" {
		t.Errorf("want prod/api-abc, got %q", key)
	}
}

func TestWorkloadKey_ServiceFallback(t *testing.T) {
	a := detection.Anomaly{
		Labels: map[string]string{"namespace": "prod", "service_name": "api-svc"},
	}
	key := workloadKey(a)
	if key != "prod/api-svc" {
		t.Errorf("want prod/api-svc, got %q", key)
	}
}

func TestWorkloadKey_Empty(t *testing.T) {
	a := detection.Anomaly{Labels: map[string]string{}}
	key := workloadKey(a)
	if key != "_unknown_" {
		t.Errorf("want _unknown_, got %q", key)
	}
}

func TestNewCorrelator_SetsDefaultMinPods(t *testing.T) {
	c := NewCorrelator(newFakeDedup(), nil, 0, time.Minute, 1)
	if c.workloadMinPods != 3 {
		t.Errorf("min pods below 2 should default to 3, got %d", c.workloadMinPods)
	}
}
