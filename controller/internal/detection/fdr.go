package detection

import (
	"math"
	"sort"
)

// FDR implements Benjamini-Hochberg False Discovery Rate control.
// It filters a batch of adaptive anomalies per detection cycle,
// rejecting those whose p-values don't survive the BH procedure
// at the configured target rate.
type FDR struct {
	Target float64 // desired FDR (e.g., 0.05 = 5% expected false discoveries)
}

// NewFDR creates an FDR filter. Target must be in (0, 1].
func NewFDR(target float64) *FDR {
	if target <= 0 || target > 1 {
		target = 0.05
	}
	return &FDR{Target: target}
}

// Apply runs BH correction on adaptive anomalies. Non-adaptive anomalies
// pass through unchanged. Returns (accepted, rejected count).
func (f *FDR) Apply(anomalies []*Anomaly) ([]*Anomaly, int) {
	if f.Target >= 1.0 {
		return anomalies, 0
	}

	// Separate adaptive from non-adaptive (static/pattern pass through).
	var adaptive []*Anomaly
	var passthrough []*Anomaly
	for _, a := range anomalies {
		if a.Detector == "adaptive" {
			adaptive = append(adaptive, a)
		} else {
			passthrough = append(passthrough, a)
		}
	}

	if len(adaptive) == 0 {
		return anomalies, 0
	}

	// Compute p-values from z-scores and run BH.
	m := len(adaptive)
	type ranked struct {
		anomaly *Anomaly
		pvalue  float64
	}
	items := make([]ranked, m)
	for i, a := range adaptive {
		items[i] = ranked{anomaly: a, pvalue: zscoreToPValue(a.Score)}
	}

	// Sort by p-value ascending.
	sort.Slice(items, func(i, j int) bool {
		return items[i].pvalue < items[j].pvalue
	})

	// BH procedure: find largest k where p(k) <= (k/m) * target.
	threshold := 0
	for k := 1; k <= m; k++ {
		bhCritical := (float64(k) / float64(m)) * f.Target
		if items[k-1].pvalue <= bhCritical {
			threshold = k
		}
	}

	// Accept the first `threshold` items (those that pass BH).
	accepted := passthrough
	for i := 0; i < threshold; i++ {
		accepted = append(accepted, items[i].anomaly)
	}

	rejected := m - threshold
	return accepted, rejected
}

// zscoreToPValue converts an absolute z-score to a two-tailed p-value
// using the complementary error function (no external dependency).
func zscoreToPValue(z float64) float64 {
	absZ := math.Abs(z)
	// Two-tailed p-value: P(|Z| > z) = erfc(z / sqrt(2))
	p := math.Erfc(absZ / math.Sqrt2)
	if p < 1e-300 {
		p = 1e-300 // avoid exact zero for very large z-scores
	}
	return p
}
