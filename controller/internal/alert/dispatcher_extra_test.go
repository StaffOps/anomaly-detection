package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
)

// ─── buildAlert — missing branches ───────────────────────────────────────────

func TestBuildAlert_WithPodLabel(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	a := baseAnomaly("warning", "adaptive", "metrics")
	a.Labels["pod"] = "api-abc-xyz"
	alert := d.buildAlert(a, correlation.KindPod, "api-abc-xyz", "api", "reason")
	if alert.Annotations["pod"] != "api-abc-xyz" {
		t.Errorf("pod label should be in annotations, got %q", alert.Annotations["pod"])
	}
}

func TestBuildAlert_WithLinks(t *testing.T) {
	lb := NewLinkBuilder(config.Links{
		GrafanaBaseURL:   "https://grafana.test",
		RunbookBaseURL:   "https://docs.test/runbooks",
	})
	d := &Dispatcher{
		client:  &http.Client{Timeout: time.Second},
		url:     "http://localhost/api/v2/alerts",
		dryRun:  true,
		cluster: "test",
		links:   lb,
	}
	a := baseAnomaly("warning", "adaptive", "metrics")
	a.Labels["namespace"] = "prod"
	a.Labels["pod"] = "api-abc"
	alert := d.buildAlert(a, correlation.KindPod, "api-abc", "api", "reason")
	// With a link builder, grafana/runbook URLs should be set
	if alert.Annotations["grafana_url"] == "" {
		t.Error("grafana_url annotation should be set when link builder is configured")
	}
}

// ─── amWorkloadLabel — missing branches ──────────────────────────────────────

func TestAmWorkloadLabel_PodNameNotExtractable(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	// Pod name with no recognizable pattern → ExtractWorkload returns the pod name itself
	rep := detection.Anomaly{Labels: map[string]string{"pod": "simplename"}}
	got := amWorkloadLabel(ca, rep)
	// Should return the pod name as-is when no pattern matches
	if got == "" {
		t.Error("amWorkloadLabel should return something even for unextractable pod names")
	}
}

func TestAmWorkloadLabel_ServiceFallback(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod}
	rep := detection.Anomaly{Labels: map[string]string{"service_name": "api-svc"}}
	got := amWorkloadLabel(ca, rep)
	if got != "api-svc" {
		t.Errorf("want api-svc, got %q", got)
	}
}

func TestAmWorkloadLabel_WorkloadFallback(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod, Workload: "fallback-workload"}
	rep := detection.Anomaly{Labels: map[string]string{}} // no pod, no service
	got := amWorkloadLabel(ca, rep)
	if got != "fallback-workload" {
		t.Errorf("want fallback-workload, got %q", got)
	}
}

// ─── FireCorrelated — missing ML + workload paths ────────────────────────────

func TestFireCorrelated_WithML_DryRun(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	ca := baseCorrelated("critical")
	ca.MLDetection = &correlation.MLDetection{
		IsAnomaly:    true,
		Score:        0.9,
		Contributors: []string{"cpu_ratio", "memory_ratio"},
		FeatureCount: 10,
	}
	if err := d.FireCorrelated(context.Background(), ca); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFireCorrelated_WorkloadKind_DryRun(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	ca := correlation.CorrelatedAlert{
		Kind:             correlation.KindWorkload,
		Anomalies:        []detection.Anomaly{baseAnomaly("critical", "adaptive", "metrics")},
		Severity:         "critical",
		Namespace:        "prod",
		Workload:         "api",
		Signals:          []string{"metrics"},
		AffectedPods:     []string{"api-abc", "api-def", "api-ghi"},
		AffectedReplicas: 3,
	}
	if err := d.FireCorrelated(context.Background(), ca); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFireCorrelated_WithEnrichment_RealMode(t *testing.T) {
	var received []alertmanagerAlert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := newTestDispatcher(srv.URL, false)
	ca := baseCorrelated("warning")
	ca.Enrichment = enrichment.Bundle{
		Results: []enrichment.Result{
			{Name: "cpu_ratio", Value: 0.85, Error: ""},
			{Name: "mem_ratio", Value: 0.0, Error: "query failed"},
		},
	}
	_ = received
	if err := d.FireCorrelated(context.Background(), ca); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── identityOf — service fallback ───────────────────────────────────────────

func TestIdentityOf_PodKind_NoLabels_FallbackWorkload(t *testing.T) {
	ca := correlation.CorrelatedAlert{Kind: correlation.KindPod, Workload: "api"}
	rep := detection.Anomaly{Labels: map[string]string{}} // no pod, no service
	got := identityOf(ca, rep)
	if got != "api" {
		t.Errorf("want api (workload fallback), got %q", got)
	}
}

// ─── buildLogQL — missing branches ───────────────────────────────────────────

func TestBuildLogQL_ServiceOnly(t *testing.T) {
	a := detection.Anomaly{Labels: map[string]string{"service_name": "api-svc"}}
	got := buildLogQL(a)
	if got == "" {
		t.Error("service-only LogQL should not be empty")
	}
	if got != `{service_name="api-svc"}` {
		t.Errorf("unexpected LogQL: %q", got)
	}
}

func TestBuildLogQL_NamespaceOnly(t *testing.T) {
	a := detection.Anomaly{Labels: map[string]string{"namespace": "prod"}}
	got := buildLogQL(a)
	if got == "" {
		t.Error("namespace-only LogQL should not be empty")
	}
}

func TestBuildLogQL_NoLabels_Empty(t *testing.T) {
	a := detection.Anomaly{Labels: map[string]string{}}
	got := buildLogQL(a)
	if got != "" {
		t.Errorf("no labels should produce empty LogQL, got %q", got)
	}
}

// ─── buildPromQLExpr — missing branches ──────────────────────────────────────

func TestBuildPromQLExpr_EmptyMetric_ReturnsUp(t *testing.T) {
	a := detection.Anomaly{Labels: map[string]string{"namespace": "prod"}}
	got := buildPromQLExpr(a)
	if got != "up" {
		t.Errorf("empty metric name should return 'up', got %q", got)
	}
}

func TestBuildPromQLExpr_ServiceOnly(t *testing.T) {
	a := detection.Anomaly{
		MetricName: "http_errors",
		Labels:     map[string]string{"service_name": "api"},
	}
	got := buildPromQLExpr(a)
	if got == "" || got == "http_errors" {
		t.Errorf("service-only expr should include service filter, got %q", got)
	}
}

func TestBuildPromQLExpr_NoLabels(t *testing.T) {
	a := detection.Anomaly{
		MetricName: "cpu_rate",
		Labels:     map[string]string{},
	}
	got := buildPromQLExpr(a)
	if got != "cpu_rate" {
		t.Errorf("no labels should return bare metric name, got %q", got)
	}
}

// ─── attachContext — error enrichment path ────────────────────────────────────

func TestAttachContext_EnrichmentWithError(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	al := alertmanagerAlert{Labels: map[string]string{}, Annotations: map[string]string{}}
	ca := correlation.CorrelatedAlert{
		Enrichment: enrichment.Bundle{
			Results: []enrichment.Result{
				{Name: "cpu_ratio", Error: "query timeout"},
			},
		},
	}
	d.attachContext(&al, ca)
	if al.Annotations["enrich_cpu_ratio"] == "" {
		t.Error("error enrichment should still produce an annotation")
	}
}

// ─── MLDetection not anomaly ──────────────────────────────────────────────────

func TestAttachContext_MLNotAnomaly(t *testing.T) {
	d := newTestDispatcher("http://localhost", true)
	al := alertmanagerAlert{Labels: map[string]string{}, Annotations: map[string]string{}}
	ca := correlation.CorrelatedAlert{
		MLDetection: &correlation.MLDetection{
			IsAnomaly:    false,
			Score:        0.5,
			FeatureCount: 5,
		},
	}
	d.attachContext(&al, ca)
	if al.Labels["ml_confirmed"] == "true" {
		t.Error("non-anomaly ML result should not set ml_confirmed")
	}
	if al.Annotations["ml_score"] == "" {
		t.Error("ml_score annotation should be set even when not anomaly")
	}
}
