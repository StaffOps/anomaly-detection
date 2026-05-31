package detection_test

import (
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

func TestStaticDetector_Breaches(t *testing.T) {
	rule := config.StaticRule{
		Name:      "high_cpu",
		Threshold: 0.9,
		Operator:  ">",
		Severity:  "warning",
	}

	samples := []ingestion.Sample{
		{Labels: map[string]string{"pod": "a"}, Value: 0.5},
		{Labels: map[string]string{"pod": "b"}, Value: 0.95},
		{Labels: map[string]string{"pod": "c"}, Value: 0.91},
		{Labels: map[string]string{"pod": "d"}, Value: 0.89},
	}

	engine := detection.NewEngine(config.Detection{
		StaticRules:   []config.StaticRule{rule},
		EventPatterns: []string{"OOMKilled"},
	}, nil)

	anomalies := engine.EvaluateMetricsStatic(rule, samples)

	if len(anomalies) != 2 {
		t.Fatalf("expected 2 anomalies, got %d", len(anomalies))
	}
	if anomalies[0].Labels["pod"] != "b" {
		t.Errorf("expected pod b, got %s", anomalies[0].Labels["pod"])
	}
	if anomalies[1].Labels["pod"] != "c" {
		t.Errorf("expected pod c, got %s", anomalies[1].Labels["pod"])
	}
	if anomalies[0].Detector != "static" {
		t.Errorf("expected detector static, got %s", anomalies[0].Detector)
	}
}

func TestStaticDetector_LessThan(t *testing.T) {
	rule := config.StaticRule{
		Name:      "low_availability",
		Threshold: 0.99,
		Operator:  "<",
		Severity:  "critical",
	}

	samples := []ingestion.Sample{
		{Labels: map[string]string{"svc": "api"}, Value: 0.98},
		{Labels: map[string]string{"svc": "web"}, Value: 1.0},
	}

	engine := detection.NewEngine(config.Detection{
		StaticRules:   []config.StaticRule{rule},
		EventPatterns: []string{},
	}, nil)

	anomalies := engine.EvaluateMetricsStatic(rule, samples)

	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Severity != "critical" {
		t.Errorf("expected critical, got %s", anomalies[0].Severity)
	}
}

func TestStaticDetector_NoBreaches(t *testing.T) {
	rule := config.StaticRule{
		Name:      "high_cpu",
		Threshold: 0.9,
		Operator:  ">",
		Severity:  "warning",
	}

	samples := []ingestion.Sample{
		{Labels: map[string]string{"pod": "a"}, Value: 0.1},
		{Labels: map[string]string{"pod": "b"}, Value: 0.5},
	}

	engine := detection.NewEngine(config.Detection{
		StaticRules:   []config.StaticRule{rule},
		EventPatterns: []string{},
	}, nil)

	anomalies := engine.EvaluateMetricsStatic(rule, samples)

	if len(anomalies) != 0 {
		t.Fatalf("expected 0 anomalies, got %d", len(anomalies))
	}
}

func TestPatternDetector_MatchesKnownEvents(t *testing.T) {
	engine := detection.NewEngine(config.Detection{
		EventPatterns: []string{"CrashLoopBackOff", "OOMKilled", "Evicted"},
	}, nil)

	tests := []struct {
		event    ingestion.EventAnomaly
		wantNil  bool
		severity string
	}{
		{
			event:    ingestion.EventAnomaly{Reason: "OOMKilled", Namespace: "devops", Pod: "api-xyz", Count: 1, Timestamp: time.Now()},
			severity: "critical",
		},
		{
			event:    ingestion.EventAnomaly{Reason: "CrashLoopBackOff", Namespace: "devops", Pod: "worker-abc", Count: 5, Timestamp: time.Now()},
			severity: "critical",
		},
		{
			event:   ingestion.EventAnomaly{Reason: "Pulled", Namespace: "devops", Pod: "api-xyz", Count: 1, Timestamp: time.Now()},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		result := engine.EvaluateEvent(tt.event)
		if tt.wantNil {
			if result != nil {
				t.Errorf("expected nil for reason=%s, got anomaly", tt.event.Reason)
			}
			continue
		}
		if result == nil {
			t.Fatalf("expected anomaly for reason=%s, got nil", tt.event.Reason)
		}
		if result.Severity != tt.severity {
			t.Errorf("reason=%s: expected severity %s, got %s", tt.event.Reason, tt.severity, result.Severity)
		}
		if result.Signal != "events" {
			t.Errorf("expected signal events, got %s", result.Signal)
		}
	}
}
