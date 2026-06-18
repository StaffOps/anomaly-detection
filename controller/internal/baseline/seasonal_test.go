package baseline

import (
	"context"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func seasonalCfg() config.Baseline {
	return config.Baseline{SeasonalMinDays: 2}
}

func TestSeasonalProfile_UpdateAndGet(t *testing.T) {
	store := newFakeHashStore()
	s := NewSeasonalProfile(store, seasonalCfg())
	ctx := context.Background()
	labels := map[string]string{"pod": "p1"}

	// Feed enough samples to pass minSamples check
	for i := 0; i < 5; i++ {
		if err := s.Update(ctx, "cpu", labels, 1.0); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}

	stats, ok := s.Get(ctx, "cpu", labels)
	if !ok {
		t.Fatal("expected seasonal stats to be available after 5 updates (minSamples=2)")
	}
	if stats.Samples != 5 {
		t.Errorf("expected 5 samples, got %d", stats.Samples)
	}
}

func TestSeasonalProfile_Get_InsufficientSamples(t *testing.T) {
	store := newFakeHashStore()
	s := NewSeasonalProfile(store, config.Baseline{SeasonalMinDays: 10})
	ctx := context.Background()

	// Feed only 2 samples — below minSamples=10
	for i := 0; i < 2; i++ {
		_ = s.Update(ctx, "cpu", map[string]string{"pod": "p1"}, 1.0)
	}

	_, ok := s.Get(ctx, "cpu", map[string]string{"pod": "p1"})
	if ok {
		t.Error("should return false when samples < minSamples")
	}
}

func TestSeasonalProfile_Get_NoData(t *testing.T) {
	s := NewSeasonalProfile(newFakeHashStore(), seasonalCfg())
	_, ok := s.Get(context.Background(), "cpu", map[string]string{"pod": "p1"})
	if ok {
		t.Error("should return false for unknown series")
	}
}

func TestSeasonalProfile_Update_StoreError(t *testing.T) {
	store := newFakeHashStore()
	s := NewSeasonalProfile(store, seasonalCfg())
	store.err = context.DeadlineExceeded
	err := s.Update(context.Background(), "cpu", map[string]string{"pod": "p1"}, 1.0)
	if err == nil {
		t.Error("expected error when store fails")
	}
}

func TestSeasonalProfile_IsSeasonalAnomaly_NotEnoughData(t *testing.T) {
	s := NewSeasonalProfile(newFakeHashStore(), seasonalCfg())
	isAnomaly, score := s.IsSeasonalAnomaly(context.Background(), "cpu", map[string]string{}, 100.0)
	if isAnomaly {
		t.Error("should not be anomalous without baseline data")
	}
	if score != 0 {
		t.Errorf("score should be 0 without data, got %v", score)
	}
}

func TestSeasonalProfile_IsSeasonalAnomaly_NormalValue(t *testing.T) {
	store := newFakeHashStore()
	s := NewSeasonalProfile(store, config.Baseline{SeasonalMinDays: 1})
	ctx := context.Background()
	labels := map[string]string{"svc": "api"}

	// Build stable baseline: 5 samples of 1.0
	for i := 0; i < 5; i++ {
		_ = s.Update(ctx, "cpu", labels, 1.0)
	}

	isAnomaly, _ := s.IsSeasonalAnomaly(ctx, "cpu", labels, 1.01)
	if isAnomaly {
		t.Error("near-baseline value should not be seasonal anomaly")
	}
}

func TestSeasonalProfile_IsSeasonalAnomaly_Spike(t *testing.T) {
	store := newFakeHashStore()
	s := NewSeasonalProfile(store, config.Baseline{SeasonalMinDays: 1})
	ctx := context.Background()
	labels := map[string]string{"svc": "api"}

	// Build stable baseline: 20 samples of 1.0 → stddev will be non-zero
	for i := 0; i < 10; i++ {
		_ = s.Update(ctx, "cpu", labels, float64(i%2)) // alternating 0 and 1
	}

	isAnomaly, score := s.IsSeasonalAnomaly(ctx, "cpu", labels, 100.0)
	if !isAnomaly {
		t.Errorf("massive spike should be seasonal anomaly (score=%.2f)", score)
	}
}

func TestParseSeasonalStats_RoundTrip(t *testing.T) {
	data := map[string]string{
		"mean":    "2.5",
		"stddev":  "0.5",
		"samples": "100",
	}
	s := parseSeasonalStats(data)
	if s.Mean != 2.5 {
		t.Errorf("mean: want 2.5, got %v", s.Mean)
	}
	if s.Stddev != 0.5 {
		t.Errorf("stddev: want 0.5, got %v", s.Stddev)
	}
	if s.Samples != 100 {
		t.Errorf("samples: want 100, got %v", s.Samples)
	}
}

func TestParseSeasonalStats_Empty(t *testing.T) {
	s := parseSeasonalStats(map[string]string{})
	if s.Mean != 0 || s.Stddev != 0 || s.Samples != 0 {
		t.Error("empty data should produce zero stats")
	}
}

func TestSeasonalKey_Deterministic(t *testing.T) {
	// Same metric+labels+time should produce same key
	labels := map[string]string{"pod": "p1"}
	k1 := seasonalKey("cpu", labels, fixedTime)
	k2 := seasonalKey("cpu", labels, fixedTime)
	if k1 != k2 {
		t.Error("seasonalKey should be deterministic")
	}
}

func TestSeasonalKey_DifferentHoursDiffer(t *testing.T) {
	labels := map[string]string{"pod": "p1"}
	k1 := seasonalKey("cpu", labels, fixedTime)
	k2 := seasonalKey("cpu", labels, fixedTime.Add(2*3600*1e9)) // +2h
	if k1 == k2 {
		t.Error("different hours should produce different seasonal keys")
	}
}
