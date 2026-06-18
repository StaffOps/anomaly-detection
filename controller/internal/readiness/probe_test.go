package readiness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var testClient = &http.Client{Timeout: time.Second}

// ─── probeHTTP ────────────────────────────────────────────────────────────────

func TestProbeHTTP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	if err := probeHTTP(context.Background(), testClient, srv.URL); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestProbeHTTP_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	if err := probeHTTP(context.Background(), testClient, srv.URL); err == nil {
		t.Error("expected error for 503 response")
	}
}

func TestProbeHTTP_NetworkError(t *testing.T) {
	err := probeHTTP(context.Background(), testClient, "http://127.0.0.1:1")
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestProbeHTTP_InvalidURL(t *testing.T) {
	err := probeHTTP(context.Background(), testClient, "://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestProbeHTTP_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := probeHTTP(ctx, testClient, srv.URL); err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ─── probeJSON ────────────────────────────────────────────────────────────────

func TestProbeJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	err := probeJSON(context.Background(), testClient, srv.URL, func(body []byte) error { return nil })
	if err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestProbeJSON_ValidateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error"}`))
	}))
	defer srv.Close()
	err := probeJSON(context.Background(), testClient, srv.URL, func(body []byte) error {
		return fmt.Errorf("validation failed")
	})
	if err == nil {
		t.Error("expected error from validator")
	}
}

func TestProbeJSON_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	err := probeJSON(context.Background(), testClient, srv.URL, func(body []byte) error { return nil })
	if err == nil {
		t.Error("expected error for 503 response")
	}
}

func TestProbeJSON_NetworkError(t *testing.T) {
	err := probeJSON(context.Background(), testClient, "http://127.0.0.1:1", func(body []byte) error { return nil })
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestProbeJSON_InvalidURL(t *testing.T) {
	err := probeJSON(context.Background(), testClient, "://invalid", func(body []byte) error { return nil })
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
