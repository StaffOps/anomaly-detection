package baseline

import (
	"context"
	"fmt"
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

// fakeHashStore is an in-memory implementation of hashStore for testing.
type fakeHashStore struct {
	data map[string]map[string]string
	err  error // if set, all operations return this error
}

func newFakeHashStore() *fakeHashStore {
	return &fakeHashStore{data: make(map[string]map[string]string)}
}

func (f *fakeHashStore) HSet(_ context.Context, key string, values map[string]interface{}) error {
	if f.err != nil {
		return f.err
	}
	if f.data[key] == nil {
		f.data[key] = make(map[string]string)
	}
	for k, v := range values {
		f.data[key][k] = fmt.Sprintf("%v", v)
	}
	return nil
}

func (f *fakeHashStore) HGetAll(_ context.Context, key string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if d, ok := f.data[key]; ok {
		// Return a copy to avoid mutation issues
		out := make(map[string]string, len(d))
		for k, v := range d {
			out[k] = v
		}
		return out, nil
	}
	return map[string]string{}, nil
}

func defaultCfg() config.Baseline {
	return config.Baseline{
		EWMAAlpha:       0.3,
		ZScoreThreshold: 3.0,
		WarmUpSamples:   5,
	}
}

func TestEvaluate_FirstSample_IsWarmingUp(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	res, err := s.Evaluate(context.Background(), "cpu", map[string]string{"pod": "p1"}, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsWarmingUp {
		t.Error("first sample should be warming up")
	}
	if res.IsAnomaly {
		t.Error("first sample should never be anomalous")
	}
	if res.Value != 0.5 {
		t.Errorf("value: want 0.5, got %v", res.Value)
	}
}

func TestEvaluate_WarmupPhase(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	ctx := context.Background()

	// Feed samples below WarmUpSamples (5)
	for i := 0; i < 4; i++ {
		res, err := s.Evaluate(ctx, "cpu", map[string]string{"pod": "p1"}, float64(i))
		if err != nil {
			t.Fatalf("sample %d: unexpected error: %v", i, err)
		}
		if !res.IsWarmingUp {
			t.Errorf("sample %d: should still be warming up", i)
		}
	}
}

func TestEvaluate_PostWarmup_NormalValue(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	ctx := context.Background()
	labels := map[string]string{"pod": "p1"}

	// Build a stable baseline: feed 10 samples of value 1.0
	for i := 0; i < 10; i++ {
		_, err := s.Evaluate(ctx, "cpu", labels, 1.0)
		if err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}

	// A normal value should not be anomalous
	res, err := s.Evaluate(ctx, "cpu", labels, 1.01)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsAnomaly {
		t.Errorf("near-baseline value should not be anomalous (z=%.2f)", res.ZScore)
	}
}

func TestEvaluate_PostWarmup_SpikeIsAnomalous(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	ctx := context.Background()
	labels := map[string]string{"pod": "p1"}

	// Build a very stable baseline: 20 samples of 1.0 → mean≈1.0, stddev≈0 initially then small
	for i := 0; i < 20; i++ {
		_, err := s.Evaluate(ctx, "cpu", labels, 1.0)
		if err != nil {
			t.Fatalf("baseline sample %d: %v", i, err)
		}
	}

	// A massive spike (100x) should be detected
	res, err := s.Evaluate(ctx, "cpu", labels, 100.0)
	if err != nil {
		t.Fatalf("unexpected error on spike: %v", err)
	}
	if !res.IsAnomaly {
		t.Errorf("massive spike should be anomalous (z=%.2f, threshold=%.2f)", res.ZScore, defaultCfg().ZScoreThreshold)
	}
}

func TestEvaluate_DifferentSeriesAreIsolated(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	ctx := context.Background()

	// Build baseline for pod p1
	for i := 0; i < 10; i++ {
		_, _ = s.Evaluate(ctx, "cpu", map[string]string{"pod": "p1"}, 1.0)
	}

	// pod p2 is a new series — should still be warming up
	res, err := s.Evaluate(ctx, "cpu", map[string]string{"pod": "p2"}, 50.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsWarmingUp {
		t.Error("p2 should still be warming up (different series from p1)")
	}
}

func TestEvaluate_TracksDistinctSeriesCount(t *testing.T) {
	s := NewStore(newFakeHashStore(), defaultCfg())
	ctx := context.Background()

	// Same series evaluated repeatedly must count once.
	for i := 0; i < 5; i++ {
		_, _ = s.Evaluate(ctx, "cpu", map[string]string{"pod": "p1"}, 1.0)
	}
	if got := len(s.seenSeries); got != 1 {
		t.Errorf("repeated evaluations of the same series: want 1 tracked series, got %d", got)
	}

	// A genuinely new series must add to the count.
	_, _ = s.Evaluate(ctx, "cpu", map[string]string{"pod": "p2"}, 1.0)
	_, _ = s.Evaluate(ctx, "memory", map[string]string{"pod": "p1"}, 1.0)
	if got := len(s.seenSeries); got != 3 {
		t.Errorf("want 3 distinct tracked series (cpu/p1, cpu/p2, memory/p1), got %d", got)
	}
}

func TestEvaluate_RedisLoadError(t *testing.T) {
	store := newFakeHashStore()
	store.err = fmt.Errorf("redis timeout")
	s := NewStore(store, defaultCfg())

	_, err := s.Evaluate(context.Background(), "cpu", map[string]string{"pod": "p1"}, 1.0)
	if err == nil {
		t.Error("expected error when Redis load fails")
	}
}

func TestEvaluate_RedisSaveError(t *testing.T) {
	store := newFakeHashStore()
	s := NewStore(store, defaultCfg())
	ctx := context.Background()

	// First call populates in-memory (HGetAll returns empty → first sample path)
	// On the save, trigger an error
	// We can't easily intercept mid-call, but we can set err before second call
	// Let's test save error on a non-first sample by setting err after first success
	_, _ = s.Evaluate(ctx, "cpu", map[string]string{"pod": "p1"}, 1.0)
	store.err = fmt.Errorf("redis write error")

	_, err := s.Evaluate(ctx, "cpu", map[string]string{"pod": "p1"}, 1.0)
	if err == nil {
		t.Error("expected error when Redis save fails")
	}
}

// ─── Pure function tests ──────────────────────────────────────────────────────

func TestBaselineKey_Deterministic(t *testing.T) {
	labels := map[string]string{"pod": "p1", "namespace": "prod"}
	k1 := baselineKey("cpu", labels)
	k2 := baselineKey("cpu", labels)
	if k1 != k2 {
		t.Error("baselineKey should be deterministic")
	}
}

func TestBaselineKey_DifferentMetricsDiffer(t *testing.T) {
	labels := map[string]string{"pod": "p1"}
	k1 := baselineKey("cpu", labels)
	k2 := baselineKey("memory", labels)
	if k1 == k2 {
		t.Error("different metrics should produce different keys")
	}
}

func TestBaselineKey_DifferentLabelsDiffer(t *testing.T) {
	k1 := baselineKey("cpu", map[string]string{"pod": "p1"})
	k2 := baselineKey("cpu", map[string]string{"pod": "p2"})
	if k1 == k2 {
		t.Error("different label values should produce different keys")
	}
}

func TestLabelsHash_EmptyLabels(t *testing.T) {
	h := labelsHash(map[string]string{})
	if h != "none" {
		t.Errorf("empty labels should hash to 'none', got %q", h)
	}
}

func TestLabelsHash_OrderIndependent(t *testing.T) {
	h1 := labelsHash(map[string]string{"a": "1", "b": "2"})
	h2 := labelsHash(map[string]string{"b": "2", "a": "1"})
	if h1 != h2 {
		t.Errorf("hash should be order-independent: %q vs %q", h1, h2)
	}
}

func TestParseStats_RoundTrip(t *testing.T) {
	data := map[string]string{
		"mean":        "1.5",
		"stddev":      "0.25",
		"ewma":        "1.4",
		"count":       "42",
		"last_update": "1700000000",
	}
	s := parseStats(data)
	if s.Mean != 1.5 {
		t.Errorf("mean: want 1.5, got %v", s.Mean)
	}
	if s.Stddev != 0.25 {
		t.Errorf("stddev: want 0.25, got %v", s.Stddev)
	}
	if s.Count != 42 {
		t.Errorf("count: want 42, got %v", s.Count)
	}
}
