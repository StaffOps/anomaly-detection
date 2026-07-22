package detection

import "math"

// FilterByFloor drops adaptive anomalies whose current reading does not cross an
// absolute floor declared for their rule. floors maps a rule name
// (Anomaly.MetricName) to its min_value; rules absent from the map (and all
// non-adaptive anomalies) pass through unchanged. Returns the kept anomalies and
// the number dropped.
//
// This fixes the near-zero-baseline false positive. An adaptive z-score is scale
// free: a gauge that idles at ~0.1 with a tiny stddev produces a large z-score
// for any reading of a few units — statistically anomalous, operationally noise
// (a quiet service handling a handful of requests is not an incident). The floor
// gates firing on operational magnitude: fire only when the deviation is BOTH
// significant (z > threshold, already enforced upstream) AND the reading reaches
// min_value.
//
// The floor is compared on the absolute reading (|Value|), so it is meaningful
// regardless of direction-of-badness. A floor <= 0 is treated as "no floor".
func FilterByFloor(anomalies []*Anomaly, floors map[string]float64) ([]*Anomaly, int) {
	if len(floors) == 0 {
		return anomalies, 0
	}
	kept := anomalies[:0]
	dropped := 0
	for _, a := range anomalies {
		if a.Detector == "adaptive" {
			if floor, ok := floors[a.MetricName]; ok && floor > 0 && math.Abs(a.Value) < floor {
				dropped++
				continue
			}
		}
		kept = append(kept, a)
	}
	return kept, dropped
}
