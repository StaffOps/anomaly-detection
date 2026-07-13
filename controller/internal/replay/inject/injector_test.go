package inject

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// --- Task 17: Injector no-op passthrough when no profile ---

func TestInjector_NilConfig_Passthrough(t *testing.T) {
	// Contract: NewInjector(nil) creates an injector that is a no-op passthrough.
	// Apply must return series unchanged — exact equality (no allocation).
	inj := NewInjector(nil)

	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	series := []ingestion.TimeSeries{
		makeConstantSeries(t0, time.Minute, 10, 42.0),
		makeConstantSeries(t0, time.Minute, 5, 99.0),
	}

	// Save pointers to verify exact identity (no copy/allocation).
	origPtr0 := &series[0].Points[0]

	result := inj.Apply("some_metric", series)

	// Result must be the same slice
	if len(result) != len(series) {
		t.Fatalf("passthrough: expected %d series, got %d", len(series), len(result))
	}
	if &result[0].Points[0] != origPtr0 {
		t.Error("passthrough allocated new slice — expected exact identity (no allocation)")
	}

	// Seed should be 0 when config is nil
	if inj.Seed() != 0 {
		t.Errorf("Seed() with nil config: expected 0, got %d", inj.Seed())
	}

	// Ground truths should be empty
	if len(inj.GroundTruths()) != 0 {
		t.Errorf("GroundTruths() with nil config: expected empty, got %d", len(inj.GroundTruths()))
	}
}

func TestInjector_EmptyInjections_Passthrough(t *testing.T) {
	// Config exists but has no injection entries → still passthrough.
	cfg := &InjectionConfig{
		Seed:       42,
		Injections: []InjectionEntry{},
	}
	inj := NewInjector(cfg)

	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	series := []ingestion.TimeSeries{makeConstantSeries(t0, time.Minute, 10, 50.0)}
	original := series[0].Points[0].V

	result := inj.Apply("metric_x", series)

	if result[0].Points[0].V != original {
		t.Errorf("empty injections modified series: got %f, expected %f", result[0].Points[0].V, original)
	}
	if inj.Seed() != 42 {
		t.Errorf("Seed(): expected 42, got %d", inj.Seed())
	}
}

func TestInjector_MetricMismatch_Passthrough(t *testing.T) {
	// Injection targets a different metric → series for this metric unchanged.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	cfg := &InjectionConfig{
		Seed: 1,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "error_rate", Labels: nil},
			Type:      FaultStep,
			Start:     t0,
			End:       t0.Add(5 * time.Minute),
			Magnitude: 5.0,
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{makeConstantSeries(t0, time.Minute, 10, 100.0)}
	original := make([]float64, 10)
	for i, p := range series[0].Points {
		original[i] = p.V
	}

	result := inj.Apply("different_metric", series)

	for i, p := range result[0].Points {
		if p.V != original[i] {
			t.Errorf("metric mismatch: point[%d] modified (got %f, expected %f)", i, p.V, original[i])
		}
	}
}

// --- Task 17: Multiple injections in one run ---

func TestInjector_MultipleInjections(t *testing.T) {
	// Contract: multiple InjectionEntry items can target the same metric but
	// different time windows. Both are applied, and both produce ground truths.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute

	cfg := &InjectionConfig{
		Seed: 100,
		Injections: []InjectionEntry{
			{
				Target:    TargetSpec{Metric: "cpu", Labels: map[string]string{"svc": "a"}},
				Type:      FaultSpike,
				Start:     t0.Add(2 * step),
				End:       t0.Add(3 * step),
				Magnitude: 5.0,
			},
			{
				Target:    TargetSpec{Metric: "cpu", Labels: map[string]string{"svc": "a"}},
				Type:      FaultStep,
				Start:     t0.Add(7 * step),
				End:       t0.Add(9 * step),
				Magnitude: 3.0,
			},
		},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{{
		Labels: map[string]string{"svc": "a", "__name__": "cpu"},
		Points: make([]ingestion.Point, 12),
	}}
	for i := range series[0].Points {
		series[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: 50.0}
	}

	result := inj.Apply("cpu", series)

	// Points 2-3 should be spiked (increased)
	for i := 2; i <= 3; i++ {
		if result[0].Points[i].V <= 50.0 {
			t.Errorf("point[%d] should have spike increase, got %f", i, result[0].Points[i].V)
		}
	}
	// Points 7-9 should be stepped up
	for i := 7; i <= 9; i++ {
		if result[0].Points[i].V <= 50.0 {
			t.Errorf("point[%d] should have step increase, got %f", i, result[0].Points[i].V)
		}
	}
	// Points outside both windows should be unchanged
	for _, i := range []int{0, 1, 4, 5, 6, 10, 11} {
		if result[0].Points[i].V != 50.0 {
			t.Errorf("point[%d] outside both windows: expected 50.0, got %f", i, result[0].Points[i].V)
		}
	}

	// Both injections should produce ground truths
	truths := inj.GroundTruths()
	if len(truths) != 2 {
		t.Fatalf("expected 2 ground truths, got %d", len(truths))
	}
}

func TestInjector_MultipleSeriesMatchingLabels(t *testing.T) {
	// When injection targets labels={svc:a} and multiple series have svc=a,
	// all matching series should be perturbed.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute

	cfg := &InjectionConfig{
		Seed: 42,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "latency", Labels: map[string]string{"svc": "checkout"}},
			Type:      FaultStep,
			Start:     t0,
			End:       t0.Add(5 * step),
			Magnitude: 4.0,
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{
		{Labels: map[string]string{"svc": "checkout", "pod": "pod-1"}, Points: make([]ingestion.Point, 8)},
		{Labels: map[string]string{"svc": "checkout", "pod": "pod-2"}, Points: make([]ingestion.Point, 8)},
		{Labels: map[string]string{"svc": "payments", "pod": "pod-3"}, Points: make([]ingestion.Point, 8)},
	}
	for s := range series {
		for i := range series[s].Points {
			series[s].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: 100.0}
		}
	}

	result := inj.Apply("latency", series)

	// Series 0 and 1 (svc=checkout) should be modified
	for i := 0; i <= 5; i++ {
		if result[0].Points[i].V <= 100.0 {
			t.Errorf("series[0] point[%d]: expected step increase, got %f", i, result[0].Points[i].V)
		}
		if result[1].Points[i].V <= 100.0 {
			t.Errorf("series[1] point[%d]: expected step increase, got %f", i, result[1].Points[i].V)
		}
	}
	// Series 2 (svc=payments) should be unchanged
	for i := range result[2].Points {
		if result[2].Points[i].V != 100.0 {
			t.Errorf("series[2] point[%d]: expected unchanged 100.0, got %f", i, result[2].Points[i].V)
		}
	}
}

// --- Task 17: Ground truth recorded correctly ---

func TestInjector_GroundTruth_Correct(t *testing.T) {
	// Contract: ground truth records target fingerprint, type, window, magnitude.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	start := t0.Add(5 * time.Minute)
	end := t0.Add(15 * time.Minute)

	cfg := &InjectionConfig{
		Seed: 77,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "error_rate", Labels: map[string]string{"service_name": "api"}},
			Type:      FaultRamp,
			Start:     start,
			End:       end,
			Magnitude: 5.0,
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{{
		Labels: map[string]string{"service_name": "api"},
		Points: make([]ingestion.Point, 20),
	}}
	for i := range series[0].Points {
		series[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * time.Minute), V: 10.0}
	}

	inj.Apply("error_rate", series)

	truths := inj.GroundTruths()
	if len(truths) != 1 {
		t.Fatalf("expected 1 ground truth, got %d", len(truths))
	}

	gt := truths[0]
	expectedTarget := Fingerprint("error_rate", map[string]string{"service_name": "api"})
	if gt.Target != expectedTarget {
		t.Errorf("target: got %q, expected %q", gt.Target, expectedTarget)
	}
	if gt.Type != FaultRamp {
		t.Errorf("type: got %q, expected %q", gt.Type, FaultRamp)
	}
	if !gt.Start.Equal(start) {
		t.Errorf("start: got %v, expected %v", gt.Start, start)
	}
	if !gt.End.Equal(end) {
		t.Errorf("end: got %v, expected %v", gt.End, end)
	}
	if gt.Magnitude != 5.0 {
		t.Errorf("magnitude: got %f, expected 5.0", gt.Magnitude)
	}
}

func TestInjector_GroundTruth_Deduplication(t *testing.T) {
	// Contract: ground truth is recorded once per (fingerprint + type + start),
	// not per tick. Calling Apply multiple times with same series should not
	// duplicate ground truths.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	cfg := &InjectionConfig{
		Seed: 1,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "m1", Labels: nil},
			Type:      FaultStep,
			Start:     t0,
			End:       t0.Add(10 * time.Minute),
			Magnitude: 2.0,
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{makeConstantSeries(t0, time.Minute, 5, 10.0)}

	// Call Apply multiple times (simulating multiple ticks querying overlapping chunks)
	inj.Apply("m1", series)
	inj.Apply("m1", series)
	inj.Apply("m1", series)

	truths := inj.GroundTruths()
	if len(truths) != 1 {
		t.Errorf("expected 1 deduplicated ground truth, got %d", len(truths))
	}
}

// --- Task 17: LoadConfig ---

func TestLoadConfig_ValidYAML(t *testing.T) {
	yaml := `
seed: 42
injections:
  - target:
      metric: error_rate_by_service
      labels:
        service_name: checkout-api
    type: ramp
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 5.0
  - target:
      metric: latency_p99
    type: spike
    start: "2026-06-10T03:05:00Z"
    end: "2026-06-10T03:06:00Z"
    magnitude: 3.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "inject.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Seed != 42 {
		t.Errorf("seed: got %d, expected 42", cfg.Seed)
	}
	if len(cfg.Injections) != 2 {
		t.Fatalf("injections: got %d, expected 2", len(cfg.Injections))
	}
	if cfg.Injections[0].Target.Metric != "error_rate_by_service" {
		t.Errorf("injection[0].target.metric: got %q", cfg.Injections[0].Target.Metric)
	}
	if cfg.Injections[0].Type != FaultRamp {
		t.Errorf("injection[0].type: got %q", cfg.Injections[0].Type)
	}
	if cfg.Injections[0].Magnitude != 5.0 {
		t.Errorf("injection[0].magnitude: got %f", cfg.Injections[0].Magnitude)
	}
}

func TestLoadConfig_InvalidType(t *testing.T) {
	yaml := `
seed: 1
injections:
  - target:
      metric: m1
    type: invalid_type
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 1.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for invalid type, got nil")
	}
}

func TestLoadConfig_EndBeforeStart(t *testing.T) {
	yaml := `
seed: 1
injections:
  - target:
      metric: m1
    type: spike
    start: "2026-06-10T03:20:00Z"
    end: "2026-06-10T03:00:00Z"
    magnitude: 1.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for end before start, got nil")
	}
}

func TestLoadConfig_MissingMetric(t *testing.T) {
	yaml := `
seed: 1
injections:
  - target:
      metric: ""
    type: step
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 2.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for missing metric, got nil")
	}
}

func TestLoadConfig_ZeroMagnitudeNonSilence(t *testing.T) {
	yaml := `
seed: 1
injections:
  - target:
      metric: m1
    type: ramp
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 0.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for zero magnitude on non-silence type, got nil")
	}
}

func TestLoadConfig_SilenceZeroMagnitudeOK(t *testing.T) {
	yaml := `
seed: 1
injections:
  - target:
      metric: m1
    type: silence
    start: "2026-06-10T03:00:00Z"
    end: "2026-06-10T03:20:00Z"
    magnitude: 0.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	_, err := LoadConfig(path)
	if err != nil {
		t.Errorf("silence with magnitude=0 should be valid, got: %v", err)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/inject.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// --- Fingerprint ---

func TestFingerprint_Normalization(t *testing.T) {
	// Contract: fingerprint is "metric{key1=val1,key2=val2}" with labels sorted by key.
	// __name__ is excluded.
	cases := []struct {
		name     string
		metric   string
		labels   map[string]string
		expected string
	}{
		{
			name:     "no labels",
			metric:   "cpu_usage",
			labels:   nil,
			expected: "cpu_usage",
		},
		{
			name:     "empty labels map",
			metric:   "cpu_usage",
			labels:   map[string]string{},
			expected: "cpu_usage",
		},
		{
			name:     "single label",
			metric:   "error_rate",
			labels:   map[string]string{"service_name": "checkout"},
			expected: "error_rate{service_name=checkout}",
		},
		{
			name:     "multiple labels sorted",
			metric:   "latency",
			labels:   map[string]string{"zone": "us-east-1", "app": "web", "env": "prd"},
			expected: "latency{app=web,env=prd,zone=us-east-1}",
		},
		{
			name:     "__name__ excluded",
			metric:   "requests_total",
			labels:   map[string]string{"__name__": "requests_total", "method": "GET"},
			expected: "requests_total{method=GET}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Fingerprint(tc.metric, tc.labels)
			if got != tc.expected {
				t.Errorf("Fingerprint(%q, %v) = %q, want %q", tc.metric, tc.labels, got, tc.expected)
			}
		})
	}
}

func TestFingerprint_OrderIndependence(t *testing.T) {
	// Same labels in different insertion order must produce same fingerprint.
	labels1 := map[string]string{"b": "2", "a": "1", "c": "3"}
	labels2 := map[string]string{"c": "3", "a": "1", "b": "2"}
	fp1 := Fingerprint("m", labels1)
	fp2 := Fingerprint("m", labels2)
	if fp1 != fp2 {
		t.Errorf("order-dependent fingerprint: %q != %q", fp1, fp2)
	}
}
