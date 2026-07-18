//go:build integration

package replay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/replay"
)

// Integration test for replay mode.
//
// Prerequisites:
//   docker compose -f internal/replay/testdata/docker-compose-integ.yaml up -d
//
// Run:
//   go test -tags=integration -run TestReplayIntegration ./internal/replay/
//
// The test injects synthetic metrics into a Prometheus-compatible TSDB and verifies that
// the replay engine detects the known anomalies with ≥90% accuracy.

func vmURL() string {
	if u := os.Getenv("PROMETHEUS_URL"); u != "" {
		return u
	}
	return "http://localhost:8428"
}

func lokiURL() string {
	if u := os.Getenv("LOKI_URL"); u != "" {
		return u
	}
	return "http://localhost:3100"
}

func TestReplayIntegration(t *testing.T) {
	ctx := context.Background()
	vm := vmURL()
	loki := lokiURL()

	// Verify backends are reachable.
	if err := waitReady(vm+"/health", 15*time.Second); err != nil {
		t.Fatalf("Prometheus not ready: %v", err)
	}
	if err := waitReady(loki+"/ready", 15*time.Second); err != nil {
		t.Fatalf("Loki not ready: %v", err)
	}

	// Time window: inject 3 hours of data ending 1 minute ago.
	// First 2h = normal baseline, last 1h = anomalous spikes.
	now := time.Now().UTC().Truncate(time.Minute)
	windowEnd := now.Add(-1 * time.Minute)
	windowStart := windowEnd.Add(-3 * time.Hour)
	anomalyStart := windowEnd.Add(-1 * time.Hour)

	// --- Inject synthetic metrics into Prometheus-compatible TSDB ---
	t.Log("Injecting synthetic metrics into Prometheus...")
	injectedAnomalies := injectMetrics(t, vm, windowStart, windowEnd, anomalyStart)
	t.Logf("Injected %d anomalous data points", injectedAnomalies)

	// --- Inject synthetic logs into Loki ---
	t.Log("Injecting synthetic logs into Loki...")
	injectedLogAnomalies := injectLogs(t, loki, windowStart, windowEnd, anomalyStart)
	t.Logf("Injected %d anomalous log bursts", injectedLogAnomalies)

	// Wait for Prometheus to index.
	time.Sleep(2 * time.Second)

	// --- Run replay ---
	cfg := buildTestConfig(vm, loki)
	rcfg := replay.ReplayConfig{
		From:           windowStart,
		To:             windowEnd,
		ConfigPath:     "integration-test",
		OutputPath:     "/tmp/replay-integ-test.json",
		WarmupFraction: 0.6, // 2h warmup out of 3h window
		MaxAnomalies:   500,
	}

	report, err := replay.Run(ctx, rcfg, cfg, nil)
	if err != nil {
		t.Fatalf("replay.Run failed: %v", err)
	}

	// --- Verify results ---
	t.Logf("Replay result: %d anomalies detected (status: %s)", report.Totals.Anomalies, report.Metadata.ResultStatus)
	t.Logf("  by_severity: warning=%d critical=%d", report.Totals.BySeverity["warning"], report.Totals.BySeverity["critical"])
	t.Logf("  by_detector: static=%d adaptive=%d", report.Totals.ByDetector["static"], report.Totals.ByDetector["adaptive"])
	t.Logf("  ticks_processed: %d, query_errors: %d", report.Metadata.ExecutionMetrics.TicksProcessed, report.Totals.QueryErrors)

	// We expect the replay to detect anomalies. The exact count depends on
	// how many ticks fall in the anomaly window and how many series spike.
	// With 1h of anomalies at 30s ticks = 120 ticks × N series.
	// We injected clear spikes (10x baseline) so detection should be high.
	totalInjected := injectedAnomalies + injectedLogAnomalies
	if totalInjected == 0 {
		t.Fatal("no anomalies were injected — test setup error")
	}

	// Accuracy: we expect ≥90% of injected anomaly ticks to be detected.
	// Since each injected anomaly is a distinct (metric, tick) pair that should
	// trigger detection, we compare detected count vs injected count.
	// Note: detected may be > injected due to multiple detectors firing on same data.
	if report.Totals.Anomalies == 0 {
		t.Fatal("replay detected 0 anomalies — expected at least some from injected spikes")
	}

	// Minimum detection: at least 90% of metric anomalies should be caught.
	// (Log anomalies depend on Loki ingestion timing, so we're lenient there.)
	detectionRate := float64(report.Totals.Anomalies) / float64(injectedAnomalies)
	t.Logf("Detection rate: %.1f%% (detected=%d, injected_metric_anomalies=%d)",
		detectionRate*100, report.Totals.Anomalies, injectedAnomalies)

	if detectionRate < 0.9 {
		t.Errorf("detection rate %.1f%% < 90%% threshold (detected=%d, injected=%d)",
			detectionRate*100, report.Totals.Anomalies, injectedAnomalies)
	}
}

// injectMetrics pushes synthetic time-series into a Prometheus-compatible TSDB.
// Returns the number of anomalous data points injected.
func injectMetrics(t *testing.T, vmBase string, start, end, anomalyStart time.Time) int {
	t.Helper()
	step := 30 * time.Second
	// 2 pods with normal CPU (~0.2), then spike to 0.95 in anomaly window.
	pods := []string{"api-server-abc12-x1y2z", "api-server-abc12-a3b4c"}
	var lines []string
	anomalyCount := 0

	for _, pod := range pods {
		for ts := start; ts.Before(end); ts = ts.Add(step) {
			var value float64
			if ts.Before(anomalyStart) {
				// Normal: 0.15-0.25 (slight variation)
				value = 0.2 + 0.05*float64(ts.Unix()%3-1)
			} else {
				// Anomalous: 0.92-0.98
				value = 0.95 + 0.03*float64(ts.Unix()%2)
				anomalyCount++
			}
			line := fmt.Sprintf(
				`container_cpu_usage_ratio{namespace="production",pod="%s",container="app"} %f %d`,
				pod, value, ts.Unix(),
			)
			lines = append(lines, line)
		}
	}

	// Push via /api/v1/import/prometheus (line protocol).
	body := strings.Join(lines, "\n")
	resp, err := http.Post(vmBase+"/api/v1/import/prometheus", "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to inject metrics: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("Prometheus import returned %d", resp.StatusCode)
	}

	return anomalyCount
}

// injectLogs pushes synthetic log entries into Loki.
// Returns the number of anomalous log bursts injected.
func injectLogs(t *testing.T, lokiBase string, start, end, anomalyStart time.Time) int {
	t.Helper()
	step := 30 * time.Second
	anomalyCount := 0

	type lokiStream struct {
		Stream map[string]string `json:"stream"`
		Values [][]string        `json:"values"`
	}
	type lokiPush struct {
		Streams []lokiStream `json:"streams"`
	}

	var values [][]string
	for ts := anomalyStart; ts.Before(end); ts = ts.Add(step) {
		// Inject 5 error logs per tick in anomaly window (burst).
		for i := 0; i < 5; i++ {
			nsTs := fmt.Sprintf("%d", ts.Add(time.Duration(i)*time.Second).UnixNano())
			values = append(values, []string{nsTs, "ERROR: connection timeout to database"})
		}
		anomalyCount++
	}

	// Also inject some normal logs in the baseline period.
	for ts := start; ts.Before(anomalyStart); ts = ts.Add(5 * time.Minute) {
		nsTs := fmt.Sprintf("%d", ts.UnixNano())
		values = append(values, []string{nsTs, "INFO: request processed successfully"})
	}

	push := lokiPush{
		Streams: []lokiStream{{
			Stream: map[string]string{
				"service_namespace": "production",
				"service_workload":  "api-server",
			},
			Values: values,
		}},
	}

	body, _ := json.Marshal(push)
	resp, err := http.Post(lokiBase+"/loki/api/v1/push", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to inject logs: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != 204 {
		t.Fatalf("Loki push returned %d", resp.StatusCode)
	}

	return anomalyCount
}

// buildTestConfig creates a minimal config targeting the local test stack.
func buildTestConfig(vmBase, lokiBase string) *config.Config {
	return &config.Config{
		Cluster: "integration-test",
		Datasources: config.Datasources{
			Prometheus: config.DatasourceEndpoint{URL: vmBase, Timeout: 10 * time.Second},
			Loki:       config.DatasourceEndpoint{URL: lokiBase, Timeout: 10 * time.Second},
		},
		Controller: config.Controller{
			JobInterval:            30 * time.Second,
			CorrelationWindow:      2 * time.Minute,
			Cooldown:               5 * time.Minute,
			WorkloadPatternMinPods: 3,
		},
		Baseline: config.Baseline{
			WindowSize:      60,
			EWMAAlpha:       0.3,
			ZScoreThreshold: 3.0,
			WarmUpSamples:   60,
		},
		Detection: config.Detection{
			StaticRules: []config.StaticRule{
				{
					Name:      "high_cpu_ratio",
					Query:     `container_cpu_usage_ratio{namespace="production"}`,
					Threshold: 0.9,
					Operator:  ">",
					Severity:  "warning",
				},
			},
			AdaptiveMetrics: []config.AdaptiveMetric{
				{
					Name:    "cpu_by_pod",
					Query:   `container_cpu_usage_ratio{namespace="production"}`,
					GroupBy: []string{"namespace", "pod"},
				},
			},
			LogPatterns: []config.LogPattern{
				{
					Name:    "error_rate_production",
					Query:   `sum(rate({service_namespace="production"} |= "ERROR" [1m])) by (service_namespace)`,
					GroupBy: []string{"service_namespace"},
				},
			},
		},
	}
}

// waitReady polls a URL until it returns 200 or timeout.
func waitReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}
