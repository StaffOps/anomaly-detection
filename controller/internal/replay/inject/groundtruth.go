package inject

import "time"

// GroundTruth records exactly what was injected: which series, what fault type,
// the time window, and the magnitude. This is the "answer key" for scoring.
type GroundTruth struct {
	Target    string    // Normalized fingerprint: metric{sorted labels}
	Type      FaultType // spike|ramp|step|silence
	Start     time.Time
	End       time.Time
	Magnitude float64
}

// GroundTruthAccumulator collects GroundTruth entries produced during injection.
type GroundTruthAccumulator struct {
	truths []GroundTruth
}

// NewGroundTruthAccumulator creates an empty accumulator.
func NewGroundTruthAccumulator() *GroundTruthAccumulator {
	return &GroundTruthAccumulator{}
}

// Add records a single ground truth entry.
func (a *GroundTruthAccumulator) Add(gt GroundTruth) {
	a.truths = append(a.truths, gt)
}

// Truths returns all accumulated ground truth entries.
func (a *GroundTruthAccumulator) Truths() []GroundTruth {
	return a.truths
}
