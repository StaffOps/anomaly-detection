package inject

import (
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// --- Task 19: Integration — replay+injection end-to-end over a KNOWN synthetic base series ---
//
// This test verifies the full pipeline: Injector applies a ramp fault to a clean
// synthetic series, we simulate what the detector would see (the perturbed values),
// and Score produces a populated, internally consistent scoring block.
//
// Note: we don't test the actual detector here (that's the replay engine's job).
// We test that the injection→ground_truth→scoring pipeline is end-to-end correct.

func TestIntegration_RampInjection_ScoringPopulated(t *testing.T) {
	// Setup: a clean series with 60 points at 30s intervals (30min window).
	// Inject a ramp from minute 10 to minute 20.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	nPoints := 60
	baseValue := 100.0

	// Create the injection config.
	rampStart := t0.Add(10 * time.Minute)
	rampEnd := t0.Add(20 * time.Minute)
	cfg := &InjectionConfig{
		Seed: 42,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "error_rate", Labels: map[string]string{"svc": "api"}},
			Type:      FaultRamp,
			Start:     rampStart,
			End:       rampEnd,
			Magnitude: 5.0,
		}},
	}
	inj := NewInjector(cfg)

	// Create clean series.
	series := []ingestion.TimeSeries{{
		Labels: map[string]string{"svc": "api"},
		Points: make([]ingestion.Point, nPoints),
	}}
	for i := range series[0].Points {
		series[0].Points[i] = ingestion.Point{
			T: t0.Add(time.Duration(i) * step),
			V: baseValue,
		}
	}

	// Apply injection.
	result := inj.Apply("error_rate", series)
	truths := inj.GroundTruths()

	// Verify ground truth is correct.
	if len(truths) != 1 {
		t.Fatalf("expected 1 ground truth, got %d", len(truths))
	}
	gt := truths[0]
	if gt.Type != FaultRamp {
		t.Errorf("ground truth type: got %q, expected %q", gt.Type, FaultRamp)
	}

	// Verify the series was actually perturbed.
	// Points in the ramp window should show increasing values.
	rampEndIdx := int(rampEnd.Sub(t0) / step) // point 40
	maxIncrease := result[0].Points[rampEndIdx].V - baseValue
	if maxIncrease <= 0 {
		t.Fatalf("ramp did not increase values: max point value=%f, base=%f",
			result[0].Points[rampEndIdx].V, baseValue)
	}

	// Simulate detection: pretend the detector caught anomalies at the latter part
	// of the ramp (realistic — adaptive detector needs some deviation to trigger).
	// We simulate detection starting at the midpoint of the ramp.
	detectionStart := rampStart.Add(5 * time.Minute) // midpoint
	grace := step                                    // 30s grace (1 tick)

	detected := []DetectedAnomaly{
		{Metric: "error_rate", Labels: map[string]string{"svc": "api"}, Timestamp: detectionStart},
		{Metric: "error_rate", Labels: map[string]string{"svc": "api"}, Timestamp: detectionStart.Add(step)},
		{Metric: "error_rate", Labels: map[string]string{"svc": "api"}, Timestamp: detectionStart.Add(2 * step)},
	}

	scoring := Score(detected, truths, grace)

	// The scoring block should be populated and internally consistent.
	if scoring == nil {
		t.Fatal("Score returned nil")
	}

	// All detections are within the truth window → all TP.
	if scoring.TP != 3 {
		t.Errorf("TP: expected 3, got %d", scoring.TP)
	}
	if scoring.FP != 0 {
		t.Errorf("FP: expected 0, got %d", scoring.FP)
	}
	if scoring.FN != 0 {
		t.Errorf("FN: expected 0 (truth detected), got %d", scoring.FN)
	}

	// Recall = 1.0 (truth detected)
	if scoring.Recall != 1.0 {
		t.Errorf("Recall: expected 1.0, got %f", scoring.Recall)
	}
	// Precision = 1.0 (no FP)
	if scoring.Precision != 1.0 {
		t.Errorf("Precision: expected 1.0, got %f", scoring.Precision)
	}
	// F1 = 1.0
	if scoring.F1 != 1.0 {
		t.Errorf("F1: expected 1.0, got %f", scoring.F1)
	}

	// recall_by_type should have ramp = 1.0
	if scoring.RecallByType["ramp"] != 1.0 {
		t.Errorf("recall_by_type[ramp]: expected 1.0, got %f", scoring.RecallByType["ramp"])
	}

	// Detection latency: first detection at rampStart+5min → latency = 5min = 300s
	key := gt.Target + "/ramp"
	lat, ok := scoring.DetectionLatency[key]
	if !ok {
		t.Fatalf("detection_latency missing key %q (available: %v)", key, scoring.DetectionLatency)
	}
	expectedLat := 300.0 // 5 minutes in seconds
	if lat != expectedLat {
		t.Errorf("detection_latency[%s]: expected %f, got %f", key, expectedLat, lat)
	}

	// FPCaveat must always be populated.
	if scoring.FPCaveat == "" {
		t.Error("FPCaveat should be populated")
	}

	// Internal consistency: TP + FP = total anomalies, TP > 0 → FN must equal total truths - detected
	if scoring.TP+scoring.FP != len(detected) {
		t.Errorf("TP(%d) + FP(%d) != len(detected)(%d)", scoring.TP, scoring.FP, len(detected))
	}
	// detected truths = truths - FN
	if scoring.FN != len(truths)-1 { // 1 truth detected → FN = 0
		t.Errorf("FN inconsistency: FN=%d, truths=%d, detected=%d", scoring.FN, len(truths), 1)
	}

	t.Logf("Integration result: Recall=%.2f, Precision=%.2f, F1=%.2f, TP=%d, FP=%d, FN=%d, Latency=%.0fs",
		scoring.Recall, scoring.Precision, scoring.F1, scoring.TP, scoring.FP, scoring.FN, lat)
}

func TestIntegration_RampNotDetected_RecallZero(t *testing.T) {
	// Scenario: ramp is injected but detector finds nothing → FN=1, recall=0.
	// This is the "EWMA chases the ramp" scenario that P0.1 is designed to measure.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second

	cfg := &InjectionConfig{
		Seed: 99,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "latency_p99", Labels: map[string]string{"svc": "web"}},
			Type:      FaultRamp,
			Start:     t0.Add(5 * time.Minute),
			End:       t0.Add(20 * time.Minute),
			Magnitude: 2.0, // gentle ramp — harder to detect
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{{
		Labels: map[string]string{"svc": "web"},
		Points: make([]ingestion.Point, 60),
	}}
	for i := range series[0].Points {
		series[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: 200.0}
	}
	inj.Apply("latency_p99", series)
	truths := inj.GroundTruths()

	// No anomalies detected (detector blind to gentle ramp).
	detected := []DetectedAnomaly{}
	scoring := Score(detected, truths, step)

	if scoring.Recall != 0.0 {
		t.Errorf("expected recall=0.0 (ramp undetected), got %f", scoring.Recall)
	}
	if scoring.FN != 1 {
		t.Errorf("expected FN=1, got %d", scoring.FN)
	}
	if scoring.RecallByType["ramp"] != 0.0 {
		t.Errorf("recall_by_type[ramp]: expected 0.0, got %f", scoring.RecallByType["ramp"])
	}

	t.Logf("Ramp undetected (EWMA-chase scenario): Recall=%.2f, FN=%d — this is the expected V1 baseline for ramp-blindness measurement",
		scoring.Recall, scoring.FN)
}

// --- Task 20: Silence case — confirm recall ~0 is expected V1 result ---
//
// The current detector evaluates present values only. When a series disappears
// (silence injection), there's nothing to evaluate → no anomaly is produced.
// Recall ~0 for silence is the EXPECTED V1 result, not a harness bug.
// This gives a baseline for P2.10 (dead-man's-switch feature).

func TestIntegration_Silence_RecallZero_ExpectedV1(t *testing.T) {
	// Inject silence: remove all points in a window. The detector cannot detect
	// what isn't there. This test documents that recall=0 for silence is correct
	// V1 behavior, not a test failure.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	nPoints := 60

	cfg := &InjectionConfig{
		Seed: 7,
		Injections: []InjectionEntry{{
			Target:    TargetSpec{Metric: "requests_total", Labels: map[string]string{"svc": "checkout"}},
			Type:      FaultSilence,
			Start:     t0.Add(10 * time.Minute),
			End:       t0.Add(25 * time.Minute),
			Magnitude: 0.0, // irrelevant for silence
		}},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{{
		Labels: map[string]string{"svc": "checkout"},
		Points: make([]ingestion.Point, nPoints),
	}}
	for i := range series[0].Points {
		series[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: 1000.0}
	}

	result := inj.Apply("requests_total", series)
	truths := inj.GroundTruths()

	// Verify points were actually removed from the silence window.
	silenceStart := t0.Add(10 * time.Minute)
	silenceEnd := t0.Add(25 * time.Minute)
	for _, p := range result[0].Points {
		if !p.T.Before(silenceStart) && !p.T.After(silenceEnd) {
			t.Fatalf("point at %v should have been removed by silence injection", p.T)
		}
	}

	// The detector processes present values only — no data means no anomaly.
	// This simulates the expected V1 detector output: NOTHING detected.
	detected := []DetectedAnomaly{}
	scoring := Score(detected, truths, step)

	// EXPECTED RESULT: recall = 0, FN = 1.
	// This is NOT a harness bug. It's the baseline measurement for P2.10.
	if scoring.Recall != 0.0 {
		t.Errorf("silence recall: expected 0.0 (V1 detector is blind to absence), got %f", scoring.Recall)
	}
	if scoring.FN != 1 {
		t.Errorf("silence FN: expected 1, got %d", scoring.FN)
	}
	if scoring.RecallByType["silence"] != 0.0 {
		t.Errorf("recall_by_type[silence]: expected 0.0, got %f", scoring.RecallByType["silence"])
	}
	if scoring.TP != 0 {
		t.Errorf("silence TP: expected 0, got %d", scoring.TP)
	}
	if scoring.FP != 0 {
		t.Errorf("silence FP: expected 0, got %d", scoring.FP)
	}

	t.Logf("Silence injection V1 result (EXPECTED baseline): Recall=%.2f, FN=%d — "+
		"detector cannot detect absence of signal. P2.10 (dead-man's-switch) will address this.",
		scoring.Recall, scoring.FN)
}

func TestIntegration_MultipleTypes_MixedRecall(t *testing.T) {
	// Integration test with spike (easy to detect) + silence (impossible to detect)
	// → demonstrates recall_by_type divergence.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second

	cfg := &InjectionConfig{
		Seed: 55,
		Injections: []InjectionEntry{
			{
				Target:    TargetSpec{Metric: "cpu", Labels: map[string]string{"pod": "web-1"}},
				Type:      FaultSpike,
				Start:     t0.Add(5 * time.Minute),
				End:       t0.Add(6 * time.Minute),
				Magnitude: 10.0, // huge spike — easy to detect
			},
			{
				Target:    TargetSpec{Metric: "cpu", Labels: map[string]string{"pod": "web-2"}},
				Type:      FaultSilence,
				Start:     t0.Add(10 * time.Minute),
				End:       t0.Add(20 * time.Minute),
				Magnitude: 0.0,
			},
		},
	}
	inj := NewInjector(cfg)

	series := []ingestion.TimeSeries{
		{Labels: map[string]string{"pod": "web-1"}, Points: make([]ingestion.Point, 60)},
		{Labels: map[string]string{"pod": "web-2"}, Points: make([]ingestion.Point, 60)},
	}
	for s := range series {
		for i := range series[s].Points {
			series[s].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: 50.0}
		}
	}

	inj.Apply("cpu", series)
	truths := inj.GroundTruths()

	if len(truths) != 2 {
		t.Fatalf("expected 2 ground truths, got %d", len(truths))
	}

	// Simulate: spike is detected, silence is not.
	detected := []DetectedAnomaly{
		{Metric: "cpu", Labels: map[string]string{"pod": "web-1"}, Timestamp: t0.Add(5*time.Minute + 15*time.Second)},
	}
	scoring := Score(detected, truths, step)

	// Overall recall: 1/2 = 0.5
	if scoring.Recall != 0.5 {
		t.Errorf("overall recall: expected 0.5, got %f", scoring.Recall)
	}
	if scoring.RecallByType["spike"] != 1.0 {
		t.Errorf("recall_by_type[spike]: expected 1.0, got %f", scoring.RecallByType["spike"])
	}
	if scoring.RecallByType["silence"] != 0.0 {
		t.Errorf("recall_by_type[silence]: expected 0.0, got %f", scoring.RecallByType["silence"])
	}

	t.Logf("Mixed-type result: recall_spike=%.0f%%, recall_silence=%.0f%% — "+
		"demonstrates type-dependent detector capabilities",
		scoring.RecallByType["spike"]*100, scoring.RecallByType["silence"]*100)
}

// --- Task 19: Verify BuildInjectionResult integration with scorer ---

func TestIntegration_BuildInjectionResult_ConsistentWithGroundTruths(t *testing.T) {
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	cfg := &InjectionConfig{
		Seed: 123,
		Injections: []InjectionEntry{
			{
				Target:    TargetSpec{Metric: "m1", Labels: map[string]string{"a": "1"}},
				Type:      FaultStep,
				Start:     t0,
				End:       t0.Add(10 * time.Minute),
				Magnitude: 3.0,
			},
			{
				Target:    TargetSpec{Metric: "m2", Labels: map[string]string{"b": "2"}},
				Type:      FaultRamp,
				Start:     t0.Add(5 * time.Minute),
				End:       t0.Add(15 * time.Minute),
				Magnitude: 4.0,
			},
		},
	}
	inj := NewInjector(cfg)

	// Apply both injections.
	s1 := []ingestion.TimeSeries{{
		Labels: map[string]string{"a": "1"},
		Points: make([]ingestion.Point, 30),
	}}
	s2 := []ingestion.TimeSeries{{
		Labels: map[string]string{"b": "2"},
		Points: make([]ingestion.Point, 30),
	}}
	for i := range s1[0].Points {
		s1[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * time.Minute), V: 10.0}
	}
	for i := range s2[0].Points {
		s2[0].Points[i] = ingestion.Point{T: t0.Add(time.Duration(i) * time.Minute), V: 20.0}
	}
	inj.Apply("m1", s1)
	inj.Apply("m2", s2)

	truths := inj.GroundTruths()
	injResult := BuildInjectionResult(inj.Seed(), truths)

	// Seed matches
	if injResult.Seed != 123 {
		t.Errorf("seed mismatch: got %d, expected 123", injResult.Seed)
	}
	// Ground truths count matches
	if len(injResult.GroundTruths) != len(truths) {
		t.Errorf("ground truth count: got %d, expected %d", len(injResult.GroundTruths), len(truths))
	}
	// Each ground truth JSON has correct type
	for _, gt := range injResult.GroundTruths {
		if gt.Type != "step" && gt.Type != "ramp" {
			t.Errorf("unexpected type: %q", gt.Type)
		}
		if gt.Target == "" {
			t.Error("empty target in ground truth JSON")
		}
		if gt.Start == "" || gt.End == "" {
			t.Error("empty start/end in ground truth JSON")
		}
	}
}
