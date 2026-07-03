package replay

import (
	"math"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// SamplesAt extracts the last point with T <= ts from each TimeSeries and
// returns it as an ingestion.Sample. Series with no point at or before ts
// are skipped.
//
// Non-finite values (NaN / ±Inf) are skipped: they are not real measurements
// but artifacts of PromQL division-by-zero (e.g. a usage/limit ratio for a
// workload with no resource limit set). Passing them downstream would produce
// meaningless z-scores and break JSON serialization of the report.
func SamplesAt(ts time.Time, series []ingestion.TimeSeries) []ingestion.Sample {
	samples := make([]ingestion.Sample, 0, len(series))
	for _, s := range series {
		if p, ok := lastPointBefore(ts, s.Points); ok {
			if math.IsNaN(p.V) || math.IsInf(p.V, 0) {
				continue
			}
			samples = append(samples, ingestion.Sample{
				Labels: s.Labels,
				Value:  p.V,
			})
		}
	}
	return samples
}

// lastPointBefore finds the last point with T <= ts using a linear scan
// (points are expected to be sorted chronologically from range queries).
func lastPointBefore(ts time.Time, points []ingestion.Point) (ingestion.Point, bool) {
	var found bool
	var result ingestion.Point
	for _, p := range points {
		if !p.T.After(ts) {
			result = p
			found = true
		} else {
			break // points are sorted, no need to continue
		}
	}
	return result, found
}
