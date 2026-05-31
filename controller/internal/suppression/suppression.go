package suppression

import (
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

// Filter checks if an anomaly should be suppressed.
type Filter struct {
	excludeAll    map[string]bool // suppress all detectors
	excludeStatic map[string]bool // suppress only static (keep adaptive)
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
	return &Filter{excludeAll: all, excludeStatic: static}
}

// ShouldSuppress returns true if the anomaly should be silenced.
func (f *Filter) ShouldSuppress(a detection.Anomaly) bool {
	ns := a.Labels["namespace"]
	if ns == "" {
		return false
	}
	if f.excludeAll[ns] {
		return true
	}
	if f.excludeStatic[ns] && a.Detector == "static" {
		return true
	}
	return false
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
