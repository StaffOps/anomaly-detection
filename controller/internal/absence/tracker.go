package absence

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Config holds settings for the absence-of-signal detector.
type Config struct {
	Enabled          bool          `yaml:"enabled"`
	Threshold        time.Duration `yaml:"threshold"`         // silence duration before alerting (default 5m)
	CheckInterval    time.Duration `yaml:"check_interval"`    // how often to scan (default 30s)
	StartupGrace     time.Duration `yaml:"startup_grace"`     // skip alerts after boot (default 2× threshold)
	SuppressPatterns []string      `yaml:"suppress_patterns"` // namespace patterns to suppress (e.g. "batch-*", "keda-*")
}

// Alert represents an absence-of-signal detection.
type Alert struct {
	SeriesKey string
	Metric    string
	Labels    map[string]string
	LastSeen  time.Time
	SilentFor time.Duration
}

// Tracker records when each series was last seen and detects silence.
type Tracker struct {
	mu         sync.RWMutex
	series     map[string]*seriesState
	cfg        Config
	startedAt  time.Time
	suppressed map[string]struct{} // pre-compiled suppress patterns (simple prefix match)
}

type seriesState struct {
	metric   string
	labels   map[string]string
	lastSeen time.Time
}

// NewTracker creates an absence tracker. Call RecordSample on every sample
// processed by the baseline, then call Check periodically to find silent series.
func NewTracker(cfg Config) *Tracker {
	supp := make(map[string]struct{}, len(cfg.SuppressPatterns))
	for _, p := range cfg.SuppressPatterns {
		supp[p] = struct{}{}
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = 5 * time.Minute
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 30 * time.Second
	}
	if cfg.StartupGrace == 0 {
		cfg.StartupGrace = 2 * cfg.Threshold
	}
	return &Tracker{
		series:     make(map[string]*seriesState),
		cfg:        cfg,
		startedAt:  time.Now(),
		suppressed: supp,
	}
}

// RecordSample marks a series as active. Called on every baseline evaluation.
func (t *Tracker) RecordSample(metric string, labels map[string]string) {
	key := seriesKey(metric, labels)
	t.mu.Lock()
	s, exists := t.series[key]
	if !exists {
		s = &seriesState{metric: metric, labels: labels}
		t.series[key] = s
	}
	s.lastSeen = time.Now()
	t.mu.Unlock()
}

// Check scans all tracked series and returns alerts for those that have gone silent.
func (t *Tracker) Check() []Alert {
	if !t.cfg.Enabled {
		return nil
	}

	// Startup grace: don't alert during initial period.
	if time.Since(t.startedAt) < t.cfg.StartupGrace {
		return nil
	}

	now := time.Now()
	t.mu.RLock()
	defer t.mu.RUnlock()

	var alerts []Alert
	for _, s := range t.series {
		silentFor := now.Sub(s.lastSeen)
		if silentFor <= t.cfg.Threshold {
			continue
		}
		if t.isSuppressed(s.labels) {
			continue
		}
		alerts = append(alerts, Alert{
			SeriesKey: seriesKey(s.metric, s.labels),
			Metric:    s.metric,
			Labels:    s.labels,
			LastSeen:  s.lastSeen,
			SilentFor: silentFor,
		})
	}
	return alerts
}

// SeriesCount returns the number of tracked series.
func (t *Tracker) SeriesCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.series)
}

// Run starts the background checker goroutine. Blocks until ctx is cancelled.
// Calls onAlerts for each batch of absence alerts detected.
func (t *Tracker) Run(ctx context.Context, onAlerts func([]Alert)) {
	ticker := time.NewTicker(t.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			alerts := t.Check()
			if len(alerts) > 0 {
				slog.Info("absence_detected", "count", len(alerts))
				onAlerts(alerts)
			}
		}
	}
}

func (t *Tracker) isSuppressed(labels map[string]string) bool {
	ns := labels["namespace"]
	if ns == "" {
		return false
	}
	for pattern := range t.suppressed {
		if matchPattern(pattern, ns) {
			return true
		}
	}
	return false
}

// matchPattern does simple glob matching (only trailing *).
func matchPattern(pattern, value string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, pattern[:len(pattern)-1])
	}
	return pattern == value
}

func seriesKey(metric string, labels map[string]string) string {
	return fmt.Sprintf("%s:%v", metric, labels)
}
