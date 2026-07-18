package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

// emptyRangeResponse is a valid but empty Prometheus-compatible/Loki range response.
const emptyRangeResponse = `{"status":"success","data":{"resultType":"matrix","result":[]}}`

func newReplayConfig(vmURL, lokiURL string) *config.Config {
	return &config.Config{
		Cluster: "test",
		Datasources: config.Datasources{
			Prometheus: config.DatasourceEndpoint{URL: vmURL, Timeout: time.Second},
			Loki:       config.DatasourceEndpoint{URL: lokiURL, Timeout: time.Second},
		},
		Controller: config.Controller{
			JobInterval: 30 * time.Minute, // large step = few ticks in test
		},
		Baseline: config.Baseline{
			EWMAAlpha:       0.3,
			ZScoreThreshold: 3.0,
			WarmUpSamples:   1, // minimal warmup for tests
		},
		Detection: config.Detection{
			StaticRules:     []config.StaticRule{},
			AdaptiveMetrics: []config.AdaptiveMetric{},
			LogPatterns:     []config.LogPattern{},
		},
	}
}

func TestRun_EmptyQueries_ReturnsReport(t *testing.T) {
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(emptyRangeResponse))
	}))
	defer vmSrv.Close()

	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(emptyRangeResponse))
	}))
	defer lokiSrv.Close()

	cfg := newReplayConfig(vmSrv.URL, lokiSrv.URL)
	now := time.Now().UTC()
	rcfg := DefaultReplayConfig()
	rcfg.From = now.Add(-4 * time.Hour)
	rcfg.To = now.Add(-1 * time.Hour) // 3h window, well above MinWindow (2.5h)
	rcfg.MaxAnomalies = 10
	rcfg.OutputPath = "" // no file output

	report, err := Run(context.Background(), rcfg, cfg, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if report == nil {
		t.Fatal("Run returned nil report")
	}
	// With empty Prometheus/Loki responses, no anomalies expected
	if report.Totals.Anomalies != 0 {
		t.Errorf("expected 0 anomalies, got %d", report.Totals.Anomalies)
	}
}

func TestRun_VMError_GracefulSkip(t *testing.T) {
	// Prometheus returns 500 — Run should skip ticks and return report anyway
	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer vmSrv.Close()
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer lokiSrv.Close()

	cfg := newReplayConfig(vmSrv.URL, lokiSrv.URL)
	now := time.Now().UTC()
	rcfg := DefaultReplayConfig()
	rcfg.From = now.Add(-4 * time.Hour)
	rcfg.To = now.Add(-1 * time.Hour)

	// Run should not return an error even when Prometheus/Loki are failing
	report, err := Run(context.Background(), rcfg, cfg, nil)
	if err != nil {
		t.Fatalf("Run should handle Prometheus errors gracefully: %v", err)
	}
	if report == nil {
		t.Fatal("expected a report even with Prometheus errors")
	}
}

// TestRun_StaticAnomaly_FlowsThroughFDR drives the full tick loop with a static
// rule that breaches on every tick, exercising the per-tick FDR pass (static
// anomalies pass through unchanged) and the accepted → report path.
func TestRun_StaticAnomaly_FlowsThroughFDR(t *testing.T) {
	// One series with a high constant value at an early timestamp. SamplesAt
	// uses lastPointBefore, so every tick after it reads 0.99 → breaches > 0.9.
	early := time.Now().UTC().Add(-5 * time.Hour).Unix()
	matrix := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"namespace":"production","pod":"api-1"},"values":[[` +
		strconv.FormatInt(early, 10) + `,"0.99"]]}]}}`

	vmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(matrix))
	}))
	defer vmSrv.Close()
	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(emptyRangeResponse))
	}))
	defer lokiSrv.Close()

	cfg := newReplayConfig(vmSrv.URL, lokiSrv.URL)
	cfg.Detection.StaticRules = []config.StaticRule{{
		Name:      "high_cpu_ratio",
		Query:     `container_cpu_usage_ratio{namespace="production"}`,
		Threshold: 0.9,
		Operator:  ">",
		Severity:  "warning",
	}}

	now := time.Now().UTC()
	rcfg := DefaultReplayConfig()
	rcfg.From = now.Add(-4 * time.Hour)
	rcfg.To = now.Add(-1 * time.Hour)
	rcfg.MaxAnomalies = 100
	rcfg.OutputPath = ""

	report, err := Run(context.Background(), rcfg, cfg, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if report.Totals.Anomalies == 0 {
		t.Fatal("expected static anomalies to flow through the per-tick FDR path, got 0")
	}
	if report.Totals.ByDetector["static"] == 0 {
		t.Errorf("expected static detector anomalies in report, got by_detector=%v", report.Totals.ByDetector)
	}
}

func TestReportBuilder_FDRRejectedFlowsToTotals(t *testing.T) {
	rb := newReportBuilder(10)
	rb.fdrRejected = 7
	report := rb.build(Metadata{})
	if report.Totals.FDRRejected != 7 {
		t.Errorf("fdrRejected must surface in Totals: got %d, want 7", report.Totals.FDRRejected)
	}
}

func TestReportBuilder_AddAnomaly_RespectsMax(t *testing.T) {
	rb := newReportBuilder(2)
	for i := 0; i < 5; i++ {
		rb.addAnomaly(detection.Anomaly{
			MetricName: "m",
			Severity:   "warning",
			Timestamp:  time.Now().UTC(),
			Labels:     map[string]string{"namespace": "ns"},
		})
	}
	if len(rb.anomalies) != 2 {
		t.Errorf("maxAnomalies cap not honored: got %d, want 2", len(rb.anomalies))
	}
}
