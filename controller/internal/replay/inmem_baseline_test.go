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
	for i := 0; i < 4; i++ {
		r, _ := s.Evaluate(context.Background(), "cpu", labels, 0.5)
		if !r.IsWarmingUp {
			t.Errorf("sample %d should still be warming up", i+1)
		}
	}
	r, _ := s.Evaluate(context.Background(), "cpu", labels, 0.5)
	// 5th sample → warmed up. Variance is 0 → no anomaly possible.
	if r.IsWarmingUp {
		t.Errorf("5th sample should not be warming up")
	}
	if r.IsAnomaly {
		t.Errorf("zero-variance series cannot be anomaly")
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
