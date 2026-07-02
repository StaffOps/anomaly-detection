package readiness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

func TestPromChecker_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	check := PromChecker(config.DatasourceEndpoint{URL: srv.URL, Timeout: 2 * time.Second})
	if err := check(context.Background()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPromChecker_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	check := PromChecker(config.DatasourceEndpoint{URL: srv.URL})
	if err := check(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPromChecker_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error","error":"oops"}`))
	}))
	defer srv.Close()
	check := PromChecker(config.DatasourceEndpoint{URL: srv.URL})
	if err := check(context.Background()); err == nil {
		t.Fatal("expected error on status=error")
	}
}

func TestLokiChecker_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"status":"success","data":[]}`))
	}))
	defer srv.Close()
	check := LokiChecker(config.DatasourceEndpoint{URL: srv.URL})
	if err := check(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLokiChecker_Down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	check := LokiChecker(config.DatasourceEndpoint{URL: srv.URL})
	if err := check(context.Background()); err == nil {
		t.Fatal("expected error on 502")
	}
}

func TestAlertmanagerChecker_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"cluster":{"status":"ready"}}`))
	}))
	defer srv.Close()
	check := AlertmanagerChecker(config.DatasourceEndpoint{URL: srv.URL})
	if err := check(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClampReadinessTimeout(t *testing.T) {
	if got := clamp(0); got != readinessCap {
		t.Errorf("zero should clamp to %v, got %v", readinessCap, got)
	}
	if got := clamp(time.Second); got != time.Second {
		t.Errorf("under-cap should pass through, got %v", got)
	}
	if got := clamp(10 * time.Second); got != readinessCap {
		t.Errorf("over-cap should be clamped, got %v", got)
	}
}
