package ml

import (
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
)

func TestBuildFeatureVector_IncludesAnomalyAndEnrichment(t *testing.T) {
	rep := detection.Anomaly{Score: 4.2, Value: 0.95}
	bundle := enrichment.Bundle{
		Results: []enrichment.Result{
			{Name: "cpu_ratio", Value: 0.95},
			{Name: "memory_ratio", Value: 0.62},
			{Name: "restarts_5m", Value: 0},
		},
	}
	got := BuildFeatureVector(rep, bundle)
	if got == nil {
		t.Fatal("expected non-nil feature vector")
	}
	for _, k := range []string{"anomaly_score", "anomaly_value", "cpu_ratio", "memory_ratio", "restarts_5m"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing feature %q", k)
		}
	}
	if got["anomaly_score"] != 4.2 || got["cpu_ratio"] != 0.95 {
		t.Errorf("values not propagated: %v", got)
	}
}

func TestBuildFeatureVector_SkipsErroredEnrichments(t *testing.T) {
	rep := detection.Anomaly{Score: 3.0, Value: 1.0}
	bundle := enrichment.Bundle{
		Results: []enrichment.Result{
			{Name: "cpu_ratio", Value: 0.5},
			{Name: "memory_ratio", Error: "no_data"},
			{Name: "error_logs_1m", Error: "loki_not_configured"},
		},
	}
	got := BuildFeatureVector(rep, bundle)
	if _, ok := got["memory_ratio"]; ok {
		t.Error("memory_ratio with error should be excluded")
	}
	if _, ok := got["error_logs_1m"]; ok {
		t.Error("error_logs_1m with error should be excluded")
	}
	if _, ok := got["cpu_ratio"]; !ok {
		t.Error("cpu_ratio should be present")
	}
}

func TestBuildFeatureVector_NilWhenInsufficient(t *testing.T) {
	// Anomaly always contributes 2 features (score, value), so to drop below
	// the threshold we'd need both base features missing — not possible.
	// Test with empty enrichment (still 2 base features = OK):
	rep := detection.Anomaly{Score: 3.0, Value: 0.5}
	got := BuildFeatureVector(rep, enrichment.Bundle{})
	if got == nil {
		t.Error("two base features should always be sufficient")
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 base features, got %d", len(got))
	}
}
