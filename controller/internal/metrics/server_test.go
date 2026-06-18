package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestServer() *Server {
	return NewServer(0, prometheus.NewRegistry())
}

func TestNewServer_NotNil(t *testing.T) {
	s := newTestServer()
	if s == nil {
		t.Fatal("NewServer should not return nil")
	}
}

func TestHandleHealthz_Always200(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.handleHealthz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: want 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("healthz body: want ok, got %q", rec.Body.String())
	}
}

func TestHandleReadyz_NotReady_503(t *testing.T) {
	s := newTestServer()
	// ready defaults to false
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz when not ready: want 503, got %d", rec.Code)
	}
}

func TestHandleReadyz_Ready_NoCheckers_200(t *testing.T) {
	s := newTestServer()
	s.SetReady(true)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("readyz when ready, no checkers: want 200, got %d", rec.Code)
	}
}

func TestHandleReadyz_Ready_FailingChecker_503(t *testing.T) {
	s := newTestServer()
	s.SetReady(true)
	s.AddReadinessCheck(func(_ context.Context) error {
		return fmt.Errorf("redis unavailable")
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz with failing checker: want 503, got %d", rec.Code)
	}
}

func TestHandleReadyz_Ready_PassingCheckers_200(t *testing.T) {
	s := newTestServer()
	s.SetReady(true)
	s.AddReadinessCheck(func(_ context.Context) error { return nil })
	s.AddReadinessCheck(func(_ context.Context) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("readyz with all passing checkers: want 200, got %d", rec.Code)
	}
}

func TestSetReady_Toggle(t *testing.T) {
	s := newTestServer()
	s.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.handleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("after SetReady(true): want 200, got %d", rec.Code)
	}

	s.SetReady(false)
	rec2 := httptest.NewRecorder()
	s.handleReadyz(rec2, req)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Errorf("after SetReady(false): want 503, got %d", rec2.Code)
	}
}

func TestMustRegisterController_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustRegisterController panicked: %v", r)
		}
	}()
	reg := prometheus.NewRegistry()
	MustRegisterController(reg)
}

func TestMustRegisterWorker_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustRegisterWorker panicked: %v", r)
		}
	}()
	reg := prometheus.NewRegistry()
	MustRegisterWorker(reg)
}

func TestStart_And_Shutdown(t *testing.T) {
	// Use port 0 to let OS pick a free port (Start won't bind to port 0 directly,
	// but we can call Start and immediately Shutdown to exercise the code path).
	// We use a non-zero port that's unlikely to be in use but accepted by the OS.
	s := NewServer(0, prometheus.NewRegistry())
	s.Start()
	// use background context
	// Shutdown should return promptly since server just started (or never fully bound)
	_ = s.Shutdown(context.Background())
}
