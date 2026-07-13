package inject

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// makeConstantSeries creates a TimeSeries with n points at constant value v,
// starting at t0 with the given step interval.
func makeConstantSeries(t0 time.Time, step time.Duration, n int, v float64) ingestion.TimeSeries {
	pts := make([]ingestion.Point, n)
	for i := range pts {
		pts[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: v}
	}
	return ingestion.TimeSeries{Labels: map[string]string{"job": "test"}, Points: pts}
}

// makeSlopeSeries creates a series with linearly increasing values (for stddev testing).
func makeSlopeSeries(t0 time.Time, step time.Duration, n int, start, increment float64) ingestion.TimeSeries {
	pts := make([]ingestion.Point, n)
	for i := range pts {
		pts[i] = ingestion.Point{T: t0.Add(time.Duration(i) * step), V: start + float64(i)*increment}
	}
	return ingestion.TimeSeries{Labels: map[string]string{"job": "test"}, Points: pts}
}

// --- Task 16: faultSpike ---

func TestFaultSpike_TransientPeak(t *testing.T) {
	// Contract: spike adds a transient peak in [start, end]. Points outside the
	// window MUST NOT be modified. Points inside MUST be increased by ~magnitude*stddev.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	n := 20
	baseValue := 100.0
	ts := makeConstantSeries(t0, step, n, baseValue)

	// Injection window: points 5-7 (3 points out of 20)
	start := t0.Add(5 * step)
	end := t0.Add(7 * step)
	magnitude := 5.0

	rng := rand.New(rand.NewSource(42))
	faultSpike(&ts, start, end, magnitude, rng)

	// stddev of constant series = 1.0 (per seriesStddev: returns 1.0 for zero-variance)
	// Expected increase: ~magnitude * 1.0 * jitter(±5%)
	for i, p := range ts.Points {
		if i >= 5 && i <= 7 {
			// Points in window must be increased
			increase := p.V - baseValue
			if increase <= 0 {
				t.Errorf("point[%d] at %v: expected increase, got %f (value=%f, base=%f)",
					i, p.T, increase, p.V, baseValue)
			}
			// The increase should be approximately magnitude * 1.0 (±5% jitter)
			expectedIncrease := magnitude * 1.0 // stddev=1.0 for constant series
			if math.Abs(increase-expectedIncrease) > expectedIncrease*0.1 {
				t.Errorf("point[%d]: increase %f not within ±10%% of expected %f",
					i, increase, expectedIncrease)
			}
		} else {
			// Points outside window must be unchanged
			if p.V != baseValue {
				t.Errorf("point[%d] outside window: expected %f, got %f", i, baseValue, p.V)
			}
		}
	}
}

func TestFaultSpike_Determinism(t *testing.T) {
	// Contract (US-7): same seed → identical output.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	start := t0.Add(2 * step)
	end := t0.Add(4 * step)

	run := func(seed int64) []float64 {
		ts := makeSlopeSeries(t0, step, 10, 50.0, 2.0)
		rng := rand.New(rand.NewSource(seed))
		faultSpike(&ts, start, end, 3.0, rng)
		vals := make([]float64, len(ts.Points))
		for i, p := range ts.Points {
			vals[i] = p.V
		}
		return vals
	}

	run1 := run(99)
	run2 := run(99)
	run3 := run(77) // different seed

	for i := range run1 {
		if run1[i] != run2[i] {
			t.Fatalf("determinism broken: point[%d] run1=%f run2=%f (same seed=99)", i, run1[i], run2[i])
		}
	}

	// Different seed should produce different jitter in the spike window
	sameAsDiffSeed := true
	for i := range run1 {
		if run1[i] != run3[i] {
			sameAsDiffSeed = false
			break
		}
	}
	if sameAsDiffSeed {
		t.Error("different seeds produced identical output — expected divergence in jitter")
	}
}

// --- Task 16: faultRamp ---

func TestFaultRamp_LinearGrowth(t *testing.T) {
	// Contract: ramp grows linearly from 0 to magnitude*stddev across [start, end].
	// At start: increase=0. At end: increase=magnitude*stddev.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute
	n := 30
	baseValue := 50.0
	ts := makeConstantSeries(t0, step, n, baseValue)

	start := t0.Add(10 * step) // point 10
	end := t0.Add(20 * step)   // point 20 — 10 minute ramp
	magnitude := 4.0

	rng := rand.New(rand.NewSource(123))
	faultRamp(&ts, start, end, magnitude, rng)

	// stddev = 1.0 for constant series
	sd := 1.0

	for i, p := range ts.Points {
		if i < 10 || i > 20 {
			// Outside window: unchanged
			if p.V != baseValue {
				t.Errorf("point[%d] outside ramp window: expected %f, got %f", i, baseValue, p.V)
			}
		} else {
			// Inside window: linear increase
			progress := float64(i-10) / float64(20-10) // 0.0 at start, 1.0 at end
			expectedIncrease := progress * magnitude * sd
			actualIncrease := p.V - baseValue
			tolerance := 0.001
			if math.Abs(actualIncrease-expectedIncrease) > tolerance {
				t.Errorf("point[%d]: ramp increase=%f, expected=%f (progress=%.2f)",
					i, actualIncrease, expectedIncrease, progress)
			}
		}
	}

	// Verify monotonic growth within the ramp window
	for i := 11; i <= 20; i++ {
		if ts.Points[i].V < ts.Points[i-1].V {
			t.Errorf("ramp not monotonically increasing: point[%d]=%f < point[%d]=%f",
				i, ts.Points[i].V, i-1, ts.Points[i-1].V)
		}
	}
}

func TestFaultRamp_ZeroDuration(t *testing.T) {
	// Contract: if end <= start, ramp is a no-op (returns without modifying).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	ts := makeConstantSeries(t0, time.Minute, 5, 100.0)
	original := make([]float64, len(ts.Points))
	for i, p := range ts.Points {
		original[i] = p.V
	}

	rng := rand.New(rand.NewSource(1))
	faultRamp(&ts, t0.Add(2*time.Minute), t0.Add(2*time.Minute), 5.0, rng) // end == start

	for i, p := range ts.Points {
		if p.V != original[i] {
			t.Errorf("zero-duration ramp modified point[%d]: expected %f, got %f",
				i, original[i], p.V)
		}
	}
}

func TestFaultRamp_Determinism(t *testing.T) {
	// Ramp has no RNG usage (pure linear), but verify determinism anyway.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	start := t0.Add(2 * time.Minute)
	end := t0.Add(6 * time.Minute)

	run := func() []float64 {
		ts := makeConstantSeries(t0, time.Minute, 10, 75.0)
		rng := rand.New(rand.NewSource(42))
		faultRamp(&ts, start, end, 3.0, rng)
		vals := make([]float64, len(ts.Points))
		for i, p := range ts.Points {
			vals[i] = p.V
		}
		return vals
	}

	r1 := run()
	r2 := run()
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("ramp determinism broken at point[%d]: %f != %f", i, r1[i], r2[i])
		}
	}
}

// --- Task 16: faultStep ---

func TestFaultStep_SustainedJump(t *testing.T) {
	// Contract: step shifts ALL points in [start, end] up by magnitude*stddev.
	// Unlike spike (which has jitter), step is a uniform shift.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	n := 20
	baseValue := 200.0
	ts := makeConstantSeries(t0, step, n, baseValue)

	start := t0.Add(5 * step)
	end := t0.Add(14 * step)
	magnitude := 3.0

	rng := rand.New(rand.NewSource(7))
	faultStep(&ts, start, end, magnitude, rng)

	// stddev = 1.0 for constant series
	expectedShift := magnitude * 1.0

	for i, p := range ts.Points {
		if i >= 5 && i <= 14 {
			shift := p.V - baseValue
			if math.Abs(shift-expectedShift) > 0.001 {
				t.Errorf("point[%d] in step window: shift=%f, expected=%f", i, shift, expectedShift)
			}
		} else {
			if p.V != baseValue {
				t.Errorf("point[%d] outside step window: expected %f, got %f", i, baseValue, p.V)
			}
		}
	}

	// Step is uniform — all values in window should be identical
	for i := 6; i <= 14; i++ {
		if ts.Points[i].V != ts.Points[5].V {
			t.Errorf("step not uniform: point[%d]=%f != point[5]=%f", i, ts.Points[i].V, ts.Points[5].V)
		}
	}
}

func TestFaultStep_Determinism(t *testing.T) {
	// Step uses no RNG — should be perfectly deterministic regardless of seed.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	start := t0.Add(time.Minute)
	end := t0.Add(4 * time.Minute)

	run := func(seed int64) []float64 {
		ts := makeConstantSeries(t0, time.Minute, 8, 50.0)
		rng := rand.New(rand.NewSource(seed))
		faultStep(&ts, start, end, 2.0, rng)
		vals := make([]float64, len(ts.Points))
		for i, p := range ts.Points {
			vals[i] = p.V
		}
		return vals
	}

	r1 := run(1)
	r2 := run(999) // different seed — shouldn't matter, step ignores RNG
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("step should be RNG-independent: point[%d] seed1=%f seed999=%f", i, r1[i], r2[i])
		}
	}
}

// --- Task 16: faultSilence ---

func TestFaultSilence_PointsRemoved(t *testing.T) {
	// Contract: silence REMOVES points within [start, end]. Points outside remain.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := 30 * time.Second
	n := 20
	ts := makeConstantSeries(t0, step, n, 42.0)

	// Remove points 5-14 (10 points)
	start := t0.Add(5 * step)
	end := t0.Add(14 * step)

	rng := rand.New(rand.NewSource(0))
	faultSilence(&ts, start, end, 0.0, rng) // magnitude irrelevant for silence

	// Should have 10 points remaining (0-4 and 15-19)
	if len(ts.Points) != 10 {
		t.Fatalf("expected 10 remaining points, got %d", len(ts.Points))
	}

	// Verify the remaining points are the ones outside the window
	for _, p := range ts.Points {
		if !p.T.Before(start) && !p.T.After(end) {
			t.Errorf("point at %v should have been removed (within [%v, %v])", p.T, start, end)
		}
	}

	// All remaining values should be unchanged
	for _, p := range ts.Points {
		if p.V != 42.0 {
			t.Errorf("remaining point value changed: got %f, expected 42.0", p.V)
		}
	}
}

func TestFaultSilence_AllPointsInWindow(t *testing.T) {
	// When the entire series falls within [start, end], all points are removed.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute
	ts := makeConstantSeries(t0, step, 5, 10.0)

	start := t0.Add(-time.Minute) // before first point
	end := t0.Add(10 * time.Minute)

	rng := rand.New(rand.NewSource(0))
	faultSilence(&ts, start, end, 0.0, rng)

	if len(ts.Points) != 0 {
		t.Fatalf("expected 0 points after full-window silence, got %d", len(ts.Points))
	}
}

func TestFaultSilence_Determinism(t *testing.T) {
	// Silence is deterministic (no RNG usage, purely conditional removal).
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute
	start := t0.Add(2 * step)
	end := t0.Add(4 * step)

	run := func() int {
		ts := makeConstantSeries(t0, step, 10, 1.0)
		rng := rand.New(rand.NewSource(55))
		faultSilence(&ts, start, end, 0.0, rng)
		return len(ts.Points)
	}

	r1 := run()
	r2 := run()
	if r1 != r2 {
		t.Fatalf("silence not deterministic: run1=%d points, run2=%d points", r1, r2)
	}
}

// --- Task 16: faultFuncForType ---

func TestFaultFuncForType_AllTypes(t *testing.T) {
	cases := []struct {
		ft   FaultType
		want bool // expect non-nil
	}{
		{FaultSpike, true},
		{FaultRamp, true},
		{FaultStep, true},
		{FaultSilence, true},
		{FaultType("unknown"), false},
		{FaultType(""), false},
	}
	for _, tc := range cases {
		fn := faultFuncForType(tc.ft)
		got := fn != nil
		if got != tc.want {
			t.Errorf("faultFuncForType(%q): got nil=%v, want nil=%v", tc.ft, !got, !tc.want)
		}
	}
}

// --- Task 16: seriesStddev ---

func TestSeriesStddev_ConstantReturnsOne(t *testing.T) {
	// Contract: constant series has stddev=0, but the function returns 1.0 as floor.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	ts := makeConstantSeries(t0, time.Minute, 100, 42.0)
	sd := seriesStddev(&ts)
	if sd != 1.0 {
		t.Errorf("constant series stddev: expected 1.0 (floor), got %f", sd)
	}
}

func TestSeriesStddev_TooFewPoints(t *testing.T) {
	// Contract: fewer than 2 points → returns 1.0.
	ts := ingestion.TimeSeries{Points: []ingestion.Point{{V: 5.0}}}
	if sd := seriesStddev(&ts); sd != 1.0 {
		t.Errorf("single point: expected 1.0, got %f", sd)
	}
	ts2 := ingestion.TimeSeries{Points: nil}
	if sd := seriesStddev(&ts2); sd != 1.0 {
		t.Errorf("zero points: expected 1.0, got %f", sd)
	}
}

func TestSeriesStddev_KnownDistribution(t *testing.T) {
	// Series with known stddev: [1, 3] → mean=2, variance=1, stddev=1
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	ts := ingestion.TimeSeries{
		Points: []ingestion.Point{
			{T: t0, V: 1.0},
			{T: t0.Add(time.Minute), V: 3.0},
		},
	}
	sd := seriesStddev(&ts)
	if math.Abs(sd-1.0) > 0.001 {
		t.Errorf("expected stddev=1.0, got %f", sd)
	}

	// [10, 20, 30] → mean=20, variance=((100+0+100)/3)=66.67, sd=~8.16
	ts2 := ingestion.TimeSeries{
		Points: []ingestion.Point{
			{T: t0, V: 10.0},
			{T: t0.Add(time.Minute), V: 20.0},
			{T: t0.Add(2 * time.Minute), V: 30.0},
		},
	}
	sd2 := seriesStddev(&ts2)
	expected := math.Sqrt((100.0 + 0.0 + 100.0) / 3.0) // population stddev
	if math.Abs(sd2-expected) > 0.01 {
		t.Errorf("expected stddev=%.4f, got %.4f", expected, sd2)
	}
}

// --- Spike with real stddev (non-constant series) ---

func TestFaultSpike_WithRealStddev(t *testing.T) {
	// Verify spike magnitude scales with the series' own standard deviation.
	t0 := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	step := time.Minute
	// Create series with known distribution: values 0,10,20,...,90 → mean=45, stddev≈28.7
	ts := makeSlopeSeries(t0, step, 10, 0.0, 10.0)
	sd := seriesStddev(&ts) // should be ~28.7

	start := t0.Add(4 * step)
	end := t0.Add(4 * step) // single point
	magnitude := 3.0

	original4 := ts.Points[4].V
	rng := rand.New(rand.NewSource(42))
	faultSpike(&ts, start, end, magnitude, rng)

	increase := ts.Points[4].V - original4
	expectedApprox := magnitude * sd
	// With ±5% jitter, allow 15% tolerance
	if math.Abs(increase-expectedApprox) > expectedApprox*0.15 {
		t.Errorf("spike increase=%f, expected ~%f (magnitude=%f, sd=%f)", increase, expectedApprox, magnitude, sd)
	}
}
