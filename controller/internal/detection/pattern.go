package detection

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// PatternDetector matches K8s events against known anomalous patterns.
type PatternDetector struct {
	patterns map[string]bool
}

func NewPatternDetector(patterns []string) *PatternDetector {
	m := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		m[p] = true
	}
	return &PatternDetector{patterns: m}
}

// severity mapping for known event reasons.
var eventSeverity = map[string]string{
	"OOMKilled":        "critical",
	"CrashLoopBackOff": "critical",
	"Evicted":          "critical",
	"FailedScheduling": "warning",
	"FailedMount":      "warning",
	"BackOff":          "warning",
}

// EvaluateEvent returns an Anomaly if the event matches a known pattern.
func (d *PatternDetector) EvaluateEvent(event ingestion.EventAnomaly) *Anomaly {
	if !d.patterns[event.Reason] {
		return nil
	}

	severity := eventSeverity[event.Reason]
	if severity == "" {
		severity = "warning"
	}

	metrics.WorkerDetections.Add(context.Background(), 1, metric.WithAttributes(attribute.String("detector", "pattern")))

	return &Anomaly{
		MetricName: event.Reason,
		Labels: map[string]string{
			"namespace": event.Namespace,
			"pod":       event.Pod,
		},
		Value:     float64(event.Count),
		Score:     float64(event.Count),
		Severity:  severity,
		Signal:    "events",
		Detector:  "pattern",
		Timestamp: event.Timestamp,
	}
}
