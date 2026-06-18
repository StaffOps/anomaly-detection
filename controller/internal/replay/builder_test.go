package replay

import (
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

func TestReportBuilder_AddWarmupSkipped(t *testing.T) {
	b := newReportBuilder(10)
	b.addWarmupSkipped()
	b.addWarmupSkipped()
	if b.warmupSkipped != 2 {
		t.Errorf("warmupSkipped: want 2, got %d", b.warmupSkipped)
	}
}

func TestReportBuilder_AddQueryError(t *testing.T) {
	b := newReportBuilder(10)
	b.addQueryError()
	if b.queryErrors != 1 {
		t.Errorf("queryErrors: want 1, got %d", b.queryErrors)
	}
}

func TestNewMetadata(t *testing.T) {
	now := time.Now().UTC()
	rcfg := DefaultReplayConfig()
	rcfg.From = now.Add(-4 * time.Hour)
	rcfg.To = now

	meta := NewMetadata(
		rcfg,
		30*time.Second,
		now.Add(-3*time.Hour),
		ConfigSummary{},
		ExecutionMetrics{},
	)

	if meta.SchemaVersion != "1" {
		t.Errorf("SchemaVersion: want 1, got %q", meta.SchemaVersion)
	}
	if meta.WarmupFraction != 0.2 {
		t.Errorf("WarmupFraction: want 0.2, got %v", meta.WarmupFraction)
	}
	if meta.TickIntervalSec != 30 {
		t.Errorf("TickIntervalSec: want 30, got %d", meta.TickIntervalSec)
	}
}

func TestReportBuilder_Build_Totals(t *testing.T) {
	b := newReportBuilder(100)
	b.addAnomaly(detection.Anomaly{
		MetricName: "cpu",
		Severity:   "warning",
		Signal:     "metrics",
		Detector:   "adaptive",
		Labels:     map[string]string{"namespace": "prod", "pod": "api-abc-xyz"},
		Timestamp:  time.Now(),
	})
	b.addAnomaly(detection.Anomaly{
		MetricName: "error_rate",
		Severity:   "critical",
		Signal:     "logs",
		Detector:   "adaptive",
		Labels:     map[string]string{"namespace": "prod"},
		Timestamp:  time.Now(),
	})
	b.addWarmupSkipped()
	b.addQueryError()

	meta := NewMetadata(DefaultReplayConfig(), 30*time.Second, time.Now(), ConfigSummary{}, ExecutionMetrics{})
	report := b.build(meta)

	if report.Totals.Anomalies != 2 {
		t.Errorf("total anomalies: want 2, got %d", report.Totals.Anomalies)
	}
	if report.Totals.BySeverity["warning"] != 1 {
		t.Errorf("warning count: want 1, got %d", report.Totals.BySeverity["warning"])
	}
	if report.Totals.BySeverity["critical"] != 1 {
		t.Errorf("critical count: want 1, got %d", report.Totals.BySeverity["critical"])
	}
	if report.Totals.WarmupSkipped != 1 {
		t.Errorf("warmupSkipped: want 1, got %d", report.Totals.WarmupSkipped)
	}
	if report.Totals.QueryErrors != 1 {
		t.Errorf("queryErrors: want 1, got %d", report.Totals.QueryErrors)
	}
}
