package suppression

import (
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

// Filter checks if an anomaly should be suppressed.
type Filter struct {
	excludeAll              map[string]bool // by namespace: suppress all detectors
	excludeStatic           map[string]bool // by namespace: suppress only static (keep adaptive)
	excludeAdaptiveWorkload map[string]bool // by workload: suppress only adaptive (keep static/logs)
}

func NewFilter(cfg config.Suppression) *Filter {
	all := make(map[string]bool, len(cfg.ExcludeNamespaces))
	for _, ns := range cfg.ExcludeNamespaces {
		all[ns] = true
	}
	static := make(map[string]bool, len(cfg.ExcludeStaticOnly))
	for _, ns := range cfg.ExcludeStaticOnly {
		static[ns] = true
	}
	adaptiveWl := make(map[string]bool, len(cfg.ExcludeAdaptiveWorkloads))
	for _, wl := range cfg.ExcludeAdaptiveWorkloads {
		adaptiveWl[wl] = true
	}
	return &Filter{excludeAll: all, excludeStatic: static, excludeAdaptiveWorkload: adaptiveWl}
}

// Suppression reason tags (also used as the `reason` metric label value).
const (
	ReasonNamespaceAll     = "namespace_all"
	ReasonNamespaceStatic  = "namespace_static"
	ReasonAdaptiveWorkload = "adaptive_workload"
)

// SuppressReason returns the reason tag if the anomaly should be silenced, or
// "" if it passes. Keeping the reason (rather than a bare bool) lets callers
// instrument *why* something was dropped without re-deriving the decision.
func (f *Filter) SuppressReason(a detection.Anomaly) string {
	// Workload-scoped adaptive suppression is namespace-independent: bursty infra
	// (Kafka, OTel collectors, service mesh) shares the namespace with real
	// workloads, so we silence only its adaptive noise, not static/log signals.
	if a.Detector == "adaptive" && len(f.excludeAdaptiveWorkload) > 0 {
		if f.excludeAdaptiveWorkload[workloadIdentity(a)] {
			return ReasonAdaptiveWorkload
		}
	}
	ns := a.Labels["namespace"]
	if ns == "" {
		return ""
	}
	if f.excludeAll[ns] {
		return ReasonNamespaceAll
	}
	if f.excludeStatic[ns] && a.Detector == "static" {
		return ReasonNamespaceStatic
	}
	return ""
}

// ShouldSuppress returns true if the anomaly should be silenced.
func (f *Filter) ShouldSuppress(a detection.Anomaly) bool {
	return f.SuppressReason(a) != ""
}

// workloadIdentity resolves the workload name of an anomaly the SAME way the
// controller labels its AnomalyByWorkload metric (cmd/controller/main.go): prefer
// the pod (via ExtractWorkload), and fall back to service_name for span-metric
// anomalies that carry no pod label (e.g. the error_rate_by_service adaptive
// metric). Keeping both paths in sync is what makes the exclude list match what
// operators see in the "top noisy workloads" view.
func workloadIdentity(a detection.Anomaly) string {
	if pod := a.Labels["pod"]; pod != "" {
		if w := correlation.ExtractWorkload(pod); w != "" {
			return w
		}
		return pod
	}
	return a.Labels["service_name"]
}

// FilterAnomalies returns only non-suppressed anomalies.
func (f *Filter) FilterAnomalies(anomalies []detection.Anomaly) []detection.Anomaly {
	result := make([]detection.Anomaly, 0, len(anomalies))
	for _, a := range anomalies {
		if !f.ShouldSuppress(a) {
			result = append(result, a)
		}
	}
	return result
}
