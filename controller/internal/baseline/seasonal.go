package baseline

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

// SeasonalProfile tracks per-hour-of-day, per-day-of-week baselines.
type SeasonalProfile struct {
	redis hashStore
	cfg   config.Baseline
}

// SeasonalStats holds the seasonal baseline for a specific time slot.
type SeasonalStats struct {
	Mean    float64
	Stddev  float64
	Samples int64
}

func NewSeasonalProfile(redis hashStore, cfg config.Baseline) *SeasonalProfile {
	return &SeasonalProfile{redis: redis, cfg: cfg}
}

// Update records a value into the seasonal bucket for the current time.
func (s *SeasonalProfile) Update(ctx context.Context, metric string, labels map[string]string, value float64) error {
	now := time.Now()
	key := seasonalKey(metric, labels, now)

	data, err := s.redis.HGetAll(ctx, key)
	if err != nil {
		return err
	}

	stats := parseSeasonalStats(data)
	stats.Samples++

	// Welford's online update
	delta := value - stats.Mean
	stats.Mean += delta / float64(stats.Samples)
	delta2 := value - stats.Mean
	m2 := stats.Stddev * stats.Stddev * float64(stats.Samples-1)
	newM2 := m2 + delta*delta2
	if stats.Samples > 1 {
		stats.Stddev = math.Sqrt(newM2 / float64(stats.Samples))
	}

	return s.redis.HSet(ctx, key, map[string]interface{}{
		"mean":    strconv.FormatFloat(stats.Mean, 'f', -1, 64),
		"stddev":  strconv.FormatFloat(stats.Stddev, 'f', -1, 64),
		"samples": strconv.FormatInt(stats.Samples, 10),
	})
}

// Get returns the seasonal baseline for the current time slot, if enough data exists.
func (s *SeasonalProfile) Get(ctx context.Context, metric string, labels map[string]string) (*SeasonalStats, bool) {
	now := time.Now()
	key := seasonalKey(metric, labels, now)

	data, err := s.redis.HGetAll(ctx, key)
	if err != nil || len(data) == 0 {
		return nil, false
	}

	stats := parseSeasonalStats(data)

	// Need minimum samples (seasonal_min_days * samples_per_slot_per_day)
	// At 30s interval, 1 slot = 1 hour = 120 samples/day for that slot
	// min_days=7 → need at least 7 samples for that hour/dow combo
	minSamples := int64(s.cfg.SeasonalMinDays)
	if stats.Samples < minSamples {
		return nil, false
	}

	return &stats, true
}

// IsSeasonalAnomaly checks if a value deviates from the seasonal baseline.
func (s *SeasonalProfile) IsSeasonalAnomaly(ctx context.Context, metric string, labels map[string]string, value float64) (bool, float64) {
	stats, ok := s.Get(ctx, metric, labels)
	if !ok {
		return false, 0
	}
	if stats.Stddev == 0 {
		return false, 0
	}

	zscore := math.Abs(value-stats.Mean) / stats.Stddev
	return zscore > 3.0, zscore
}

func seasonalKey(metric string, labels map[string]string, t time.Time) string {
	dow := int(t.Weekday()) // 0=Sunday
	hour := t.Hour()
	return fmt.Sprintf("seasonal:%s:%s:%d:%d", metric, labelsHash(labels), dow, hour)
}

func parseSeasonalStats(data map[string]string) SeasonalStats {
	s := SeasonalStats{}
	s.Mean, _ = strconv.ParseFloat(data["mean"], 64)
	s.Stddev, _ = strconv.ParseFloat(data["stddev"], 64)
	s.Samples, _ = strconv.ParseInt(data["samples"], 10, 64)
	return s
}
