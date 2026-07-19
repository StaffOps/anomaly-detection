package replay

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/baseline"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

// InMemStore is an in-memory implementation of baseline.Evaluator used by
// replay mode. It mirrors *baseline.Store's Welford + EWMA math exactly so
// replay results match what production would produce, but stores stats in a
// concurrent map instead of Redis.
//
// InMemStore deliberately does NOT increment production metrics
// (metrics.WorkerBaselineUpdates etc.) — replay must be a no-op for the
// production observability stack.
type InMemStore struct {
	cfg    config.Baseline
	mu     sync.Mutex
	series map[string]*baseline.Stats
}

// Compile-time check that *InMemStore satisfies baseline.Evaluator.
var _ baseline.Evaluator = (*InMemStore)(nil)

// NewInMemStore creates an empty in-memory baseline store.
func NewInMemStore(cfg config.Baseline) *InMemStore {
	return &InMemStore{
		cfg:    cfg,
		series: make(map[string]*baseline.Stats),
	}
}

// Evaluate updates the in-memory baseline for a series and returns a Result
// matching production semantics:
//   - First sample → IsWarmingUp=true, IsAnomaly=false
//   - Sample count < WarmUpSamples → IsWarmingUp=true, IsAnomaly=false
//   - Otherwise → IsAnomaly = (|value - EWMA| / stddev > ZScoreThreshold)
//
// ctx is accepted for interface compatibility but is not used (in-memory
// store has no I/O that could be canceled).
func (s *InMemStore) Evaluate(_ context.Context, metric string, labels map[string]string, value float64) (*baseline.Result, error) {
	key := inMemKey(metric, labels)

	s.mu.Lock()
	defer s.mu.Unlock()

	stats, exists := s.series[key]

	// First sample for this series.
	if !exists || stats.Count == 0 {
		s.series[key] = &baseline.Stats{
			Mean:       value,
			Stddev:     0,
			EWMA:       value,
			Count:      1,
			LastUpdate: time.Now().UTC(),
		}
		return &baseline.Result{
			IsWarmingUp: true,
			Value:       value,
			Mean:        value,
			EWMA:        value,
		}, nil
	}

	// Z-score against the CURRENT baseline (BEFORE any update) — matches
	// production (internal/baseline/store.go), including the stddev floor.
	// Detecting on POST-update stats (as this store used to) dampens the
	// numerator — the EWMA has already moved toward `value` — and inflates the
	// denominator — the stddev now includes the spike — so injected/real faults
	// under-fire in replay vs production. That gap is exactly what broke the
	// P0.1 recall measurement.
	stddev := stats.Stddev
	if stddev == 0 {
		floor := math.Max(math.Abs(stats.EWMA)*0.01, math.Abs(value)*0.01)
		stddev = math.Max(floor, 1e-9)
	}
	zscore := math.Abs(value-stats.EWMA) / stddev

	// Anti-poisoning gate (production parity): skip the baseline update when the
	// sample is extremely anomalous (z > poison_threshold). 0 disables it.
	poisoned := s.cfg.PoisonThreshold > 0 && zscore > s.cfg.PoisonThreshold
	isWarmingUp := stats.Count < int64(s.cfg.WarmUpSamples)

	if !poisoned || isWarmingUp {
		alpha := s.cfg.EWMAAlpha
		newEWMA := alpha*value + (1-alpha)*stats.EWMA
		newCount := stats.Count + 1
		delta := value - stats.Mean
		newMean := stats.Mean + delta/float64(newCount)
		delta2 := value - newMean
		m2 := stats.Stddev * stats.Stddev * float64(stats.Count)
		newM2 := m2 + delta*delta2
		var newStddev float64
		if newCount > 1 {
			newStddev = math.Sqrt(newM2 / float64(newCount))
		}
		s.series[key] = &baseline.Stats{
			Mean:       newMean,
			Stddev:     newStddev,
			EWMA:       newEWMA,
			Count:      newCount,
			LastUpdate: time.Now().UTC(),
		}
		if isWarmingUp {
			return &baseline.Result{
				IsWarmingUp: true, Value: value, Mean: newMean, Stddev: newStddev, EWMA: newEWMA,
			}, nil
		}
		return &baseline.Result{
			IsAnomaly: zscore > s.cfg.ZScoreThreshold, ZScore: zscore,
			Value: value, Mean: newMean, Stddev: newStddev, EWMA: newEWMA,
		}, nil
	}

	// Poisoned: return the anomaly WITHOUT updating the baseline.
	return &baseline.Result{
		IsAnomaly: zscore > s.cfg.ZScoreThreshold, ZScore: zscore,
		Value: value, Mean: stats.Mean, Stddev: stats.Stddev, EWMA: stats.EWMA,
	}, nil
}

// SeriesCount returns the number of series tracked. Used for diagnostics
// and execution metrics.
func (s *InMemStore) SeriesCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.series)
}

// inMemKey produces a deterministic key for (metric, labels). Same hashing
// scheme as baseline.baselineKey but inlined to avoid exposing internals
// across packages.
func inMemKey(metric string, labels map[string]string) string {
	if len(labels) == 0 {
		return metric + ":none"
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
	return fmt.Sprintf("%s:%x", metric, h[:8])
}
