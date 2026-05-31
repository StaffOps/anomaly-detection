package detection

import (
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// StaticDetector evaluates samples against fixed thresholds.
type StaticDetector struct {
	rules []config.StaticRule
}

func NewStaticDetector(rules []config.StaticRule) *StaticDetector {
	return &StaticDetector{rules: rules}
}

// Evaluate checks all samples against a single rule's threshold.
func (d *StaticDetector) Evaluate(rule config.StaticRule, samples []ingestion.Sample) []Anomaly {
	var anomalies []Anomaly
	for _, s := range samples {
		if breaches(s.Value, rule.Threshold, rule.Operator) {
			anomalies = append(anomalies, Anomaly{
				MetricName: rule.Name,
				Labels:     s.Labels,
				Value:      s.Value,
				Score:      s.Value / rule.Threshold, // ratio above threshold
				Severity:   rule.Severity,
				Signal:     "metrics",
				Detector:   "static",
				Timestamp:  time.Now(),
			})
		}
	}
	if len(anomalies) > 0 {
		metrics.WorkerDetections.WithLabelValues("static").Add(float64(len(anomalies)))
	}
	return anomalies
}

func breaches(value, threshold float64, op string) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	default:
		return value > threshold
	}
}
