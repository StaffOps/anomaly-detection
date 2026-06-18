package baseline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Stats holds the baseline statistics for a single series.
type Stats struct {
	Mean       float64
	Stddev     float64
	EWMA       float64
	Count      int64
	LastUpdate time.Time
}

// Result is the output of evaluating a value against its baseline.
type Result struct {
	IsAnomaly  bool
	ZScore     float64
	Value      float64
	Mean       float64
	Stddev     float64
	EWMA       float64
	IsWarmingUp bool
}

// Evaluator updates a baseline with a new sample and returns whether the
// sample is anomalous. Implementations: *Store (Redis-backed, used in
// production) and *replay.InMemStore (in-memory, used in replay mode).
//
// Behavior contract:
//   - First sample for a series MUST return IsWarmingUp=true
//   - Samples below WarmUpSamples count MUST return IsWarmingUp=true and
//     IsAnomaly=false
//   - Once warmed up, IsAnomaly is true iff |value - EWMA| / stddev > threshold
type Evaluator interface {
	Evaluate(ctx context.Context, metric string, labels map[string]string, value float64) (*Result, error)
}

// hashStore is the minimal Redis interface needed for baseline persistence.
// Extracted as an interface so tests can substitute a fake without a real Redis.
type hashStore interface {
	HSet(ctx context.Context, key string, values map[string]interface{}) error
	HGetAll(ctx context.Context, key string) (map[string]string, error)
}

// Store manages baselines in Redis.
type Store struct {
	redis hashStore
	cfg   config.Baseline
}

// Compile-time check that Store satisfies Evaluator.
var _ Evaluator = (*Store)(nil)

func NewStore(redis hashStore, cfg config.Baseline) *Store {
	return &Store{redis: redis, cfg: cfg}
}

// Evaluate updates the baseline for a series and returns whether the value is anomalous.
func (s *Store) Evaluate(ctx context.Context, metric string, labels map[string]string, value float64) (*Result, error) {
	key := baselineKey(metric, labels)

	stats, err := s.load(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("load baseline: %w", err)
	}

	// First sample
	if stats.Count == 0 {
		stats = &Stats{
			Mean:       value,
			Stddev:     0,
			EWMA:       value,
			Count:      1,
			LastUpdate: time.Now(),
		}
		if err := s.save(ctx, key, stats); err != nil {
			return nil, err
		}
		return &Result{IsWarmingUp: true, Value: value, Mean: value, EWMA: value}, nil
	}

	// Update EWMA
	alpha := s.cfg.EWMAAlpha
	newEWMA := alpha*value + (1-alpha)*stats.EWMA

	// Update running mean and stddev (Welford's algorithm)
	newCount := stats.Count + 1
	delta := value - stats.Mean
	newMean := stats.Mean + delta/float64(newCount)
	delta2 := value - newMean
	// Running variance: M2 = stddev^2 * (count-1)
	m2 := stats.Stddev * stats.Stddev * float64(stats.Count)
	newM2 := m2 + delta*delta2
	var newStddev float64
	if newCount > 1 {
		newStddev = math.Sqrt(newM2 / float64(newCount))
	}

	updated := &Stats{
		Mean:       newMean,
		Stddev:     newStddev,
		EWMA:       newEWMA,
		Count:      newCount,
		LastUpdate: time.Now(),
	}

	if err := s.save(ctx, key, updated); err != nil {
		return nil, err
	}

	metrics.WorkerBaselineUpdates.Inc()

	// Warm-up: not enough samples for reliable detection
	if newCount < int64(s.cfg.WarmUpSamples) {
		return &Result{
			IsWarmingUp: true,
			Value:       value,
			Mean:        newMean,
			Stddev:      newStddev,
			EWMA:        newEWMA,
		}, nil
	}

	// Z-Score against EWMA baseline
	var zscore float64
	if newStddev > 0 {
		zscore = math.Abs(value-newEWMA) / newStddev
	}

	return &Result{
		IsAnomaly: zscore > s.cfg.ZScoreThreshold,
		ZScore:    zscore,
		Value:     value,
		Mean:      newMean,
		Stddev:    newStddev,
		EWMA:      newEWMA,
	}, nil
}

func (s *Store) load(ctx context.Context, key string) (*Stats, error) {
	data, err := s.redis.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return &Stats{}, nil
	}
	return parseStats(data), nil
}

func (s *Store) save(ctx context.Context, key string, stats *Stats) error {
	return s.redis.HSet(ctx, key, map[string]interface{}{
		"mean":        strconv.FormatFloat(stats.Mean, 'f', -1, 64),
		"stddev":      strconv.FormatFloat(stats.Stddev, 'f', -1, 64),
		"ewma":        strconv.FormatFloat(stats.EWMA, 'f', -1, 64),
		"count":       strconv.FormatInt(stats.Count, 10),
		"last_update": strconv.FormatInt(stats.LastUpdate.Unix(), 10),
	})
}

func parseStats(data map[string]string) *Stats {
	s := &Stats{}
	s.Mean, _ = strconv.ParseFloat(data["mean"], 64)
	s.Stddev, _ = strconv.ParseFloat(data["stddev"], 64)
	s.EWMA, _ = strconv.ParseFloat(data["ewma"], 64)
	s.Count, _ = strconv.ParseInt(data["count"], 10, 64)
	ts, _ := strconv.ParseInt(data["last_update"], 10, 64)
	s.LastUpdate = time.Unix(ts, 0)
	return s
}

func baselineKey(metric string, labels map[string]string) string {
	return fmt.Sprintf("baseline:%s:%s", metric, labelsHash(labels))
}

func labelsHash(labels map[string]string) string {
	if len(labels) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte(',')
	}
	h := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", h[:8])
}
