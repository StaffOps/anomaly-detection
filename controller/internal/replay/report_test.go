package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

var testTime = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

func testReport() *Report {
	anomalies := []detection.Anomaly{
		{
			MetricName: "cpu_by_workload",
			Labels:     map[string]string{"namespace": "payments", "pod": "pay-api-abc12", "workload": "pay-api"},
			Value:      0.87, Mean: 0.42, Stddev: 0.1, Score: 4.5,
			Severity: "warning", Signal: "metrics", Detector: "adaptive",
			Timestamp: testTime,
		},
		{
			MetricName: "error_rate",
			Labels:     map[string]string{"namespace": "payments", "pod": "pay-api-def34", "workload": "pay-api"},
			Value:      15.0, Mean: 2.0, Stddev: 1.0, Score: 13.0,
			Severity: "critical", Signal: "metrics", Detector: "static",
			Timestamp: testTime.Add(time.Hour),
		},
		{
			MetricName: "log_error_rate",
			Labels:     map[string]string{"namespace": "orders", "workload": "order-svc"},
			Value:      50.0, Mean: 5.0, Stddev: 3.0, Score: 15.0,
			Severity: "warning", Signal: "logs", Detector: "adaptive",
			Timestamp: testTime.Add(2 * time.Hour),
		},
	}

	meta := Metadata{
		SchemaVersion:     "1",
		ControllerVersion: "0.7.0",
		RanAt:             time.Date(2026, 5, 30, 20, 0, 0, 0, time.UTC),
		WindowStart:       time.Date(2026, 5, 29, 20, 0, 0, 0, time.UTC),
		WindowEnd:         time.Date(2026, 5, 30, 20, 0, 0, 0, time.UTC),
		WarmupStart:       time.Date(2026, 5, 29, 20, 0, 0, 0, time.UTC),
		WarmupEnd:         time.Date(2026, 5, 30, 0, 48, 0, 0, time.UTC),
		WarmupFraction:    0.2,
		TickIntervalSec:   30,
		ConfigSummary:     ConfigSummary{StaticRules: 3, AdaptiveMetrics: 4, LogPatterns: 3},
		ExecutionMetrics: ExecutionMetrics{
			TicksProcessed:         2304,
			TicksSkippedQueryError: 2,
			PromQueriesTotal:       4608,
			PromQueryDurationP95:   0.42,
			LokiQueriesTotal:       1152,
			MemoryPeakMB:           348.5,
			DurationSeconds:        187.4,
		},
	}

	return BuildReport(anomalies, 1000, meta)
}

func TestWriteJSON_Golden(t *testing.T) {
	report := testReport()
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("testdata", "report.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(goldenPath, buf.Bytes(), 0o644)
		t.Log("golden file updated")
		return
	}
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing (run with UPDATE_GOLDEN=1): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("JSON output differs from golden file.\nGot:\n%s", buf.String())
	}
}

func TestWriteMarkdown_Golden(t *testing.T) {
	report := testReport()
	var buf bytes.Buffer
	if err := report.WriteMarkdown(&buf); err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("testdata", "report.md")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(goldenPath, buf.Bytes(), 0o644)
		t.Log("golden file updated")
		return
	}
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing (run with UPDATE_GOLDEN=1): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Markdown output differs from golden file.\nGot:\n%s", buf.String())
	}
}

func TestBuildReport_Totals(t *testing.T) {
	report := testReport()

	if report.Totals.Anomalies != 3 {
		t.Errorf("expected 3 anomalies, got %d", report.Totals.Anomalies)
	}
	if report.Totals.BySeverity["warning"] != 2 {
		t.Errorf("expected 2 warnings, got %d", report.Totals.BySeverity["warning"])
	}
	if report.Totals.BySeverity["critical"] != 1 {
		t.Errorf("expected 1 critical, got %d", report.Totals.BySeverity["critical"])
	}
	if report.Totals.BySignal["metrics"] != 2 {
		t.Errorf("expected 2 metrics, got %d", report.Totals.BySignal["metrics"])
	}
	if report.Totals.BySignal["logs"] != 1 {
		t.Errorf("expected 1 logs, got %d", report.Totals.BySignal["logs"])
	}
	if report.Totals.ByKind["pod"] != 2 {
		t.Errorf("expected 2 pod, got %d", report.Totals.ByKind["pod"])
	}
	if report.Totals.ByKind["workload"] != 1 {
		t.Errorf("expected 1 workload, got %d", report.Totals.ByKind["workload"])
	}
}

func TestBuildReport_TopWorkloads(t *testing.T) {
	report := testReport()
	if len(report.TopWorkloads) != 2 {
		t.Fatalf("expected 2 top workloads, got %d", len(report.TopWorkloads))
	}
	// pay-api has 2 anomalies, order-svc has 1
	if report.TopWorkloads[0].Workload != "pay-api" || report.TopWorkloads[0].Count != 2 {
		t.Errorf("expected pay-api:2, got %s:%d", report.TopWorkloads[0].Workload, report.TopWorkloads[0].Count)
	}
}

func TestBuildReport_Timeline(t *testing.T) {
	report := testReport()
	if len(report.Timeline) != 3 {
		t.Fatalf("expected 3 timeline entries, got %d", len(report.Timeline))
	}
	// Verify sorted by hour
	for i := 1; i < len(report.Timeline); i++ {
		if !report.Timeline[i].Hour.After(report.Timeline[i-1].Hour) {
			t.Error("timeline not sorted")
		}
	}
}

func TestBuildReport_MaxAnomalies(t *testing.T) {
	anomalies := make([]detection.Anomaly, 50)
	for i := range anomalies {
		anomalies[i] = detection.Anomaly{
			MetricName: "test", Labels: map[string]string{"namespace": "ns", "pod": "p"},
			Severity: "warning", Signal: "metrics", Detector: "adaptive",
			Timestamp: testTime,
		}
	}
	meta := Metadata{SchemaVersion: "1"}
	report := BuildReport(anomalies, 10, meta)
	if report.Totals.Anomalies != 10 {
		t.Errorf("expected max 10 anomalies, got %d", report.Totals.Anomalies)
	}
}
