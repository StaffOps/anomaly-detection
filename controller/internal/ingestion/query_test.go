package ingestion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

const vmInstantOK = `{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {"metric": {"namespace": "prod", "pod": "api-abc"}, "value": [1717070400, "0.75"]},
      {"metric": {"namespace": "prod", "pod": "api-def"}, "value": [1717070400, "0.85"]}
    ]
  }
}`

const vmInstantError = `{"status":"error","error":"bad query"}`

const vmInstantEmpty = `{
  "status": "success",
  "data": {"resultType": "vector", "result": []}
}`

const vmInstantMalformed = `not valid json`

const lokiInstantOK = `{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {"metric": {"namespace": "prod"}, "value": ["1717070400000000000", "5.2"]},
      {"metric": {"namespace": "staging"}, "value": ["1717070400000000000", "1.0"]}
    ]
  }
}`

// ─── MetricsPoller.Query ──────────────────────────────────────────────────────

func TestMetricsPoller_Query_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(vmInstantOK))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	samples, err := p.Query(context.Background(), "test_metric")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	if samples[0].Value != 0.75 {
		t.Errorf("first sample value: want 0.75, got %v", samples[0].Value)
	}
	if samples[0].Labels["pod"] != "api-abc" {
		t.Errorf("first sample pod label: want api-abc, got %q", samples[0].Labels["pod"])
	}
}

func TestMetricsPoller_Query_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.Query(context.Background(), "test_metric")
	if err == nil {
		t.Error("expected error for server error response")
	}
}

func TestMetricsPoller_Query_VMErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(vmInstantError))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.Query(context.Background(), "bad_query")
	if err == nil {
		t.Error("expected error when VM returns status=error")
	}
}

func TestMetricsPoller_Query_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(vmInstantMalformed))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.Query(context.Background(), "test_metric")
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestMetricsPoller_Query_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(vmInstantEmpty))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	samples, err := p.Query(context.Background(), "empty_metric")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected 0 samples for empty result, got %d", len(samples))
	}
}

func TestMetricsPoller_Query_Unreachable(t *testing.T) {
	p := NewMetricsPoller(config.DatasourceEndpoint{URL: "http://127.0.0.1:1", Timeout: 100 * time.Millisecond})
	_, err := p.Query(context.Background(), "test_metric")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestMetricsPoller_Query_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow server
		select {
		case <-r.Context().Done():
		case <-make(chan struct{}):
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.Query(ctx, "test_metric")
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ─── parseSamples ─────────────────────────────────────────────────────────────

func TestParseSamples_ValidValues(t *testing.T) {
	data := promData{
		Result: []promResult{
			{Metric: map[string]string{"pod": "p1"}, Value: [2]interface{}{float64(1717070400), "0.42"}},
			{Metric: map[string]string{"pod": "p2"}, Value: [2]interface{}{float64(1717070430), "0.99"}},
		},
	}
	samples := parseSamples(data)
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	if samples[0].Value != 0.42 {
		t.Errorf("want 0.42, got %v", samples[0].Value)
	}
	if samples[1].Labels["pod"] != "p2" {
		t.Errorf("want p2, got %q", samples[1].Labels["pod"])
	}
}

func TestParseSamples_InvalidFloatString(t *testing.T) {
	data := promData{
		Result: []promResult{
			{Metric: map[string]string{}, Value: [2]interface{}{float64(1), "not-a-float"}},
		},
	}
	samples := parseSamples(data)
	if len(samples) != 0 {
		t.Errorf("invalid float should be skipped, got %d samples", len(samples))
	}
}

func TestParseSamples_NonStringValue(t *testing.T) {
	data := promData{
		Result: []promResult{
			{Metric: map[string]string{}, Value: [2]interface{}{float64(1), 42.0}}, // float, not string
		},
	}
	samples := parseSamples(data)
	if len(samples) != 0 {
		t.Errorf("non-string value should be skipped, got %d samples", len(samples))
	}
}

// ─── LogsPoller.QueryMetric ───────────────────────────────────────────────────

func TestLogsPoller_QueryMetric_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(lokiInstantOK))
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	samples, err := p.QueryMetric(context.Background(), "test_query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	if samples[0].Value != 5.2 {
		t.Errorf("first sample value: want 5.2, got %v", samples[0].Value)
	}
}

func TestLogsPoller_QueryMetric_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.QueryMetric(context.Background(), "test_query")
	if err == nil {
		t.Error("expected error for server error")
	}
}

func TestLogsPoller_QueryMetric_Unreachable(t *testing.T) {
	p := NewLogsPoller(config.DatasourceEndpoint{URL: "http://127.0.0.1:1", Timeout: 100 * time.Millisecond})
	_, err := p.QueryMetric(context.Background(), "test_query")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
