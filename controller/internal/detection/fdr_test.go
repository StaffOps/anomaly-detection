package detection

import (
	"math"
	"testing"
)

// ─── zscoreToPValue tests ────────────────────────────────────────────────────

func TestZscoreToPValue_KnownValues(t *testing.T) {
	tests := []struct {
		name      string
		z         float64
		wantApprx float64
		tolerance float64
	}{
		{"z=0 → p=1.0", 0, 1.0, 1e-10},
		{"z=1.96 → p≈0.05", 1.96, 0.05, 0.001},
		{"z=2.576 → p≈0.01", 2.576, 0.01, 0.001},
		{"z=3.0 → p≈0.0027", 3.0, 0.0027, 0.0005},
		{"z=1.0 → p≈0.3173", 1.0, 0.3173, 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := zscoreToPValue(tt.z)
			if math.Abs(got-tt.wantApprx) > tt.tolerance {
				t.Errorf("zscoreToPValue(%v) = %v, want ≈%v (±%v)", tt.z, got, tt.wantApprx, tt.tolerance)
			}
		})
	}
}

func TestZscoreToPValue_ExtremeValues_NotZero(t *testing.T) {
	tests := []struct {
		name string
		z    float64
	}{
		{"z=10", 10},
		{"z=50", 50},
		{"z=100", 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := zscoreToPValue(tt.z)
			if got == 0 {
				t.Errorf("zscoreToPValue(%v) = 0, must never be exactly zero", tt.z)
			}
			if got < 1e-300 {
				t.Errorf("zscoreToPValue(%v) = %v, should be >= 1e-300 (floor)", tt.z, got)
			}
		})
	}
}

func TestZscoreToPValue_NegativeEqualsPositive(t *testing.T) {
	zScores := []float64{1.0, 2.5, 3.0, 5.0, 10.0}
	for _, z := range zScores {
		pos := zscoreToPValue(z)
		neg := zscoreToPValue(-z)
		if pos != neg {
			t.Errorf("zscoreToPValue(%v)=%v != zscoreToPValue(%v)=%v; two-tailed must be symmetric", z, pos, -z, neg)
		}
	}
}

// ─── NewFDR tests ────────────────────────────────────────────────────────────

func TestNewFDR_ValidTarget(t *testing.T) {
	tests := []struct {
		name   string
		target float64
		want   float64
	}{
		{"0.05", 0.05, 0.05},
		{"0.1", 0.1, 0.1},
		{"0.01", 0.01, 0.01},
		{"1.0 (boundary)", 1.0, 1.0},
		{"0.001", 0.001, 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fdr := NewFDR(tt.target)
			if fdr.Target != tt.want {
				t.Errorf("NewFDR(%v).Target = %v, want %v", tt.target, fdr.Target, tt.want)
			}
		})
	}
}

func TestNewFDR_InvalidTarget_DefaultsTo005(t *testing.T) {
	tests := []struct {
		name   string
		target float64
	}{
		{"zero", 0},
		{"negative", -1.0},
		{"greater than 1", 1.5},
		{"very negative", -100},
		{"slightly above 1", 1.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fdr := NewFDR(tt.target)
			if fdr.Target != 0.05 {
				t.Errorf("NewFDR(%v).Target = %v, want 0.05 (default)", tt.target, fdr.Target)
			}
		})
	}
}

// ─── FDR.Apply tests ─────────────────────────────────────────────────────────

func makeAnomaly(detector string, score float64) *Anomaly {
	return &Anomaly{
		MetricName: "test_metric",
		Detector:   detector,
		Score:      score,
		Severity:   "warning",
		Signal:     "metrics",
	}
}

func TestFDR_Apply_EmptyInput(t *testing.T) {
	fdr := NewFDR(0.05)
	accepted, rejected := fdr.Apply(nil)
	if len(accepted) != 0 {
		t.Errorf("empty input: got %d accepted, want 0", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("empty input: got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_OnlyStatic_AllPassThrough(t *testing.T) {
	fdr := NewFDR(0.05)
	input := []*Anomaly{
		makeAnomaly("static", 5.0),
		makeAnomaly("static", 3.0),
		makeAnomaly("pattern", 4.0),
	}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 3 {
		t.Errorf("all static/pattern: got %d accepted, want 3", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("all static/pattern: got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_SingleAdaptive_HighZ_Accepted(t *testing.T) {
	fdr := NewFDR(0.05)
	// z=5.0 → p≈5.7e-7, BH critical = (1/1)*0.05 = 0.05. p < critical → accepted.
	input := []*Anomaly{makeAnomaly("adaptive", 5.0)}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 1 {
		t.Errorf("single high-z adaptive: got %d accepted, want 1", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("single high-z adaptive: got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_SingleAdaptive_LowZ_Rejected(t *testing.T) {
	fdr := NewFDR(0.05)
	// z=1.0 → p≈0.317, BH critical = (1/1)*0.05 = 0.05. p > critical → rejected.
	input := []*Anomaly{makeAnomaly("adaptive", 1.0)}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 0 {
		t.Errorf("single low-z adaptive: got %d accepted, want 0", len(accepted))
	}
	if rejected != 1 {
		t.Errorf("single low-z adaptive: got %d rejected, want 1", rejected)
	}
}

func TestFDR_Apply_MultipleAdaptive_FiltersWeakOnes(t *testing.T) {
	fdr := NewFDR(0.05)
	// Mix of strong and weak z-scores.
	// z=5 → p≈5.7e-7 (very significant)
	// z=4 → p≈6.3e-5 (significant)
	// z=2 → p≈0.046  (marginally significant)
	// z=1 → p≈0.317  (not significant)
	// m=4. BH criticals: k/4 * 0.05 = [0.0125, 0.025, 0.0375, 0.05]
	// Sorted by p: z5(5.7e-7), z4(6.3e-5), z2(0.046), z1(0.317)
	// k=1: 5.7e-7 ≤ 0.0125 ✓
	// k=2: 6.3e-5 ≤ 0.025  ✓
	// k=3: 0.046  ≤ 0.0375 ✗
	// k=4: 0.317  ≤ 0.05   ✗
	// threshold=2 → accept top 2, reject 2
	input := []*Anomaly{
		makeAnomaly("adaptive", 5.0),
		makeAnomaly("adaptive", 4.0),
		makeAnomaly("adaptive", 2.0),
		makeAnomaly("adaptive", 1.0),
	}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 2 {
		t.Errorf("mixed z-scores: got %d accepted, want 2", len(accepted))
	}
	if rejected != 2 {
		t.Errorf("mixed z-scores: got %d rejected, want 2", rejected)
	}
}

func TestFDR_Apply_AllVeryHighZ_AllAccepted(t *testing.T) {
	fdr := NewFDR(0.05)
	input := make([]*Anomaly, 10)
	for i := range input {
		input[i] = makeAnomaly("adaptive", 6.0+float64(i))
	}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 10 {
		t.Errorf("all high-z: got %d accepted, want 10", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("all high-z: got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_AllMarginalZ_MostRejected(t *testing.T) {
	fdr := NewFDR(0.05)
	// z=3.0 → p≈0.0027. With m=20, BH critical for k=20 = (20/20)*0.05 = 0.05.
	// Actually p=0.0027 < 0.05 always... Let's use z≈2.0 where p≈0.046.
	// m=20, all p≈0.046. BH critical for k: (k/20)*0.05.
	// k=1: 0.0025 → 0.046 > 0.0025 ✗ ... all fail.
	// Actually for ties, need largest k where p ≤ (k/m)*α.
	// 0.046 ≤ (k/20)*0.05 → 0.046 ≤ k*0.0025 → k ≥ 18.4 → k=19: 0.046 ≤ 0.0475? No.
	// k=20: 0.046 ≤ 0.05 ✓. So threshold=20, all accepted!
	// Use z=2.3 → p≈0.0214. m=20. (k/20)*0.05. k=20: 0.0214 ≤ 0.05 ✓. Still all pass.
	// To get rejections with uniform p, need p > α. Use z=1.5 → p≈0.134.
	// m=20, p=0.134. (k/20)*0.05. Max critical at k=20 = 0.05. 0.134 > 0.05 → all rejected.
	input := make([]*Anomaly, 20)
	for i := range input {
		input[i] = makeAnomaly("adaptive", 1.5) // p≈0.134
	}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 0 {
		t.Errorf("all marginal z=1.5: got %d accepted, want 0", len(accepted))
	}
	if rejected != 20 {
		t.Errorf("all marginal z=1.5: got %d rejected, want 20", rejected)
	}
}

func TestFDR_Apply_MixedAdaptiveAndStatic(t *testing.T) {
	fdr := NewFDR(0.05)
	input := []*Anomaly{
		makeAnomaly("static", 2.0),
		makeAnomaly("pattern", 3.0),
		makeAnomaly("adaptive", 5.0),  // significant → accepted
		makeAnomaly("adaptive", 0.5),  // p≈0.617 → rejected
	}
	accepted, rejected := fdr.Apply(input)
	// Static + pattern (2) + adaptive survivors.
	// m=2 adaptive. z=5→p≈5.7e-7, z=0.5→p≈0.617.
	// Sorted: [5.7e-7, 0.617]. BH criticals: [0.025, 0.05].
	// k=1: 5.7e-7 ≤ 0.025 ✓. k=2: 0.617 ≤ 0.05 ✗. threshold=1.
	// Accepted = 2 passthrough + 1 adaptive = 3.
	if len(accepted) != 3 {
		t.Errorf("mixed: got %d accepted, want 3", len(accepted))
	}
	if rejected != 1 {
		t.Errorf("mixed: got %d rejected, want 1", rejected)
	}
}

func TestFDR_Apply_Target1_AllAccepted(t *testing.T) {
	fdr := NewFDR(1.0)
	input := []*Anomaly{
		makeAnomaly("adaptive", 0.1), // very low z → normally rejected
		makeAnomaly("adaptive", 0.5),
		makeAnomaly("adaptive", 1.0),
	}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 3 {
		t.Errorf("target=1.0: got %d accepted, want 3", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("target=1.0: got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_Target0_UsesDefault005(t *testing.T) {
	fdr := NewFDR(0)
	// Constructor clamps to 0.05. Verify behavior matches target=0.05.
	// z=5 → accepted at target=0.05.
	input := []*Anomaly{makeAnomaly("adaptive", 5.0)}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 1 {
		t.Errorf("target=0 (clamped to 0.05): got %d accepted, want 1", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("target=0 (clamped to 0.05): got %d rejected, want 0", rejected)
	}
}

func TestFDR_Apply_ZeroScore_Rejected(t *testing.T) {
	fdr := NewFDR(0.05)
	// z=0 → p=1.0. BH critical = (1/1)*0.05 = 0.05. 1.0 > 0.05 → rejected.
	input := []*Anomaly{makeAnomaly("adaptive", 0)}
	accepted, rejected := fdr.Apply(input)
	if len(accepted) != 0 {
		t.Errorf("z=0: got %d accepted, want 0", len(accepted))
	}
	if rejected != 1 {
		t.Errorf("z=0: got %d rejected, want 1", rejected)
	}
}

func TestFDR_Apply_NegativeScores_SameAsPositive(t *testing.T) {
	fdr := NewFDR(0.05)
	posInput := []*Anomaly{makeAnomaly("adaptive", 5.0)}
	negInput := []*Anomaly{makeAnomaly("adaptive", -5.0)}

	posAccepted, posRejected := fdr.Apply(posInput)
	negAccepted, negRejected := fdr.Apply(negInput)

	if len(posAccepted) != len(negAccepted) {
		t.Errorf("positive z=%d accepted vs negative z=%d accepted; should be equal", len(posAccepted), len(negAccepted))
	}
	if posRejected != negRejected {
		t.Errorf("positive rejected=%d vs negative rejected=%d; should be equal", posRejected, negRejected)
	}
}

func TestFDR_Apply_LargeBatch_CorrectBH(t *testing.T) {
	fdr := NewFDR(0.05)
	n := 400
	input := make([]*Anomaly, n)
	// Half with z=5 (very significant), half with z=1.5 (not significant).
	for i := 0; i < n/2; i++ {
		input[i] = makeAnomaly("adaptive", 5.0)
	}
	for i := n / 2; i < n; i++ {
		input[i] = makeAnomaly("adaptive", 1.5) // p≈0.134
	}

	accepted, rejected := fdr.Apply(input)

	// m=400. The 200 with z=5 have p≈5.7e-7.
	// The 200 with z=1.5 have p≈0.134.
	// Sorted: first 200 have p≈5.7e-7, next 200 have p≈0.134.
	// For k=200: BH critical = (200/400)*0.05 = 0.025. p=5.7e-7 ≤ 0.025 ✓.
	// For k=201: BH critical = (201/400)*0.05 ≈ 0.0251. p=0.134 > 0.0251 ✗.
	// threshold=200. rejected=200.
	if len(accepted) != 200 {
		t.Errorf("large batch: got %d accepted, want 200", len(accepted))
	}
	if rejected != 200 {
		t.Errorf("large batch: got %d rejected, want 200", rejected)
	}
}

func TestFDR_Apply_EmptySlice(t *testing.T) {
	fdr := NewFDR(0.05)
	accepted, rejected := fdr.Apply([]*Anomaly{})
	if len(accepted) != 0 {
		t.Errorf("empty slice: got %d accepted, want 0", len(accepted))
	}
	if rejected != 0 {
		t.Errorf("empty slice: got %d rejected, want 0", rejected)
	}
}
