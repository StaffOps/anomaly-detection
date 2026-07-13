package inject

import (
	"math"
	"math/rand"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// faultFunc transforms a TimeSeries' points within [start, end] according to
// the fault type. magnitude is in multiples of the series' own stddev.
// The rng provides determinism given a seed.
type faultFunc func(ts *ingestion.TimeSeries, start, end time.Time, magnitude float64, rng *rand.Rand)

// seriesStddev computes the standard deviation of all point values in the series.
// Returns 1.0 if the series has fewer than 2 points (avoids division by zero).
func seriesStddev(ts *ingestion.TimeSeries) float64 {
	n := len(ts.Points)
	if n < 2 {
		return 1.0
	}
	var sum, sumSq float64
	for _, p := range ts.Points {
		sum += p.V
		sumSq += p.V * p.V
	}
	mean := sum / float64(n)
	variance := (sumSq / float64(n)) - (mean * mean)
	if variance < 0 {
		variance = 0
	}
	sd := math.Sqrt(variance)
	if sd == 0 {
		return 1.0
	}
	return sd
}

// faultSpike applies a transient peak: each point within [start, end] is
// increased by magnitude * stddev. The window should be short (1-2 ticks)
// for a realistic spike. A small random jitter (±5%) makes it non-flat.
func faultSpike(ts *ingestion.TimeSeries, start, end time.Time, magnitude float64, rng *rand.Rand) {
	sd := seriesStddev(ts)
	for i := range ts.Points {
		p := &ts.Points[i]
		if !p.T.Before(start) && !p.T.After(end) {
			jitter := 1.0 + (rng.Float64()-0.5)*0.1 // ±5%
			p.V += magnitude * sd * jitter
		}
	}
}

// faultRamp applies a linear ramp from 0 to magnitude*stddev across [start, end].
// This is the critical EWMA-blindness case: the value rises slowly enough that
// an exponentially-weighted average might "chase" it without ever triggering.
func faultRamp(ts *ingestion.TimeSeries, start, end time.Time, magnitude float64, rng *rand.Rand) {
	sd := seriesStddev(ts)
	duration := end.Sub(start)
	if duration <= 0 {
		return
	}
	for i := range ts.Points {
		p := &ts.Points[i]
		if !p.T.Before(start) && !p.T.After(end) {
			progress := float64(p.T.Sub(start)) / float64(duration) // 0.0 → 1.0
			p.V += progress * magnitude * sd
		}
	}
}

// faultStep applies a sustained level jump: all points in [start, end] are
// shifted up by magnitude * stddev. Simulates a deploy-time regime change.
func faultStep(ts *ingestion.TimeSeries, start, end time.Time, magnitude float64, rng *rand.Rand) {
	sd := seriesStddev(ts)
	for i := range ts.Points {
		p := &ts.Points[i]
		if !p.T.Before(start) && !p.T.After(end) {
			p.V += magnitude * sd
		}
	}
}

// faultSilence removes all points within [start, end], simulating a
// disappearing series (dead service, scrape target gone). The current
// detector is expected to NOT detect this (recall ~0) since it evaluates
// present values only. This gives baseline for P2.10 (dead-man's-switch).
func faultSilence(ts *ingestion.TimeSeries, start, end time.Time, magnitude float64, rng *rand.Rand) {
	filtered := make([]ingestion.Point, 0, len(ts.Points))
	for _, p := range ts.Points {
		if p.T.Before(start) || p.T.After(end) {
			filtered = append(filtered, p)
		}
	}
	ts.Points = filtered
}

// faultFuncForType returns the appropriate fault function for the given type.
func faultFuncForType(ft FaultType) faultFunc {
	switch ft {
	case FaultSpike:
		return faultSpike
	case FaultRamp:
		return faultRamp
	case FaultStep:
		return faultStep
	case FaultSilence:
		return faultSilence
	default:
		return nil
	}
}
