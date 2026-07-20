package replay

import (
	"context"
	"sync"
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

func newTestStore() *InMemStore {
	return NewInMemStore(config.Baseline{
		EWMAAlpha:       0.3,
		ZScoreThreshold: 3.0,
		WarmUpSamples:   5, // small for tests
	})
}

func TestInMemStore_FirstSampleWarmingUp(t *testing.T) {
	s := newTestStore()
	r, err := s.Evaluate(context.Background(), "cpu", map[string]string{"pod": "p1"}, 0.42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsWarmingUp {
		t.Errorf("first sample should be warming up")
	}
	if r.IsAnomaly {
		t.Errorf("first sample cannot be anomaly")
	}
	if r.Value != 0.42 || r.Mean != 0.42 || r.EWMA != 0.42 {
		t.Errorf("first sample fields: got value=%f mean=%f ewma=%f", r.Value, r.Mean, r.EWMA)
	}
}

func TestInMemStore_WarmupCompletes(t *testing.T) {
	s := newTestStore() // warm-up=5
	labels := map[string]string{"pod": "p1"}
	// Production parity: isWarmingUp is checked on the PRE-update count
	// (stats.Count < WarmUpSamples), so samples 1..5 warm up and detection
	// starts on the 6th.
	for i := 0; i < 5; i++ {
		r, _ := s.Evaluate(context.Background(), "cpu", labels, 0.5)
		if !r.IsWarmingUp {
			t.Errorf("sample %d should still be warming up", i+1)
		}
	}
	r, _ := s.Evaluate(context.Background(), "cpu", labels, 0.5)
	// 6th sample → warmed up. Variance is 0 (stddev floor applies) → no anomaly.
	if r.IsWarmingUp {
		t.Errorf("6th sample should not be warming up")
	}
	if r.IsAnomaly {
		t.Errorf("zero-variance series cannot be anomaly")
	}
}

// TestInMemStore_SpikeFiresAfterWarmup is the P0.1 Blocker-1 guard: a clear
// spike on a warmed baseline MUST fire. The store used to compute the z-score
// AFTER folding the sample into EWMA/stddev, which dampened it below threshold —
// injected/real faults under-fired in replay vs production. Detecting on the
// pre-update baseline (production parity) fixes it.
func TestInMemStore_SpikeFiresAfterWarmup(t *testing.T) {
	s := newTestStore() // warm-up=5, threshold=3
	labels := map[string]string{"pod": "p1"}
	for _, v := range []float64{1.0, 1.02, 0.98, 1.01, 0.99, 1.0, 1.03, 0.97} {
		if _, err := s.Evaluate(context.Background(), "cpu", labels, v); err != nil {
			t.Fatalf("warmup evaluate: %v", err)
		}
	}
	r, _ := s.Evaluate(context.Background(), "cpu", labels, 3.0) // clear spike on a ~1.0 baseline
	if !r.IsAnomaly {
		t.Errorf("spike to 3.0 must fire, got z=%.2f (threshold %.1f)", r.ZScore, s.cfg.ZScoreThreshold)
	}
	if r.ZScore <= s.cfg.ZScoreThreshold {
		t.Errorf("z-score should clear threshold %.1f, got %.2f", s.cfg.ZScoreThreshold, r.ZScore)
	}
}

// TestInMemStore_PoisonSkipsUpdate covers the anti-poisoning gate (production
// parity): a sample with z > poison_threshold fires but does NOT update the
// baseline, so an extreme spike can't drag the mean until it reads as normal.
func TestInMemStore_PoisonSkipsUpdate(t *testing.T) {
	s := NewInMemStore(config.Baseline{
		EWMAAlpha: 0.3, ZScoreThreshold: 3.0, WarmUpSamples: 3, PoisonThreshold: 4.0,
	})
	labels := map[string]string{"pod": "p1"}
	for _, v := range []float64{1.0, 1.02, 0.98, 1.0} { // warm past warm-up=3
		if _, err := s.Evaluate(context.Background(), "cpu", labels, v); err != nil {
			t.Fatalf("warmup: %v", err)
		}
	}
	r, _ := s.Evaluate(context.Background(), "cpu", labels, 100.0) // z >> poison_threshold
	if !r.IsAnomaly {
		t.Fatalf("extreme spike should fire, z=%.2f", r.ZScore)
	}
	// Poisoned → baseline not updated → the returned EWMA stays the pre-spike ~1.0.
	if r.EWMA > 1.5 {
		t.Errorf("poisoned sample must not drag the baseline; EWMA=%.3f (should stay ~1.0)", r.EWMA)
	}
}

func TestInMemStore_DetectsSpike(t *testing.T) {
	// Use a larger warmup window so Welford's variance estimate is well
	// established before we throw an outlier at it. Otherwise the outlier
	// pollutes both numerator and denominator and z-score collapses to ~2.4
	// regardless of magnitude — same behavior as production with too little
	// history.
	s := NewInMemStore(config.Baseline{
		EWMAAlpha:       0.3,
		ZScoreThreshold: 3.0,
		WarmUpSamples:   20,
	})
	labels := map[string]string{"pod": "p1"}

	// Feed 50 stable samples around 0.5 ± noise (well past warmup of 20)
	stable := []float64{
		0.50, 0.51, 0.49, 0.52, 0.48, 0.50, 0.51, 0.49, 0.50, 0.51,
		0.50, 0.49, 0.51, 0.50, 0.52, 0.49, 0.51, 0.50, 0.51, 0.50,
		0.49, 0.50, 0.51, 0.50, 0.49, 0.50, 0.51, 0.49, 0.50, 0.51,
		0.50, 0.49, 0.51, 0.50, 0.52, 0.49, 0.51, 0.50, 0.51, 0.50,
		0.49, 0.50, 0.51, 0.50, 0.49, 0.50, 0.51, 0.49, 0.50, 0.51,
	}
	for _, v := range stable {
		_, err := s.Evaluate(context.Background(), "cpu", labels, v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Now feed an outlier large enough that EWMA smoothing doesn't hide it.
	r, err := s.Evaluate(context.Background(), "cpu", labels, 10.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.IsWarmingUp {
		t.Errorf("should be warmed up by now")
	}
	if !r.IsAnomaly {
		t.Errorf("expected anomaly for spike, got z=%f", r.ZScore)
	}
}

func TestInMemStore_IndependentSeries(t *testing.T) {
	s := newTestStore()
	for i := 0; i < 10; i++ {
		_, _ = s.Evaluate(context.Background(), "cpu", map[string]string{"pod": "p1"}, 0.5)
	}
	r, _ := s.Evaluate(context.Background(), "cpu", map[string]string{"pod": "p2"}, 0.5)
	if !r.IsWarmingUp {
		t.Errorf("p2 series should be warming up — independent from p1")
	}
}

func TestInMemStore_Concurrent(t *testing.T) {
	s := newTestStore()
	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			defer wg.Done()
			labels := map[string]string{"pod": string(rune('a' + idx))}
			for j := 0; j < 100; j++ {
				_, err := s.Evaluate(context.Background(), "cpu", labels, float64(j)*0.01)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if got := s.SeriesCount(); got != 10 {
		t.Errorf("expected 10 series, got %d", got)
	}
}

func TestInMemStore_KeyHash(t *testing.T) {
	// Same labels, different ordering, must produce same key
	a := inMemKey("cpu", map[string]string{"pod": "p1", "ns": "x"})
	b := inMemKey("cpu", map[string]string{"ns": "x", "pod": "p1"})
	if a != b {
		t.Errorf("key should be order-independent, got %s vs %s", a, b)
	}
}

func TestInMemStore_NoLabels(t *testing.T) {
	s := newTestStore()
	r, err := s.Evaluate(context.Background(), "cpu", nil, 0.42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsWarmingUp {
		t.Errorf("first sample should be warming up")
	}
}
