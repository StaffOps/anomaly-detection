package alert

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

func testLinks() config.Links {
	return config.Links{
		GrafanaBaseURL:            "https://grafana.test",
		TempoBaseURL:              "https://grafana.test",
		LokiBaseURL:               "https://grafana.test",
		RunbookBaseURL:            "https://docs.test/runbooks",
		GrafanaPromDatasourceUID:  "vm-uid",
		GrafanaTempoDatasourceUID: "tempo-uid",
		GrafanaLokiDatasourceUID:  "loki-uid",
	}
}

func podAnomaly() detection.Anomaly {
	return detection.Anomaly{
		MetricName: "cpu_by_workload",
		Labels:     map[string]string{"namespace": "monitoring", "pod": "vm-cluster-vmstorage-0"},
		Detector:   "adaptive",
		Timestamp:  time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
}

func serviceAnomaly() detection.Anomaly {
	return detection.Anomaly{
		MetricName: "latency_p99_by_service",
		Labels:     map[string]string{"service_name": "DataPlatform.Views"},
		Detector:   "ml_isolation_forest",
		Timestamp:  time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
}

func TestLinkBuilder_GrafanaContainsLabels(t *testing.T) {
	b := NewLinkBuilder(testLinks())
	got := b.Grafana(podAnomaly())
	if got == "" {
		t.Fatal("expected Grafana URL, got empty string")
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("URL not parseable: %v", err)
	}
	if !strings.HasPrefix(parsed.Host, "grafana.test") {
		t.Errorf("unexpected host: %s", parsed.Host)
	}
	// Pane payload (URL-encoded) should reference both labels and the metric.
	panes := parsed.Query().Get("panes")
	for _, want := range []string{"cpu_by_workload", "monitoring", "vm-cluster-vmstorage-0", "vm-uid"} {
		if !strings.Contains(panes, want) {
			t.Errorf("Grafana panes missing %q: %s", want, panes)
		}
	}
}

func TestLinkBuilder_TempoForService(t *testing.T) {
	b := NewLinkBuilder(testLinks())
	got := b.Tempo(serviceAnomaly())
	if got == "" {
		t.Fatal("expected Tempo URL, got empty")
	}
	parsed, _ := url.Parse(got)
	panes := parsed.Query().Get("panes")
	if !strings.Contains(panes, "DataPlatform.Views") {
		t.Errorf("Tempo URL missing service name: %s", panes)
	}
	if !strings.Contains(panes, "tempo-uid") {
		t.Errorf("Tempo URL missing datasource uid: %s", panes)
	}
}

func TestLinkBuilder_LokiForPod(t *testing.T) {
	b := NewLinkBuilder(testLinks())
	got := b.Loki(podAnomaly())
	if got == "" {
		t.Fatal("expected Loki URL")
	}
	parsed, _ := url.Parse(got)
	panes := parsed.Query().Get("panes")
	for _, want := range []string{"k8s_namespace_name", "monitoring", "k8s_pod_name", "vm-cluster-vmstorage-0", "loki-uid"} {
		if !strings.Contains(panes, want) {
			t.Errorf("Loki URL missing %q: %s", want, panes)
		}
	}
}

func TestLinkBuilder_RunbookPerDetector(t *testing.T) {
	b := NewLinkBuilder(testLinks())
	got := b.Runbook("adaptive")
	if got != "https://docs.test/runbooks/adaptive" {
		t.Errorf("unexpected runbook URL: %s", got)
	}
	if b.Runbook("") != "" {
		t.Error("empty detector should produce empty URL")
	}
}

func TestLinkBuilder_DisabledWhenBaseEmpty(t *testing.T) {
	b := NewLinkBuilder(config.Links{}) // all empty
	if b.Grafana(podAnomaly()) != "" {
		t.Error("Grafana should be empty when base is empty")
	}
	if b.Tempo(serviceAnomaly()) != "" {
		t.Error("Tempo should be empty when base is empty")
	}
	if b.Loki(podAnomaly()) != "" {
		t.Error("Loki should be empty when base is empty")
	}
	if b.Runbook("adaptive") != "" {
		t.Error("Runbook should be empty when base is empty")
	}
}

func TestLinkBuilder_NoUsableLabelsFallsBack(t *testing.T) {
	b := NewLinkBuilder(testLinks())
	a := detection.Anomaly{MetricName: "up", Detector: "static", Timestamp: time.Now()}
	// Tempo should produce nothing without service/pod
	if b.Tempo(a) != "" {
		t.Error("Tempo should be empty without service/pod")
	}
	// Loki should produce nothing without ns/pod/svc
	if b.Loki(a) != "" {
		t.Error("Loki should be empty without identity labels")
	}
	// Grafana should still produce something (raw metric)
	if got := b.Grafana(a); got == "" {
		t.Error("Grafana should fall back to raw metric")
	}
}
