package detection_test

import (
	"context"
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/baseline"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// fakeEvaluator implements baseline.Evaluator for testing.
type fakeEvaluator struct {
	results map[string]*baseline.Result
	err     error
}

func (f *fakeEvaluator) Evaluate(_ context.Context, metric string, _ map[string]string, _ float64) (*baseline.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	if r, ok := f.results[metric]; ok {
		return r, nil
	}
	return &baseline.Result{IsWarmingUp: true}, nil
}

func sampleSet(values ...float64) []ingestion.Sample {
	samples := make([]ingestion.Sample, len(values))
	for i, v := range values {
		samples[i] = ingestion.Sample{
			Labels: map[string]string{"pod": "api"},
			Value:  v,
		}
	}
	return samples
}

// ─── AdaptiveDetector tests ──────────────────────────────────────────────────

func TestAdaptiveDetector_WarmingUp_NoAnomalies(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsWarmingUp: true, Value: 0.5},
	}}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, _ := d.Evaluate(context.Background(), "cpu", sampleSet(0.5))
	if len(anomalies) != 0 {
		t.Errorf("warming-up baseline should produce no anomalies, got %d", len(anomalies))
	}
}

func TestAdaptiveDetector_Anomaly_Warning(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsAnomaly: true, ZScore: 3.5, Value: 0.9, Mean: 0.2, Stddev: 0.1},
	}}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, _ := d.Evaluate(context.Background(), "cpu", sampleSet(0.9))
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Severity != "warning" {
		t.Errorf("z=3.5 should be warning, got %q", anomalies[0].Severity)
	}
	if anomalies[0].Detector != "adaptive" {
		t.Errorf("detector should be adaptive, got %q", anomalies[0].Detector)
	}
	if anomalies[0].Signal != "metrics" {
		t.Errorf("signal should be metrics, got %q", anomalies[0].Signal)
	}
}

func TestAdaptiveDetector_Anomaly_Critical(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsAnomaly: true, ZScore: 6.0, Value: 1.0, Mean: 0.1, Stddev: 0.05},
	}}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, _ := d.Evaluate(context.Background(), "cpu", sampleSet(1.0))
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Severity != "critical" {
		t.Errorf("z=6.0 should be critical, got %q", anomalies[0].Severity)
	}
}

func TestAdaptiveDetector_EvaluatorError_Skipped(t *testing.T) {
	eval := &fakeEvaluator{err: context.DeadlineExceeded}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, _ := d.Evaluate(context.Background(), "cpu", sampleSet(0.9, 0.95))
	// Errors are skipped (logged as warn), no anomalies returned
	if len(anomalies) != 0 {
		t.Errorf("evaluator errors should be skipped, got %d anomalies", len(anomalies))
	}
}

func TestAdaptiveDetector_MultipleSamples(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsAnomaly: true, ZScore: 4.0, Value: 0.9, Mean: 0.2, Stddev: 0.1},
	}}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, _ := d.Evaluate(context.Background(), "cpu", sampleSet(0.9, 0.91, 0.92))
	if len(anomalies) != 3 {
		t.Errorf("expected 3 anomalies for 3 breaching samples, got %d", len(anomalies))
	}
}

func TestAdaptiveDetector_EmptySamples(t *testing.T) {
	eval := &fakeEvaluator{}
	d := detection.NewAdaptiveDetector(eval)
	anomalies, tested := d.Evaluate(context.Background(), "cpu", nil)
	if len(anomalies) != 0 {
		t.Errorf("empty samples should return no anomalies, got %d", len(anomalies))
	}
	if tested != 0 {
		t.Errorf("empty samples should test 0 series, got %d", tested)
	}
}

// TestAdaptiveDetector_TestedCount is the F0 contract: the tested count is the
// BH family size. Warm-up samples do NOT count (they can't fire); past-warm-up
// evaluations DO count whether or not they produce an anomaly.
func TestAdaptiveDetector_TestedCount(t *testing.T) {
	// One warming-up series, one past-warm-up non-anomaly, one anomaly.
	// fakeEvaluator keys results by metric name, so drive three metrics.
	t.Run("warmup not counted", func(t *testing.T) {
		eval := &fakeEvaluator{results: map[string]*baseline.Result{
			"cpu": {IsWarmingUp: true, Value: 0.5},
		}}
		d := detection.NewAdaptiveDetector(eval)
		_, tested := d.Evaluate(context.Background(), "cpu", sampleSet(0.5, 0.6, 0.7))
		if tested != 0 {
			t.Errorf("warming-up series must not count toward the family, got tested=%d", tested)
		}
	})

	t.Run("past-warmup non-anomaly counted", func(t *testing.T) {
		eval := &fakeEvaluator{results: map[string]*baseline.Result{
			"cpu": {IsAnomaly: false, ZScore: 1.0, Value: 0.5},
		}}
		d := detection.NewAdaptiveDetector(eval)
		anomalies, tested := d.Evaluate(context.Background(), "cpu", sampleSet(0.5, 0.6))
		if len(anomalies) != 0 {
			t.Fatalf("expected 0 anomalies, got %d", len(anomalies))
		}
		if tested != 2 {
			t.Errorf("two past-warm-up evaluations must count even without anomalies, got tested=%d", tested)
		}
	})

	t.Run("anomaly counted", func(t *testing.T) {
		eval := &fakeEvaluator{results: map[string]*baseline.Result{
			"cpu": {IsAnomaly: true, ZScore: 4.0, Value: 0.9, Mean: 0.2, Stddev: 0.1},
		}}
		d := detection.NewAdaptiveDetector(eval)
		anomalies, tested := d.Evaluate(context.Background(), "cpu", sampleSet(0.9, 0.91))
		if len(anomalies) != 2 || tested != 2 {
			t.Errorf("anomalous evaluations count toward family: got anomalies=%d tested=%d, want 2/2", len(anomalies), tested)
		}
	})
}

// ─── Engine tests ─────────────────────────────────────────────────────────────

func TestEngine_EvaluateMetricsAdaptive(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsAnomaly: true, ZScore: 4.0, Value: 0.9, Mean: 0.2, Stddev: 0.1},
	}}
	engine := detection.NewEngine(config.Detection{}, eval)
	anomalies, _ := engine.EvaluateMetricsAdaptive(context.Background(), "cpu", sampleSet(0.9))
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly from adaptive, got %d", len(anomalies))
	}
}

func TestEngine_EvaluateLogRate_SetsSignal(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"error_rate": {IsAnomaly: true, ZScore: 4.0, Value: 5.0, Mean: 1.0, Stddev: 0.5},
	}}
	engine := detection.NewEngine(config.Detection{}, eval)
	anomalies, _ := engine.EvaluateLogRate(context.Background(), "error_rate", sampleSet(5.0))
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly from log rate, got %d", len(anomalies))
	}
	if anomalies[0].Signal != "logs" {
		t.Errorf("EvaluateLogRate signal should be 'logs', got %q", anomalies[0].Signal)
	}
}

func TestEngine_EvaluateMetricsAdaptive_NoAnomaly(t *testing.T) {
	eval := &fakeEvaluator{results: map[string]*baseline.Result{
		"cpu": {IsAnomaly: false, ZScore: 1.0},
	}}
	engine := detection.NewEngine(config.Detection{}, eval)
	anomalies, _ := engine.EvaluateMetricsAdaptive(context.Background(), "cpu", sampleSet(0.5))
	if len(anomalies) != 0 {
		t.Errorf("no anomaly expected, got %d", len(anomalies))
	}
}

func TestStaticDetector_LessThanOperator(t *testing.T) {
	rule := config.StaticRule{
		Name:      "low_replicas",
		Threshold: 2.0,
		Operator:  "<",
		Severity:  "critical",
	}
	engine := detection.NewEngine(config.Detection{
		StaticRules: []config.StaticRule{rule},
	}, nil)
	samples := []ingestion.Sample{
		{Labels: map[string]string{"deploy": "api"}, Value: 1.0}, // breaches
		{Labels: map[string]string{"deploy": "api"}, Value: 3.0}, // ok
	}
	anomalies := engine.EvaluateMetricsStatic(rule, samples)
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 breach for < operator, got %d", len(anomalies))
	}
}

func TestStaticDetector_GreaterThanOrEqualOperator(t *testing.T) {
	rule := config.StaticRule{
		Name:      "restart_rate",
		Threshold: 3.0,
		Operator:  ">=",
		Severity:  "warning",
	}
	engine := detection.NewEngine(config.Detection{
		StaticRules: []config.StaticRule{rule},
	}, nil)
	samples := []ingestion.Sample{
		{Labels: map[string]string{"pod": "a"}, Value: 3.0}, // breaches (==)
		{Labels: map[string]string{"pod": "b"}, Value: 4.0}, // breaches (>)
		{Labels: map[string]string{"pod": "c"}, Value: 2.9}, // ok
	}
	anomalies := engine.EvaluateMetricsStatic(rule, samples)
	if len(anomalies) != 2 {
		t.Fatalf("expected 2 breaches for >= operator, got %d", len(anomalies))
	}
}
