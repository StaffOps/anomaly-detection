package ingestion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

const promRangeOK = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {
        "metric": {"namespace": "x", "pod": "p1"},
        "values": [
          [1717070400, "0.42"],
          [1717070430, "0.51"],
          [1717070460, "0.55"]
        ]
      },
      {
        "metric": {"namespace": "x", "pod": "p2"},
        "values": [
          [1717070400, "0.30"],
          [1717070430, "0.31"]
        ]
      }
    ]
  }
}`

const promRangeError = `{"status":"error","errorType":"bad_data","error":"bad query"}`

const lokiRangeOK = `{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [
      {
        "metric": {"namespace": "y"},
        "values": [
          ["1717070400000000000", "10"],
          ["1717070430000000000", "12"]
        ]
      }
    ]
  }
}`

func TestMetricsPoller_QueryRange_OK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("expected /api/v1/query_range, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Errorf("query param missing")
		}
		if r.URL.Query().Get("start") == "" {
			t.Errorf("start param missing")
		}
		if r.URL.Query().Get("step") == "" {
			t.Errorf("step param missing")
		}
		_, _ = w.Write([]byte(promRangeOK))
	}))
	defer server.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: server.URL, Timeout: 5 * time.Second})

	from := time.Date(2024, 5, 30, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	series, err := p.QueryRange(context.Background(), `up`, from, to, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(series))
	}
	if len(series[0].Points) != 3 {
		t.Errorf("series[0] expected 3 points, got %d", len(series[0].Points))
	}
	if series[0].Labels["pod"] != "p1" {
		t.Errorf("series[0] label pod: want p1, got %s", series[0].Labels["pod"])
	}
	if series[0].Points[0].V != 0.42 {
		t.Errorf("series[0].Points[0].V: want 0.42, got %f", series[0].Points[0].V)
	}
	if series[0].Points[0].T.Location() != time.UTC {
		t.Errorf("expected UTC point timestamp, got %s", series[0].Points[0].T.Location())
	}
}

func TestMetricsPoller_QueryRange_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: server.URL, Timeout: 5 * time.Second})

	_, err := p.QueryRange(context.Background(), `up`, time.Now().Add(-time.Hour), time.Now(), 30*time.Second)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestMetricsPoller_QueryRange_PromError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(promRangeError))
	}))
	defer server.Close()

	p := NewMetricsPoller(config.DatasourceEndpoint{URL: server.URL, Timeout: 5 * time.Second})
	_, err := p.QueryRange(context.Background(), `bad{`, time.Now().Add(-time.Hour), time.Now(), 30*time.Second)
	if err == nil {
		t.Fatal("expected error from prom error response")
	}
}

func TestLogsPoller_QueryMetricRange_OK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Errorf("expected /loki/api/v1/query_range, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(lokiRangeOK))
	}))
	defer server.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: server.URL, Timeout: 5 * time.Second})

	from := time.Date(2024, 5, 30, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	series, err := p.QueryMetricRange(context.Background(), `sum(rate({app="x"}[1m]))`, from, to, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	if len(series[0].Points) != 2 {
		t.Errorf("expected 2 points, got %d", len(series[0].Points))
	}
	if series[0].Points[0].V != 10 {
		t.Errorf("expected 10, got %f", series[0].Points[0].V)
	}
}

func TestLogsPoller_QueryMetricRange_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	p := NewLogsPoller(config.DatasourceEndpoint{URL: server.URL, Timeout: 5 * time.Second})
	_, err := p.QueryMetricRange(context.Background(), `up`, time.Now().Add(-time.Hour), time.Now(), 30*time.Second)
	if err == nil {
		t.Fatal("expected error on 502")
	}
}
