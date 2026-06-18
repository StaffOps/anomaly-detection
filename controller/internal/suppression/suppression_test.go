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
