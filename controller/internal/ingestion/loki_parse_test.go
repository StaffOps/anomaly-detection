package ingestion

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"context"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

const lokiErrorStatus = `{"status":"error","error":"query failed"}`
const lokiMalformed = `not valid json`

// ─── parseLokiSamples edge cases ─────────────────────────────────────────────

func TestParseLokiSamples_NonStringValue(t *testing.T) {
	data := lokiData{
		Result: []lokiResult{
			{Metric: map[string]string{"ns": "prod"}, Value: [2]interface{}{"1717000000000000000", 42.0}}, // float, not string
		},
	}
	samples := parseLokiSamples(data)
	if len(samples) != 0 {
		t.Errorf("non-string value should be skipped, got %d samples", len(samples))
	}
}

func TestParseLokiSamples_InvalidFloat(t *testing.T) {
	data := lokiData{
		Result: []lokiResult{
			{Metric: map[string]string{}, Value: [2]interface{}{"1717000000000000000", "not-a-float"}},
		},
	}
	samples := parseLokiSamples(data)
	if len(samples) != 0 {
		t.Errorf("invalid float should be skipped, got %d samples", len(samples))
	}
}

func TestParseLokiSamples_Valid(t *testing.T) {
	data := lokiData{
		Result: []lokiResult{
			{Metric: map[string]string{"ns": "prod"}, Value: [2]interface{}{"1717000000000000000", "5.5"}},
		},
	}
	samples := parseLokiSamples(data)
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if samples[0].Value != 5.5 {
		t.Errorf("want 5.5, got %v", samples[0].Value)
	}
}

// ─── QueryMetric error paths ──────────────────────────────────────────────────

func TestLogsPoller_QueryMetric_LokiErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(lokiErrorStatus))
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.QueryMetric(context.Background(), "test_query")
	if err == nil {
		t.Error("expected error when Loki returns status=error")
	}
}

func TestLogsPoller_QueryMetric_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(lokiMalformed))
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	_, err := p.QueryMetric(context.Background(), "test_query")
	if err == nil {
		t.Error("expected error for malformed JSON response")
	}
}

// ─── QueryMetricRange additional paths ───────────────────────────────────────

const lokiRangeErrorStatus = `{"status":"error","error":"range query failed"}`

func TestLogsPoller_QueryMetricRange_LokiErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(lokiRangeErrorStatus))
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	now := time.Now()
	_, err := p.QueryMetricRange(context.Background(), "test", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error when Loki range returns status=error")
	}
}

// ─── parseLokiRangeSeries — timestamp edge cases ──────────────────────────────

func TestParseLokiRangeSeries_FloatTimestamp(t *testing.T) {
	// Some Loki versions return float timestamps in range queries
	data := lokiRangeData{
		Result: []lokiRangeResult{
			{
				Metric: map[string]string{"ns": "prod"},
				Values: [][2]interface{}{
					{float64(1717070400), "3.14"}, // float timestamp, string value
				},
			},
		},
	}
	series := parseLokiRangeSeries(data)
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	if len(series[0].Points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(series[0].Points))
	}
}

func TestParseLokiRangeSeries_InvalidValue(t *testing.T) {
	data := lokiRangeData{
		Result: []lokiRangeResult{
			{
				Metric: map[string]string{},
				Values: [][2]interface{}{
					{"1717070400000000000", "not-a-float"}, // invalid value
				},
			},
		},
	}
	series := parseLokiRangeSeries(data)
	// Series exists but with 0 points (invalid value skipped)
	if len(series) == 1 && len(series[0].Points) != 0 {
		t.Errorf("invalid value should produce 0 points, got %d", len(series[0].Points))
	}
}
