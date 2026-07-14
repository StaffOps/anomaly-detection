package suppression

import (
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

func anomaly(ns, detector string) detection.Anomaly {
	return detection.Anomaly{
		Labels:   map[string]string{"namespace": ns},
		Detector: detector,
	}
}

func TestNewFilter_EmptyConfig(t *testing.T) {
	f := NewFilter(config.Suppression{})
	if f.ShouldSuppress(anomaly("prod", "static")) {
		t.Error("empty filter should suppress nothing")
	}
}

func TestShouldSuppress_ExcludeAll(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeNamespaces: []string{"kube-system", "monitoring"},
	})

	cases := []struct {
		ns       string
		detector string
		want     bool
	}{
		{"kube-system", "static", true},
		{"kube-system", "adaptive", true},
		{"monitoring", "adaptive", true},
		{"prod", "static", false},
		{"", "static", false},
	}
	for _, c := range cases {
		got := f.ShouldSuppress(anomaly(c.ns, c.detector))
		if got != c.want {
			t.Errorf("ns=%q detector=%q: want suppress=%v got %v", c.ns, c.detector, c.want, got)
		}
	}
}

func TestShouldSuppress_ExcludeStaticOnly(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeStaticOnly: []string{"batch"},
	})

	cases := []struct {
		ns       string
		detector string
		want     bool
	}{
		{"batch", "static", true},
		{"batch", "adaptive", false},
		{"batch", "pattern", false},
		{"prod", "static", false},
	}
	for _, c := range cases {
		got := f.ShouldSuppress(anomaly(c.ns, c.detector))
		if got != c.want {
			t.Errorf("ns=%q detector=%q: want suppress=%v got %v", c.ns, c.detector, c.want, got)
		}
	}
}

func TestShouldSuppress_ExcludeAllTakesPrecedence(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeNamespaces: []string{"mixed"},
		ExcludeStaticOnly: []string{"mixed"},
	})
	// ExcludeAll should suppress both detectors
	if !f.ShouldSuppress(anomaly("mixed", "adaptive")) {
		t.Error("ExcludeAll should suppress adaptive too")
	}
}

func TestShouldSuppress_NoNamespaceLabel(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeNamespaces: []string{"kube-system"},
	})
	a := detection.Anomaly{Labels: map[string]string{}, Detector: "static"}
	if f.ShouldSuppress(a) {
		t.Error("anomaly with no namespace label should never be suppressed")
	}
}

func anomalyPod(ns, detector, pod string) detection.Anomaly {
	return detection.Anomaly{
		Labels:   map[string]string{"namespace": ns, "pod": pod},
		Detector: detector,
	}
}

func TestShouldSuppress_ExcludeAdaptiveWorkload(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeAdaptiveWorkloads: []string{"strimzi-kafka-brokers", "otel-agent-logs-collector"},
	})

	cases := []struct {
		name     string
		detector string
		pod      string
		want     bool
	}{
		// Adaptive noise from a listed workload is silenced (pod → workload via regex).
		{"adaptive excluded (statefulset pod)", "adaptive", "strimzi-kafka-brokers-2", true},
		{"adaptive excluded (deployment pod)", "adaptive", "otel-agent-logs-collector-558596ddb7-4db97", true},
		// Static / log signals from the same workload still fire.
		{"static from excluded workload fires", "static", "strimzi-kafka-brokers-2", false},
		{"pattern from excluded workload fires", "pattern", "strimzi-kafka-brokers-2", false},
		// Adaptive from a non-listed workload fires.
		{"adaptive non-excluded", "adaptive", "my-app-558596ddb7-4db97", false},
	}
	for _, c := range cases {
		got := f.ShouldSuppress(anomalyPod("staffops", c.detector, c.pod))
		if got != c.want {
			t.Errorf("%s: want suppress=%v got %v", c.name, c.want, got)
		}
	}
}

func TestShouldSuppress_ExcludeAdaptiveWorkload_ServiceNameFallback(t *testing.T) {
	// Span-metric anomalies (error_rate_by_service) carry service_name but no pod.
	// Must match the exclude list the same way the controller's by-workload metric
	// does — otherwise these bypass suppression (the homolog bug of 2026-07-14).
	f := NewFilter(config.Suppression{
		ExcludeAdaptiveWorkloads: []string{"strimzi-kafka-brokers"},
	})
	svcAnomaly := detection.Anomaly{
		Labels:   map[string]string{"service_name": "strimzi-kafka-brokers"},
		Detector: "adaptive",
	}
	if !f.ShouldSuppress(svcAnomaly) {
		t.Error("adaptive anomaly keyed by service_name (no pod) must be suppressed")
	}
	// Static signal from the same service still fires.
	svcStatic := detection.Anomaly{
		Labels:   map[string]string{"service_name": "strimzi-kafka-brokers"},
		Detector: "static",
	}
	if f.ShouldSuppress(svcStatic) {
		t.Error("static signal must NOT be suppressed even for excluded workload")
	}
}

func TestShouldSuppress_ExcludeAdaptiveWorkload_NamespaceIndependent(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeAdaptiveWorkloads: []string{"istiod"},
	})
	// Same workload suppressed regardless of namespace — the whole point is that
	// bursty infra shares a namespace with real workloads.
	if !f.ShouldSuppress(anomalyPod("staffops", "adaptive", "istiod-abcde")) {
		t.Error("expected suppression in staffops")
	}
	if !f.ShouldSuppress(anomalyPod("istio-system", "adaptive", "istiod-abcde")) {
		t.Error("expected suppression in istio-system too (namespace-independent)")
	}
}

func TestFilterAnomalies(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeNamespaces: []string{"kube-system"},
		ExcludeStaticOnly: []string{"batch"},
	})

	input := []detection.Anomaly{
		anomaly("prod", "adaptive"),
		anomaly("kube-system", "adaptive"),
		anomaly("batch", "static"),
		anomaly("batch", "adaptive"),
		anomaly("", "static"),
	}

	got := f.FilterAnomalies(input)
	if len(got) != 3 {
		t.Errorf("expected 3 anomalies after filtering, got %d: %v", len(got), got)
	}
}

func TestFilterAnomalies_NilInput(t *testing.T) {
	f := NewFilter(config.Suppression{})
	got := f.FilterAnomalies(nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %v", got)
	}
}

func TestFilterAnomalies_AllSuppressed(t *testing.T) {
	f := NewFilter(config.Suppression{
		ExcludeNamespaces: []string{"kube-system"},
	})
	input := []detection.Anomaly{
		anomaly("kube-system", "static"),
		anomaly("kube-system", "adaptive"),
	}
	got := f.FilterAnomalies(input)
	if len(got) != 0 {
		t.Errorf("expected all suppressed, got %d", len(got))
	}
}
