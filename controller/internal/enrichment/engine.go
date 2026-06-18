// Package enrichment implements label-based pivot for anomaly diagnostics.
//
// When an anomaly fires for a workload, the engine fans out config-driven
// queries with the same labels (pod, namespace, service_name, etc.) to
// build a context bundle. The bundle is attached to the alert payload so
// operators get a full picture without opening multiple dashboards.
//
// Caching: results per Identity are cached in Redis with TTL to avoid
// re-running the same bundle for repeated anomalies in the same window.
package enrichment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// vmQuerier is the minimal interface for instant PromQL queries (satisfied by *ingestion.MetricsPoller).
type vmQuerier interface {
	Query(ctx context.Context, query string) ([]ingestion.Sample, error)
}

// lokiQuerier is the minimal interface for instant LogQL metric queries (satisfied by *ingestion.LogsPoller).
type lokiQuerier interface {
	QueryMetric(ctx context.Context, query string) ([]ingestion.Sample, error)
}

// enrichCache is the minimal Redis interface for caching enrichment bundles.
type enrichCache interface {
	Get(ctx context.Context, key string) (string, error)
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

// Result is the outcome of one enrichment query.
type Result struct {
	Name   string            `json:"name"`
	Source string            `json:"source"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// Bundle is the full enrichment context for an alert.
type Bundle struct {
	Identity Identity      `json:"identity"`
	Results  []Result      `json:"results"`
	Cached   bool          `json:"cached"`
	Took     time.Duration `json:"took_ms"`
}

// Engine runs enrichment bundles when alerts fire.
type Engine struct {
	cfg      config.Enrichment
	vmPoll   vmQuerier
	lokiPoll lokiQuerier
	redis    enrichCache
}

// NewEngine constructs an Engine. Loki poller and redis may be nil if not configured.
func NewEngine(cfg config.Enrichment, vm vmQuerier, loki lokiQuerier, r enrichCache) *Engine {
	return &Engine{
		cfg:      cfg,
		vmPoll:   vm,
		lokiPoll: loki,
		redis:    r,
	}
}

// Run executes the enrichment bundle matching the identity kind and returns
// the result bundle. If enrichment is disabled or the identity is unknown,
// returns an empty bundle without error.
func (e *Engine) Run(ctx context.Context, id Identity) Bundle {
	if !e.cfg.Enabled {
		return Bundle{Identity: id}
	}

	queries := e.bundleFor(id)
	if len(queries) == 0 {
		return Bundle{Identity: id}
	}

	// Cache lookup
	if cached, ok := e.loadCache(ctx, id); ok {
		metrics.EnrichmentCacheHits.Inc()
		cached.Cached = true
		return cached
	}
	metrics.EnrichmentCacheMisses.Inc()

	// Fan out queries with bounded concurrency
	start := time.Now()
	results := e.runQueries(ctx, id, queries)
	bundle := Bundle{Identity: id, Results: results, Took: time.Since(start)}

	// Cache successful runs (even partially, to dampen retries on failures)
	e.storeCache(ctx, bundle)

	metrics.EnrichmentRuns.WithLabelValues(kindLabel(id.Kind())).Inc()
	metrics.EnrichmentDuration.WithLabelValues(kindLabel(id.Kind())).Observe(time.Since(start).Seconds())
	return bundle
}

func (e *Engine) bundleFor(id Identity) []config.EnrichmentQuery {
	switch id.Kind() {
	case KindPod:
		return e.cfg.PodBundle
	case KindService:
		return e.cfg.ServiceBundle
	}
	return nil
}

func (e *Engine) runQueries(ctx context.Context, id Identity, queries []config.EnrichmentQuery) []Result {
	results := make([]Result, len(queries))
	sem := make(chan struct{}, e.cfg.MaxConcurrent)
	var wg sync.WaitGroup

	for i, q := range queries {
		i, q := i, q
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			callCtx, cancel := context.WithTimeout(ctx, e.cfg.QueryTimeout)
			defer cancel()

			results[i] = e.runOne(callCtx, id, q)
		}()
	}

	wg.Wait()
	return results
}

func (e *Engine) runOne(ctx context.Context, id Identity, q config.EnrichmentQuery) Result {
	rendered := substitute(q.Query, id)
	source := q.Source
	if source == "" {
		source = "vm"
	}

	r := Result{Name: q.Name, Source: source}

	switch source {
	case "vm":
		samples, err := e.vmPoll.Query(ctx, rendered)
		if err != nil {
			metrics.EnrichmentQueryErrors.WithLabelValues(source).Inc()
			r.Error = err.Error()
			return r
		}
		if len(samples) == 0 {
			r.Error = "no_data"
			return r
		}
		// Take first sample (caller's query should aggregate to a scalar via PromQL)
		r.Value = samples[0].Value
		r.Labels = samples[0].Labels

	case "loki":
		if e.lokiPoll == nil {
			r.Error = "loki_not_configured"
			return r
		}
		samples, err := e.lokiPoll.QueryMetric(ctx, rendered)
		if err != nil {
			metrics.EnrichmentQueryErrors.WithLabelValues(source).Inc()
			r.Error = err.Error()
			return r
		}
		if len(samples) == 0 {
			r.Error = "no_data"
			return r
		}
		r.Value = samples[0].Value
		r.Labels = samples[0].Labels

	default:
		r.Error = fmt.Sprintf("unknown_source: %s", source)
	}

	return r
}

func (e *Engine) loadCache(ctx context.Context, id Identity) (Bundle, bool) {
	if e.redis == nil {
		return Bundle{}, false
	}
	raw, err := e.redis.Get(ctx, id.CacheKey())
	if err != nil || raw == "" {
		return Bundle{}, false
	}
	var b Bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return Bundle{}, false
	}
	return b, true
}

func (e *Engine) storeCache(ctx context.Context, b Bundle) {
	if e.redis == nil {
		return
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return
	}
	if err := e.redis.SetWithTTL(ctx, b.Identity.CacheKey(), string(raw), e.cfg.CacheTTL); err != nil {
		slog.Debug("enrichment cache store failed", "error", err)
	}
}

func kindLabel(k Kind) string {
	switch k {
	case KindPod:
		return "pod"
	case KindService:
		return "service"
	}
	return "unknown"
}
