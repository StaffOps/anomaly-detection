package detection

import (
	"context"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/baseline"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Anomaly is the unified output of any detector.
type Anomaly struct {
	MetricName string
	Labels     map[string]string
	Value      float64
	Mean       float64
	Stddev     float64
	Score      float64 // Z-Score or severity weight
	Severity   string  // "warning" or "critical"
	Signal     string  // "metrics", "logs", "events"
	Detector   string  // "static", "adaptive", "pattern"
	Timestamp  time.Time
}

// Engine orchestrates static, adaptive, and pattern detectors.
type Engine struct {
	static   *StaticDetector
	adaptive *AdaptiveDetector
	pattern  *PatternDetector
}

func NewEngine(cfg config.Detection, store baseline.Evaluator) *Engine {
	return &Engine{
		static:   NewStaticDetector(cfg.StaticRules),
		adaptive: NewAdaptiveDetector(store),
		pattern:  NewPatternDetector(cfg.EventPatterns),
	}
}

// EvaluateMetricsStatic runs static threshold checks on samples.
func (e *Engine) EvaluateMetricsStatic(rule config.StaticRule, samples []ingestion.Sample) []Anomaly {
	return e.static.Evaluate(rule, samples)
}

// EvaluateMetricsAdaptive runs adaptive (Z-Score) detection on samples.
func (e *Engine) EvaluateMetricsAdaptive(ctx context.Context, metricName string, samples []ingestion.Sample) []Anomaly {
	return e.adaptive.Evaluate(ctx, metricName, samples)
}

// EvaluateEvent checks if a K8s event matches anomalous patterns.
func (e *Engine) EvaluateEvent(event ingestion.EventAnomaly) *Anomaly {
	return e.pattern.EvaluateEvent(event)
}

// EvaluateLogRate runs adaptive detection on log rate samples.
func (e *Engine) EvaluateLogRate(ctx context.Context, metricName string, samples []ingestion.Sample) []Anomaly {
	results := e.adaptive.Evaluate(ctx, metricName, samples)
	for i := range results {
		results[i].Signal = "logs"
	}
	metrics.WorkerDetections.WithLabelValues("adaptive").Add(float64(len(results)))
	return results
}
