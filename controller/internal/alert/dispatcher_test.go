package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
)

func newTestDispatcher(url string, dryRun bool) *Dispatcher {
	return NewDispatcher(
		config.DatasourceEndpoint{URL: url},
		dryRun,
		"test-cluster",
		nil, // no link builder
	)
}

func baseAnomaly(severity, detector, signal string) detection.Anomaly {
	return detection.Anomaly{
		MetricName: "test_metric",
		Labels: map[string]string{
			"namespace": "prod",
			"pod":       "api-abc-xyz",
		},
		Value:     10.0,
		Mean:      1.0,
		Stddev:    0.5,
		Score:     18.0,
		Severity:  severity,
		Detector:  detector,
		Signal:    signal,
		Timestamp: time.Now(),
	}
}

func baseCorrelated(severity string) correlation.CorrelatedAlert {
	return correlation.CorrelatedAlert{
		Kind:      correlation.KindPod,
		Anomalies: []detection.Anomaly{baseAnomaly(severity, "adaptive", "metrics")},
		Severity:  severity,
		Namespace: "prod",
		Workload:  "api-abc-xyz",
		Signals:   []string{"metrics"},
	}
}

// ─── Dry-run tests ────────────────────────────────────────────────────────────

func TestFireCorrelated_DryRun_NoHTTPCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, true)
	err := d.FireCorrelated(context.Background(), baseCorrelated("warning"))
	if err != nil {
		t.Fatalf("dry-run should not return error: %v", err)
	}
	if called {
		t.Error("dry-run should not call Alertmanager")
	}
}

func TestFire_DryRun_NoHTTPCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, true)
	err := d.Fire(context.Background(), baseAnomaly("critical", "static", "metrics"))
	if err != nil {
		t.Fatalf("dry-run should not return error: %v", err)
	}
	if called {
		t.Error("dry-run should not call Alertmanager")
	}
}

func TestFireCorrelated_EmptyAnomalies_NoOp(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	ca := correlation.CorrelatedAlert{} // no anomalies
	err := d.FireCorrelated(context.Background(), ca)
	if err != nil {
		t.Fatalf("empty anomalies should be a no-op: %v", err)
	}
}

// ─── Real dispatch tests ──────────────────────────────────────────────────────

func TestFireCorrelated_RealMode_SendsAlert(t *testing.T) {
	var received []alertmanagerAlert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, false)
	err := d.FireCorrelated(context.Background(), baseCorrelated("critical"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 alert sent, got %d", len(received))
	}
	if received[0].Labels["severity"] != "critical" {
		t.Errorf("expected severity=critical, got %q", received[0].Labels["severity"])
	}
	if received[0].Labels["cluster"] != "test-cluster" {
		t.Errorf("expected cluster=test-cluster, got %q", received[0].Labels["cluster"])
	}
}

func TestFire_RealMode_SendsAlert(t *testing.T) {
	var received []alertmanagerAlert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, false)
	err := d.Fire(context.Background(), baseAnomaly("warning", "adaptive", "metrics"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 alert sent, got %d", len(received))
	}
}

func TestSend_Non2xxResponse_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, false)
	err := d.FireCorrelated(context.Background(), baseCorrelated("warning"))
	if err == nil {
		t.Error("expected error for non-2xx response")
	}
}

func TestSend_UnreachableServer_ReturnsError(t *testing.T) {
	d := newTestDispatcher("http://127.0.0.1:1", false)
	err := d.FireCorrelated(context.Background(), baseCorrelated("warning"))
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ─── Alert payload tests ──────────────────────────────────────────────────────

func TestBuildAlert_Labels(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	a := baseAnomaly("critical", "static", "metrics")
	alert := d.buildAlert(a, correlation.KindPod, "api-pod", "api", "threshold breach")

	if alert.Labels["alertname"] != "AnomalyDetected" {
		t.Errorf("alertname: want AnomalyDetected, got %q", alert.Labels["alertname"])
	}
	if alert.Labels["severity"] != "critical" {
		t.Errorf("severity: want critical, got %q", alert.Labels["severity"])
	}
	if alert.Labels["detector"] != "static" {
		t.Errorf("detector: want static, got %q", alert.Labels["detector"])
	}
	if alert.Labels["workload"] != "api" {
		t.Errorf("workload: want api, got %q", alert.Labels["workload"])
	}
}

func TestBuildAlert_Annotations(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	a := baseAnomaly("warning", "adaptive", "metrics")
	alert := d.buildAlert(a, correlation.KindPod, "api-pod", "api", "z-score spike")

	if alert.Annotations["summary"] == "" {
		t.Error("summary annotation should not be empty")
	}
	if alert.Annotations["metric"] != "test_metric" {
		t.Errorf("metric: want test_metric, got %q", alert.Annotations["metric"])
	}
}

// ─── simpleReason tests ───────────────────────────────────────────────────────

func TestSimpleReason_Static(t *testing.T) {
	a := baseAnomaly("warning", "static", "metrics")
	r := simpleReason(a)
	if r == "" || r == "anomaly detected" {
		t.Errorf("static reason should be specific, got %q", r)
	}
}

func TestSimpleReason_Adaptive(t *testing.T) {
	a := baseAnomaly("warning", "adaptive", "metrics")
	a.Score = 4.2
	r := simpleReason(a)
	if r == "" {
		t.Error("adaptive reason should not be empty")
	}
}

func TestSimpleReason_MLIsolationForest(t *testing.T) {
	a := baseAnomaly("critical", "ml_isolation_forest", "metrics")
	a.Score = 0.87
	r := simpleReason(a)
	if r == "" {
		t.Error("ml reason should not be empty")
	}
}

func TestSimpleReason_Pattern(t *testing.T) {
	a := baseAnomaly("warning", "pattern", "events")
	a.MetricName = "CrashLoopBackOff"
	r := simpleReason(a)
	if r == "" {
		t.Error("pattern reason should not be empty")
	}
}

func TestSimpleReason_Unknown(t *testing.T) {
	a := detection.Anomaly{Detector: "unknown_detector", MetricName: "some_metric"}
	r := simpleReason(a)
	if r != "some_metric" {
		t.Errorf("unknown detector with metric name should return metric name, got %q", r)
	}
}

func TestSimpleReason_CompletelyEmpty(t *testing.T) {
	r := simpleReason(detection.Anomaly{})
	if r != "anomaly detected" {
		t.Errorf("empty anomaly should return default reason, got %q", r)
	}
}

// ─── identityOf / amWorkloadLabel tests ──────────────────────────────────────

func TestIdentityOf_WorkloadKind(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindWorkload, Workload: "api"}
	rep := detection.Anomaly{}
	got := identityOf(ca, rep)
	if got != "api" {
		t.Errorf("KindWorkload identity: want api, got %q", got)
	}
}

func TestIdentityOf_PodKind_WithPod(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	rep := detection.Anomaly{Labels: map[string]string{"pod": "api-abc-xyz"}}
	got := identityOf(ca, rep)
	if got != "api-abc-xyz" {
		t.Errorf("KindPod with pod label: want api-abc-xyz, got %q", got)
	}
}

func TestIdentityOf_PodKind_ServiceFallback(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	rep := detection.Anomaly{Labels: map[string]string{"service_name": "api-svc"}}
	got := identityOf(ca, rep)
	if got != "api-svc" {
		t.Errorf("KindPod without pod label: want api-svc, got %q", got)
	}
}

func TestAmWorkloadLabel_WorkloadKind(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindWorkload, Workload: "my-deploy"}
	rep := detection.Anomaly{}
	got := amWorkloadLabel(ca, rep)
	if got != "my-deploy" {
		t.Errorf("want my-deploy, got %q", got)
	}
}

func TestAmWorkloadLabel_ExtractsFromPod(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	rep := detection.Anomaly{
		Labels: map[string]string{"pod": "api-6d9ff7b9-xkrqz"},
	}
	got := amWorkloadLabel(ca, rep)
	// ExtractWorkload should return "api" from this pod name pattern
	if got == "api-6d9ff7b9-xkrqz" {
		t.Errorf("should have extracted workload from pod name, got raw pod name")
	}
}

// ─── attachContext / attachWorkloadFields tests ───────────────────────────────

func TestAttachContext_WithEnrichment(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	alert := alertmanagerAlert{
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
	ca := correlation.CorrelatedAlert{
		Enrichment: enrichment.Bundle{
			Results: []enrichment.Result{
				{Name: "cpu_ratio", Value: 0.85},
				{Name: "memory_ratio", Value: 0.60},
			},
		},
	}
	d.attachContext(&alert, ca)

	if alert.Labels["enriched"] != "true" {
		t.Error("enriched label should be set to true")
	}
	if alert.Annotations["enrich_cpu_ratio"] == "" {
		t.Error("cpu_ratio enrichment annotation should be set")
	}
	if alert.Annotations["context"] == "" {
		t.Error("context annotation should be set")
	}
}

func TestAttachContext_WithMLDetection(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	alert := alertmanagerAlert{
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
	ca := correlation.CorrelatedAlert{
		MLDetection: &correlation.MLDetection{
			IsAnomaly:    true,
			Score:        0.87,
			Contributors: []string{"cpu_ratio", "memory_ratio"},
			FeatureCount: 10,
		},
	}
	d.attachContext(&alert, ca)

	if alert.Labels["ml_confirmed"] != "true" {
		t.Error("ml_confirmed label should be set")
	}
	if alert.Annotations["ml_contributors"] == "" {
		t.Error("ml_contributors annotation should be set")
	}
}

func TestAttachWorkloadFields_WorkloadKind(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	alert := alertmanagerAlert{
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
	ca := correlation.CorrelatedAlert{
		Kind:             correlation.KindWorkload,
		AffectedPods:     []string{"api-abc", "api-def"},
		AffectedReplicas: 2,
	}
	d.attachWorkloadFields(&alert, ca)

	if alert.Labels["workload_pattern"] != "true" {
		t.Error("workload_pattern label should be set")
	}
	if alert.Annotations["affected_replicas"] != "2" {
		t.Errorf("affected_replicas: want 2, got %q", alert.Annotations["affected_replicas"])
	}
}

func TestAttachWorkloadFields_PodKind_NoOp(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	alert := alertmanagerAlert{
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	d.attachWorkloadFields(&alert, ca)

	if alert.Labels["workload_pattern"] != "" {
		t.Error("workload_pattern should not be set for pod-kind alerts")
	}
}

func TestBuildReason_WorkloadKind(t *testing.T) {
	ca := correlation.CorrelatedAlert{
		Kind:             correlation.KindWorkload,
		AffectedReplicas: 4,
	}
	rep := baseAnomaly("critical", "adaptive", "metrics")
	r := buildReason(ca, rep)
	if r == "" {
		t.Error("workload reason should not be empty")
	}
}
