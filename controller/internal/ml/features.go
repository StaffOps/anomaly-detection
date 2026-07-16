package ml

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

// Detection is the structured result of a multivariate ML call attached to an alert.
type Detection struct {
	IsAnomaly    bool
	Score        float64
	Contributors []string
	FeatureCount int
}

// minFeatures is the minimum number of distinct features required for a
// meaningful Isolation Forest call. Below this we skip the call entirely.
const minFeatures = 2

// BuildFeatureVector combines the representative anomaly's signal with the
// enrichment bundle's numeric results into a stable, named feature vector.
//
// The returned map uses well-known feature names (cpu_ratio, memory_ratio,
// error_rate_1m, etc. from the enrichment bundles) so the Python-side
// IsolationForest sees a consistent schema across calls. This is what
// Isolation Forest expects — multiple FEATURES of the same observation,
// NOT multiple observations of the same feature.
//
// Returns nil when fewer than minFeatures usable features are available.
func BuildFeatureVector(rep detection.Anomaly, bundle enrichment.Bundle) map[string]float64 {
	f := map[string]float64{
		"anomaly_score": rep.Score,
		"anomaly_value": rep.Value,
	}
	for _, r := range bundle.Results {
		if r.Error != "" {
			continue
		}
		f[r.Name] = r.Value
	}
	if len(f) < minFeatures {
		return nil
	}
	return f
}

// DetectFromFeatures runs DetectMultivariate with a pre-built feature vector
// and returns a structured Detection. Unlike DetectMultivariate, this returns
// the result even when not anomalous (so callers can log/observe scores).
func (c *Client) DetectFromFeatures(ctx context.Context, features map[string]float64) (*Detection, error) {
	if !c.enabled || len(features) < minFeatures {
		return nil, nil
	}

	start := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	pbSamples := make([]*pb.MetricSample, 0, len(features))
	for name, val := range features {
		pbSamples = append(pbSamples, &pb.MetricSample{Name: name, Value: val})
	}

	resp, err := c.client.DetectMultivariate(callCtx, &pb.MultivariateRequest{Samples: pbSamples})
	metrics.MLCallDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attribute.String("method", "multivariate")))
	if err != nil {
		metrics.MLCalls.Add(ctx, 1, metric.WithAttributes(attribute.String("method", "multivariate"), attribute.String("status", "error")))
		return nil, err
	}
	metrics.MLCalls.Add(ctx, 1, metric.WithAttributes(attribute.String("method", "multivariate"), attribute.String("status", "ok")))

	return &Detection{
		IsAnomaly:    resp.IsAnomaly,
		Score:        resp.AnomalyScore,
		Contributors: resp.ContributingMetrics,
		FeatureCount: len(features),
	}, nil
}
