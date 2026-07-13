package inject

import (
	"math"
	"testing"
	"time"
)

// --- Task 18: Scorer — TP/FP/FN classification ---

func TestScore_AllTP(t *testing.T) {
	// All anomalies match a ground truth → TP=N, FP=0, FN=0.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "cpu{svc=a}",
		Type:   FaultSpike,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{
		{Metric: "cpu", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(2 * time.Minute)},
		{Metric: "cpu", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(4 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)

	if result.TP != 2 {
		t.Errorf("TP: expected 2, got %d", result.TP)
	}
	if result.FP != 0 {
		t.Errorf("FP: expected 0, got %d", result.FP)
	}
	if result.FN != 0 {
		t.Errorf("FN: expected 0, got %d", result.FN)
	}
	if result.Recall != 1.0 {
		t.Errorf("Recall: expected 1.0, got %f", result.Recall)
	}
	if result.Precision != 1.0 {
		t.Errorf("Precision: expected 1.0, got %f", result.Precision)
	}
	if result.F1 != 1.0 {
		t.Errorf("F1: expected 1.0, got %f", result.F1)
	}
}

func TestScore_AllFP(t *testing.T) {
	// All anomalies are outside any ground truth window → all FP.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "cpu{svc=a}",
		Type:   FaultStep,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{
		// Detected at 10min — outside [0, 5min + 1min grace]
		{Metric: "cpu", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(10 * time.Minute)},
		{Metric: "cpu", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(15 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)

	if result.TP != 0 {
		t.Errorf("TP: expected 0, got %d", result.TP)
	}
	if result.FP != 2 {
		t.Errorf("FP: expected 2, got %d", result.FP)
	}
	if result.FN != 1 {
		t.Errorf("FN: expected 1, got %d", result.FN)
	}
	if result.Recall != 0.0 {
		t.Errorf("Recall: expected 0.0, got %f", result.Recall)
	}
	if result.Precision != 0.0 {
		t.Errorf("Precision: expected 0.0, got %f", result.Precision)
	}
}

func TestScore_AllFN(t *testing.T) {
	// No anomalies detected, but truths exist → all FN.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{
		{Target: "cpu{svc=a}", Type: FaultRamp, Start: t0, End: t0.Add(10 * time.Minute)},
		{Target: "mem{svc=b}", Type: FaultSilence, Start: t0, End: t0.Add(5 * time.Minute)},
	}
	anomalies := []DetectedAnomaly{} // nothing detected

	result := Score(anomalies, truths, grace)

	if result.TP != 0 {
		t.Errorf("TP: expected 0, got %d", result.TP)
	}
	if result.FP != 0 {
		t.Errorf("FP: expected 0, got %d", result.FP)
	}
	if result.FN != 2 {
		t.Errorf("FN: expected 2, got %d", result.FN)
	}
	if result.Recall != 0.0 {
		t.Errorf("Recall: expected 0.0, got %f", result.Recall)
	}
}

func TestScore_MixedTPFPFN(t *testing.T) {
	// Realistic scenario: 2 truths, 3 anomalies (1 TP match truth1, 1 FP, 1 truth undetected)
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{
		{Target: "cpu{svc=a}", Type: FaultSpike, Start: t0, End: t0.Add(5 * time.Minute)},
		{Target: "mem{svc=b}", Type: FaultRamp, Start: t0.Add(10 * time.Minute), End: t0.Add(20 * time.Minute)},
	}
	anomalies := []DetectedAnomaly{
		// Matches truth[0]
		{Metric: "cpu", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(3 * time.Minute)},
		// Outside any truth window (FP)
		{Metric: "disk", Labels: map[string]string{"svc": "c"}, Timestamp: t0.Add(7 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)

	if result.TP != 1 {
		t.Errorf("TP: expected 1, got %d", result.TP)
	}
	if result.FP != 1 {
		t.Errorf("FP: expected 1, got %d", result.FP)
	}
	if result.FN != 1 {
		t.Errorf("FN: expected 1, got %d", result.FN)
	}
	// recall = 1 detected / 2 truths = 0.5
	if math.Abs(result.Recall-0.5) > 0.001 {
		t.Errorf("Recall: expected 0.5, got %f", result.Recall)
	}
	// precision = 1 TP / (1 TP + 1 FP) = 0.5
	if math.Abs(result.Precision-0.5) > 0.001 {
		t.Errorf("Precision: expected 0.5, got %f", result.Precision)
	}
	// F1 = 2*0.5*0.5/(0.5+0.5) = 0.5
	if math.Abs(result.F1-0.5) > 0.001 {
		t.Errorf("F1: expected 0.5, got %f", result.F1)
	}
}

// --- Task 18: Grace window boundary ---

func TestScore_GraceWindow_ExactBoundary(t *testing.T) {
	// Contract: detection within [start, end + grace] is TP.
	// Detection at end+grace is TP. Detection at end+grace+1ns is FP.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := 30 * time.Second

	truths := []GroundTruth{{
		Target: "m{x=y}",
		Type:   FaultSpike,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}

	// Exactly at end + grace → should be TP
	atGrace := []DetectedAnomaly{{
		Metric:    "m",
		Labels:    map[string]string{"x": "y"},
		Timestamp: t0.Add(5*time.Minute + grace), // exactly end + grace
	}}
	result := Score(atGrace, truths, grace)
	if result.TP != 1 {
		t.Errorf("at end+grace: expected TP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}

	// 1 nanosecond past grace → should be FP
	pastGrace := []DetectedAnomaly{{
		Metric:    "m",
		Labels:    map[string]string{"x": "y"},
		Timestamp: t0.Add(5*time.Minute + grace + time.Nanosecond),
	}}
	result2 := Score(pastGrace, truths, grace)
	if result2.FP != 1 {
		t.Errorf("past grace: expected FP=1, got TP=%d, FP=%d", result2.TP, result2.FP)
	}
}

func TestScore_GraceWindow_BeforeStart(t *testing.T) {
	// Detection before truth.Start is FP (not matching even with grace).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "m{x=y}",
		Type:   FaultStep,
		Start:  t0.Add(5 * time.Minute),
		End:    t0.Add(10 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{{
		Metric:    "m",
		Labels:    map[string]string{"x": "y"},
		Timestamp: t0.Add(4*time.Minute + 59*time.Second), // 1s before start
	}}

	result := Score(anomalies, truths, grace)
	if result.FP != 1 {
		t.Errorf("before start: expected FP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}
}

func TestScore_GraceWindow_AtStart(t *testing.T) {
	// Detection exactly at truth.Start is TP.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "m",
		Type:   FaultSpike,
		Start:  t0.Add(5 * time.Minute),
		End:    t0.Add(10 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{{
		Metric:    "m",
		Labels:    nil,
		Timestamp: t0.Add(5 * time.Minute), // exactly at start
	}}

	result := Score(anomalies, truths, grace)
	if result.TP != 1 {
		t.Errorf("at start: expected TP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}
}

// --- Task 18: recall_by_type ---

func TestScore_RecallByType(t *testing.T) {
	// Contract: recall broken down by fault type.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{
		{Target: "m{t=spike}", Type: FaultSpike, Start: t0, End: t0.Add(5 * time.Minute)},
		{Target: "m{t=ramp}", Type: FaultRamp, Start: t0, End: t0.Add(10 * time.Minute)},
		{Target: "m{t=step}", Type: FaultStep, Start: t0, End: t0.Add(5 * time.Minute)},
		{Target: "m{t=silence}", Type: FaultSilence, Start: t0, End: t0.Add(5 * time.Minute)},
	}
	// Only spike and step are detected
	anomalies := []DetectedAnomaly{
		{Metric: "m", Labels: map[string]string{"t": "spike"}, Timestamp: t0.Add(2 * time.Minute)},
		{Metric: "m", Labels: map[string]string{"t": "step"}, Timestamp: t0.Add(3 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)

	if result.RecallByType["spike"] != 1.0 {
		t.Errorf("recall_by_type[spike]: expected 1.0, got %f", result.RecallByType["spike"])
	}
	if result.RecallByType["ramp"] != 0.0 {
		t.Errorf("recall_by_type[ramp]: expected 0.0, got %f", result.RecallByType["ramp"])
	}
	if result.RecallByType["step"] != 1.0 {
		t.Errorf("recall_by_type[step]: expected 1.0, got %f", result.RecallByType["step"])
	}
	if result.RecallByType["silence"] != 0.0 {
		t.Errorf("recall_by_type[silence]: expected 0.0, got %f", result.RecallByType["silence"])
	}

	// Overall recall: 2 detected out of 4
	if math.Abs(result.Recall-0.5) > 0.001 {
		t.Errorf("overall recall: expected 0.5, got %f", result.Recall)
	}
}

func TestScore_RecallByType_MultipleSameType(t *testing.T) {
	// Multiple truths of same type: recall_by_type = detected/total for that type.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{
		{Target: "m{svc=a}", Type: FaultSpike, Start: t0, End: t0.Add(5 * time.Minute)},
		{Target: "m{svc=b}", Type: FaultSpike, Start: t0, End: t0.Add(5 * time.Minute)},
		{Target: "m{svc=c}", Type: FaultSpike, Start: t0, End: t0.Add(5 * time.Minute)},
	}
	// Detect only 1 out of 3 spike truths
	anomalies := []DetectedAnomaly{
		{Metric: "m", Labels: map[string]string{"svc": "a"}, Timestamp: t0.Add(2 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)
	expectedRecall := 1.0 / 3.0
	if math.Abs(result.RecallByType["spike"]-expectedRecall) > 0.001 {
		t.Errorf("recall_by_type[spike]: expected %f, got %f", expectedRecall, result.RecallByType["spike"])
	}
}

// --- Task 18: detection_latency ---

func TestScore_DetectionLatency(t *testing.T) {
	// Contract: detection_latency = first_TP.timestamp - truth.Start (per detected truth).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "m{svc=x}",
		Type:   FaultRamp,
		Start:  t0,
		End:    t0.Add(10 * time.Minute),
	}}
	// Multiple detections of same truth — latency should use the earliest
	anomalies := []DetectedAnomaly{
		{Metric: "m", Labels: map[string]string{"svc": "x"}, Timestamp: t0.Add(7 * time.Minute)},
		{Metric: "m", Labels: map[string]string{"svc": "x"}, Timestamp: t0.Add(3 * time.Minute)}, // earlier
		{Metric: "m", Labels: map[string]string{"svc": "x"}, Timestamp: t0.Add(9 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)

	key := "m{svc=x}/ramp"
	lat, ok := result.DetectionLatency[key]
	if !ok {
		t.Fatalf("detection_latency missing key %q", key)
	}
	// Earliest detection is at 3min → latency = 3*60 = 180 seconds
	expected := 180.0
	if math.Abs(lat-expected) > 0.001 {
		t.Errorf("detection_latency[%s]: expected %f, got %f", key, expected, lat)
	}
}

func TestScore_DetectionLatency_ImmediateDetection(t *testing.T) {
	// Detection at exact start → latency = 0.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "m",
		Type:   FaultStep,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{{
		Metric: "m", Labels: nil, Timestamp: t0,
	}}

	result := Score(anomalies, truths, grace)
	lat := result.DetectionLatency["m/step"]
	if lat != 0.0 {
		t.Errorf("immediate detection latency: expected 0.0, got %f", lat)
	}
}

// --- Task 18: label-mismatch cases (fingerprint normalization) ---

func TestScore_LabelMismatch_NotMatched(t *testing.T) {
	// Contract: anomaly fingerprint must exactly match truth target.
	// Different labels → FP even if metric name matches.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "cpu{svc=checkout}", // normalized target
		Type:   FaultSpike,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{
		// Same metric, different label value
		{Metric: "cpu", Labels: map[string]string{"svc": "payments"}, Timestamp: t0.Add(2 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)
	if result.FP != 1 {
		t.Errorf("label mismatch: expected FP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}
	if result.FN != 1 {
		t.Errorf("label mismatch: expected FN=1, got %d", result.FN)
	}
}

func TestScore_LabelNormalization_ExtraLabelsOnAnomaly(t *testing.T) {
	// Anomaly has extra labels beyond what truth has. Fingerprint includes ALL
	// labels → won't match unless truth was recorded with all labels.
	// This is correct behavior per design: fingerprint is normalized from the actual series.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		// Truth was recorded with fingerprint including both labels
		Target: "cpu{pod=p1,svc=checkout}",
		Type:   FaultStep,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{
		// Anomaly with same labels → match
		{Metric: "cpu", Labels: map[string]string{"svc": "checkout", "pod": "p1"}, Timestamp: t0.Add(3 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)
	if result.TP != 1 {
		t.Errorf("matching with all labels: expected TP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}
}

func TestScore_LabelNormalization_NameLabelExcluded(t *testing.T) {
	// __name__ in anomaly labels should be excluded from fingerprint (per Fingerprint contract).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	grace := time.Minute

	truths := []GroundTruth{{
		Target: "cpu{svc=a}",
		Type:   FaultSpike,
		Start:  t0,
		End:    t0.Add(5 * time.Minute),
	}}
	anomalies := []DetectedAnomaly{
		// Has __name__ which should be ignored in fingerprint
		{Metric: "cpu", Labels: map[string]string{"__name__": "cpu", "svc": "a"}, Timestamp: t0.Add(2 * time.Minute)},
	}

	result := Score(anomalies, truths, grace)
	if result.TP != 1 {
		t.Errorf("__name__ excluded: expected TP=1, got TP=%d, FP=%d", result.TP, result.FP)
	}
}

// --- Task 18: Edge cases ---

func TestScore_EmptyBoth(t *testing.T) {
	// No anomalies, no truths → clean baseline (no metrics meaningful).
	result := Score(nil, nil, time.Minute)
	if result.TP != 0 || result.FP != 0 || result.FN != 0 {
		t.Errorf("empty both: expected all zeros, got TP=%d FP=%d FN=%d", result.TP, result.FP, result.FN)
	}
	if result.Precision != 0 || result.Recall != 0 {
		t.Errorf("empty both: precision=%f recall=%f (expected 0)", result.Precision, result.Recall)
	}
}

func TestScore_NoTruths_AllFP(t *testing.T) {
	// Anomalies with no truths → all are FP (the "inject=none" FP baseline scenario).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	anomalies := []DetectedAnomaly{
		{Metric: "cpu", Labels: nil, Timestamp: t0},
		{Metric: "mem", Labels: nil, Timestamp: t0.Add(time.Minute)},
	}

	result := Score(anomalies, nil, time.Minute)
	if result.FP != 2 {
		t.Errorf("no truths: expected FP=2, got %d", result.FP)
	}
	if result.FN != 0 {
		t.Errorf("no truths: expected FN=0, got %d", result.FN)
	}
}

func TestScore_FPCaveat_Present(t *testing.T) {
	// Contract: FPCaveat is always present in the result.
	result := Score(nil, nil, time.Minute)
	if result.FPCaveat == "" {
		t.Error("FPCaveat should always be populated")
	}
}

// --- BuildInjectionResult ---

func TestBuildInjectionResult(t *testing.T) {
	truths := []GroundTruth{
		{
			Target:    "error_rate{svc=api}",
			Type:      FaultRamp,
			Start:     time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC),
			End:       time.Date(2026, 6, 10, 3, 20, 0, 0, time.UTC),
			Magnitude: 5.0,
		},
	}

	result := BuildInjectionResult(42, truths)

	if result.Seed != 42 {
		t.Errorf("seed: got %d, expected 42", result.Seed)
	}
	if len(result.GroundTruths) != 1 {
		t.Fatalf("ground_truths: got %d, expected 1", len(result.GroundTruths))
	}
	gt := result.GroundTruths[0]
	if gt.Target != "error_rate{svc=api}" {
		t.Errorf("target: got %q", gt.Target)
	}
	if gt.Type != "ramp" {
		t.Errorf("type: got %q", gt.Type)
	}
	if gt.Start != "2026-06-10T03:00:00Z" {
		t.Errorf("start: got %q", gt.Start)
	}
	if gt.End != "2026-06-10T03:20:00Z" {
		t.Errorf("end: got %q", gt.End)
	}
	if gt.Magnitude != 5.0 {
		t.Errorf("magnitude: got %f", gt.Magnitude)
	}
}

func TestBuildInjectionResult_Empty(t *testing.T) {
	result := BuildInjectionResult(0, nil)
	if result.Seed != 0 {
		t.Errorf("seed: got %d", result.Seed)
	}
	if len(result.GroundTruths) != 0 {
		t.Errorf("ground_truths: expected empty, got %d", len(result.GroundTruths))
	}
}
