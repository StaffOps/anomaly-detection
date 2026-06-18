package ingestion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

const vmRangeMalformed = `not valid json`
const lokiRangeMalformed = `not valid json`

// ─── QueryRange malformed JSON ────────────────────────────────────────────────

func TestMetricsPoller_QueryRange_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(vmRangeMalformed))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	now := time.Now()
	_, err := p.QueryRange(context.Background(), "test", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestMetricsPoller_QueryRange_VMErrorStatus(t *testing.T) {
	const vmRangeErrorStatus = `{"status":"error","error":"range query failed"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(vmRangeErrorStatus))
	}))
	defer srv.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	now := time.Now()
	_, err := p.QueryRange(context.Background(), "test", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error when VM range returns status=error")
	}
}

// ─── QueryMetricRange malformed JSON ──────────────────────────────────────────

func TestLogsPoller_QueryMetricRange_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(lokiRangeMalformed))
	}))
	defer srv.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: srv.URL, Timeout: time.Second})
	now := time.Now()
	_, err := p.QueryMetricRange(context.Background(), "test", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

// ─── parseRangeSeries edge cases ─────────────────────────────────────────────

func TestParseRangeSeries_InvalidValue(t *testing.T) {
	data := promRangeData{
		Result: []promRangeResult{
			{
				Metric: map[string]string{"pod": "p1"},
				Values: [][2]interface{}{
					{float64(1717070400), "not-a-float"}, // invalid float
				},
			},
		},
	}
	series := parseRangeSeries(data)
	// Series should exist but with 0 points
	if len(series) == 1 && len(series[0].Points) != 0 {
		t.Errorf("invalid value should be skipped, got %d points", len(series[0].Points))
	}
}

func TestParseRangeSeries_NonFloatTimestamp(t *testing.T) {
	data := promRangeData{
		Result: []promRangeResult{
			{
				Metric: map[string]string{"pod": "p1"},
				Values: [][2]interface{}{
					{"not-a-float-ts", "0.42"}, // non-float timestamp
				},
			},
		},
	}
	series := parseRangeSeries(data)
	if len(series) == 1 && len(series[0].Points) != 0 {
		t.Errorf("non-float timestamp should be skipped, got %d points", len(series[0].Points))
	}
}

func TestParseRangeSeries_NonStringValue(t *testing.T) {
	data := promRangeData{
		Result: []promRangeResult{
			{
				Metric: map[string]string{"pod": "p1"},
				Values: [][2]interface{}{
					{float64(1717070400), 42.0}, // float value, not string
				},
			},
		},
	}
	series := parseRangeSeries(data)
	if len(series) == 1 && len(series[0].Points) != 0 {
		t.Errorf("non-string value should be skipped, got %d points", len(series[0].Points))
	}
}
