package detection

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/staffops/staffops-anomaly-detection/internal/baseline"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// AdaptiveDetector uses EWMA + Z-Score from a baseline evaluator. The
// evaluator can be Redis-backed (production) or in-memory (replay mode).
type AdaptiveDetector struct {
	store baseline.Evaluator
}

func NewAdaptiveDetector(store baseline.Evaluator) *AdaptiveDetector {
	return &AdaptiveDetector{store: store}
}

// Evaluate runs adaptive detection on each sample, updating baselines.
//
// The second return value is the number of evaluations that could have fired
// (past warm-up, no error) — the statistical family size consumed by the
// Benjamini-Hochberg FDR filter. Counting only fired anomalies would hand BH
// a censored family and neuter the correction.
func (d *AdaptiveDetector) Evaluate(ctx context.Context, metricName string, samples []ingestion.Sample) ([]Anomaly, int) {
	var anomalies []Anomaly
	tested := 0
	for _, s := range samples {
		result, err := d.store.Evaluate(ctx, metricName, s.Labels, s.Value)
		if err != nil {
			slog.Warn("baseline evaluate failed", "metric", metricName, "error", err)
			continue
		}
		if result.IsWarmingUp {
			continue
		}
		tested++
		if !result.IsAnomaly {
			continue
		}

		severity := "warning"
		if result.ZScore > 5.0 {
			severity = "critical"
		}

		anomalies = append(anomalies, Anomaly{
			MetricName: metricName,
			Labels:     s.Labels,
			Value:      result.Value,
			Mean:       result.Mean,
			Stddev:     result.Stddev,
			Score:      result.ZScore,
			Severity:   severity,
			Signal:     "metrics",
			Detector:   "adaptive",
			Timestamp:  time.Now(),
		})
	}
	if len(anomalies) > 0 {
		metrics.WorkerDetections.Add(ctx, int64(len(anomalies)),
			metric.WithAttributes(attribute.String("detector", "adaptive")))
	}
	return anomalies, tested
}
