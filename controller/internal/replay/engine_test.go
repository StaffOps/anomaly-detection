package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
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

	report, err := Run(context.Background(), rcfg, cfg)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if report == nil {
		t.Fatal("Run returned nil report")
	}
	// With empty VM/Loki responses, no anomalies expected
	if report.Totals.Anomalies != 0 {
		t.Errorf("expected 0 anomalies, got %d", report.Totals.Anomalies)
	}
}

func TestRun_VMError_GracefulSkip(t *testing.T) {
	// VM returns 500 — Run should skip ticks and return report anyway
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

	// Run should not return an error even when VM/Loki are failing
	report, err := Run(context.Background(), rcfg, cfg)
	if err != nil {
		t.Fatalf("Run should handle VM errors gracefully: %v", err)
	}
	if report == nil {
		t.Fatal("expected a report even with VM errors")
	}
}

func TestAddAll_AppendsAnomalies(t *testing.T) {
	rb := newReportBuilder(10)
	addAll(rb, nil)
	if len(rb.anomalies) != 0 {
		t.Error("addAll with nil should not add anomalies")
	}
}
