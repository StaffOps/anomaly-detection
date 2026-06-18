package detection_test

import (
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

func TestStaticDetector_LessThanOrEqualOperator(t *testing.T) {
	rule := config.StaticRule{
		Name:      "low_cpu",
		Threshold: 3.0,
		Operator:  "<=",
		Severity:  "warning",
	}
	engine := detection.NewEngine(config.Detection{StaticRules: []config.StaticRule{rule}}, nil)
	samples := []ingestion.Sample{
		{Labels: map[string]string{"pod": "a"}, Value: 3.0}, // breaches (==)
		{Labels: map[string]string{"pod": "b"}, Value: 2.0}, // breaches (<)
		{Labels: map[string]string{"pod": "c"}, Value: 4.0}, // ok
	}
	anomalies := engine.EvaluateMetricsStatic(rule, samples)
	if len(anomalies) != 2 {
		t.Fatalf("<= operator: expected 2 breaches, got %d", len(anomalies))
	}
}

func TestStaticDetector_UnknownOperator_FallsBackToGreaterThan(t *testing.T) {
	// Unknown operators fall back to ">" behavior (default case in breaches)
	rule := config.StaticRule{
		Name:      "unknown",
		Threshold: 1.0,
		Operator:  "!=", // not supported → falls back to >
		Severity:  "warning",
	}
	engine := detection.NewEngine(config.Detection{StaticRules: []config.StaticRule{rule}}, nil)
	samples := []ingestion.Sample{
		{Labels: map[string]string{"pod": "a"}, Value: 99.0}, // 99 > 1 → breaches
		{Labels: map[string]string{"pod": "b"}, Value: 0.5},  // 0.5 > 1 → false
	}
	anomalies := engine.EvaluateMetricsStatic(rule, samples)
	if len(anomalies) != 1 {
		t.Errorf("unknown operator defaults to '>', expected 1 breach, got %d", len(anomalies))
	}
}
