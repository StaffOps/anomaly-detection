package detection

// Direction-of-badness constants. Declared per adaptive rule in config.
const (
	DirectionUpBad   = "up_bad"
	DirectionDownBad = "down_bad"
	DirectionBothBad = "both_bad"
)

// DirectionAllows reports whether an anomaly's deviation direction is one the
// rule considers "bad", and therefore worth firing.
//
// The adaptive detector computes an absolute z-score, so it fires symmetrically
// on any deviation. Many metrics, though, are only anomalous one way: latency,
// error rate, and queue depth matter when they RISE; ready replicas and
// throughput when they FALL. Declaring a direction drops the harmless-direction
// false positives (e.g. an alert because latency dropped).
//
// The deviation direction is derived from Value vs Mean (the anomaly already
// carries both). An empty or "both_bad" direction always allows — the
// backward-compatible default. Non-adaptive detectors carry their own direction
// in the static operator, so this is only consulted for adaptive anomalies.
func DirectionAllows(a Anomaly, direction string) bool {
	switch direction {
	case DirectionUpBad:
		return a.Value >= a.Mean
	case DirectionDownBad:
		return a.Value <= a.Mean
	default: // "", "both_bad", or any unknown value → permissive
		return true
	}
}

// FilterByDirection drops adaptive anomalies whose deviation runs in the
// harmless direction for their rule. directions maps a rule name (Anomaly.MetricName)
// to its declared direction; rules absent from the map (and all non-adaptive
// anomalies) pass through unchanged. Returns the kept anomalies and the number dropped.
func FilterByDirection(anomalies []*Anomaly, directions map[string]string) ([]*Anomaly, int) {
	if len(directions) == 0 {
		return anomalies, 0
	}
	kept := anomalies[:0]
	dropped := 0
	for _, a := range anomalies {
		if a.Detector == "adaptive" {
			if dir, ok := directions[a.MetricName]; ok && !DirectionAllows(*a, dir) {
				dropped++
				continue
			}
		}
		kept = append(kept, a)
	}
	return kept, dropped
}
