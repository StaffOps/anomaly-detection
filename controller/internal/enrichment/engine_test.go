package enrichment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// ─── Fakes ────────────────────────────────────────────────────────────────────

type fakeVMQuerier struct {
	samples []ingestion.Sample
	err     error
}

func (f *fakeVMQuerier) Query(_ context.Context, _ string) ([]ingestion.Sample, error) {
	return f.samples, f.err
}

type fakeLokiQuerier struct {
	samples []ingestion.Sample
	err     error
}

func (f *fakeLokiQuerier) QueryMetric(_ context.Context, _ string) ([]ingestion.Sample, error) {
	return f.samples, f.err
}

type fakeEnrichCache struct {
	data map[string]string
	err  error
}

func newFakeCache() *fakeEnrichCache {
	return &fakeEnrichCache{data: make(map[string]string)}
}

func (f *fakeEnrichCache) Get(_ context.Context, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.data[key], nil
}

func (f *fakeEnrichCache) SetWithTTL(_ context.Context, key, value string, _ time.Duration) error {
	if f.err != nil {
		return f.err
	}
	f.data[key] = value
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func podEnrichmentCfg() config.Enrichment {
	return config.Enrichment{
		Enabled:       true,
		MaxConcurrent: 2,
		QueryTimeout:  time.Second,
		CacheTTL:      time.Minute,
		PodBundle: []config.EnrichmentQuery{
			{Name: "cpu_ratio", Source: "prometheus", Query: "cpu{pod=\"$pod\"}"},
		},
	}
}

func podIdentity() Identity {
	return Identity{Namespace: "prod", Pod: "api-abc-123"}
}

// ─── Engine.Run tests ─────────────────────────────────────────────────────────

func TestEngine_Run_Disabled(t *testing.T) {
	e := NewEngine(config.Enrichment{Enabled: false}, nil, nil, nil)
	bundle := e.Run(context.Background(), podIdentity())
	if len(bundle.Results) != 0 {
		t.Errorf("disabled engine should return empty bundle, got %d results", len(bundle.Results))
	}
}

func TestEngine_Run_UnknownIdentity_Empty(t *testing.T) {
	e := NewEngine(config.Enrichment{Enabled: true}, nil, nil, nil)
	// Identity with no pod or service → KindUnknown → empty bundle
	bundle := e.Run(context.Background(), Identity{})
	if len(bundle.Results) != 0 {
		t.Errorf("unknown identity should return empty bundle, got %d results", len(bundle.Results))
	}
}

func TestEngine_Run_PodBundle_Success(t *testing.T) {
	vm := &fakeVMQuerier{samples: []ingestion.Sample{{Labels: map[string]string{"pod": "api-abc"}, Value: 0.75}}}
	e := NewEngine(podEnrichmentCfg(), vm, nil, newFakeCache())

	bundle := e.Run(context.Background(), podIdentity())
	if len(bundle.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(bundle.Results))
	}
	if bundle.Results[0].Name != "cpu_ratio" {
		t.Errorf("result name: want cpu_ratio, got %q", bundle.Results[0].Name)
	}
	if bundle.Results[0].Value != 0.75 {
		t.Errorf("result value: want 0.75, got %v", bundle.Results[0].Value)
	}
	if bundle.Cached {
		t.Error("first run should not be cached")
	}
}

func TestEngine_Run_CacheHit(t *testing.T) {
	vm := &fakeVMQuerier{samples: []ingestion.Sample{{Value: 0.75}}}
	cache := newFakeCache()
	e := NewEngine(podEnrichmentCfg(), vm, nil, cache)
	id := podIdentity()

	// First run — populates cache
	e.Run(context.Background(), id)

	// Second run — should hit cache
	bundle := e.Run(context.Background(), id)
	if !bundle.Cached {
		t.Error("second run should be served from cache")
	}
}

func TestEngine_Run_VMError_ReturnsErrorResult(t *testing.T) {
	vm := &fakeVMQuerier{err: fmt.Errorf("vm timeout")}
	e := NewEngine(podEnrichmentCfg(), vm, nil, newFakeCache())

	bundle := e.Run(context.Background(), podIdentity())
	if len(bundle.Results) != 1 {
		t.Fatalf("expected 1 result even on error, got %d", len(bundle.Results))
	}
	if bundle.Results[0].Error == "" {
		t.Error("result error should be set when VM query fails")
	}
}

func TestEngine_Run_VMEmpty_ReturnsNoDataError(t *testing.T) {
	vm := &fakeVMQuerier{samples: nil}
	e := NewEngine(podEnrichmentCfg(), vm, nil, newFakeCache())

	bundle := e.Run(context.Background(), podIdentity())
	if bundle.Results[0].Error != "no_data" {
		t.Errorf("empty VM result should produce no_data error, got %q", bundle.Results[0].Error)
	}
}

func TestEngine_Run_LokiSource(t *testing.T) {
	cfg := config.Enrichment{
		Enabled:       true,
		MaxConcurrent: 2,
		QueryTimeout:  time.Second,
		CacheTTL:      time.Minute,
		PodBundle: []config.EnrichmentQuery{
			{Name: "error_logs", Source: "loki", Query: "rate({pod=\"$pod\"}|=\"error\"[5m])"},
		},
	}
	loki := &fakeLokiQuerier{samples: []ingestion.Sample{{Value: 3.0}}}
	e := NewEngine(cfg, nil, loki, newFakeCache())

	bundle := e.Run(context.Background(), podIdentity())
	if len(bundle.Results) != 1 {
		t.Fatalf("expected 1 result from loki, got %d", len(bundle.Results))
	}
	if bundle.Results[0].Value != 3.0 {
		t.Errorf("loki result value: want 3.0, got %v", bundle.Results[0].Value)
	}
}

func TestEngine_Run_LokiNil_ReturnsError(t *testing.T) {
	cfg := config.Enrichment{
		Enabled:       true,
		MaxConcurrent: 2,
		QueryTimeout:  time.Second,
		PodBundle: []config.EnrichmentQuery{
			{Name: "error_logs", Source: "loki", Query: "test"},
		},
	}
	e := NewEngine(cfg, nil, nil, newFakeCache()) // loki is nil

	bundle := e.Run(context.Background(), podIdentity())
	if bundle.Results[0].Error != "loki_not_configured" {
		t.Errorf("nil loki should produce loki_not_configured error, got %q", bundle.Results[0].Error)
	}
}

func TestEngine_Run_UnknownSource(t *testing.T) {
	cfg := config.Enrichment{
		Enabled:       true,
		MaxConcurrent: 2,
		QueryTimeout:  time.Second,
		PodBundle: []config.EnrichmentQuery{
			{Name: "thing", Source: "kafka", Query: "test"},
		},
	}
	e := NewEngine(cfg, nil, nil, newFakeCache())

	bundle := e.Run(context.Background(), podIdentity())
	if bundle.Results[0].Error == "" {
		t.Error("unknown source should produce an error")
	}
}

func TestEngine_Run_ServiceBundle(t *testing.T) {
	cfg := config.Enrichment{
		Enabled:       true,
		MaxConcurrent: 2,
		QueryTimeout:  time.Second,
		CacheTTL:      time.Minute,
		ServiceBundle: []config.EnrichmentQuery{
			{Name: "error_rate", Source: "prometheus", Query: "error_rate{service=\"$service_name\"}"},
		},
	}
	vm := &fakeVMQuerier{samples: []ingestion.Sample{{Value: 0.05}}}
	e := NewEngine(cfg, vm, nil, newFakeCache())

	// Service identity (no pod)
	id := Identity{ServiceName: "api-svc"}
	bundle := e.Run(context.Background(), id)
	if len(bundle.Results) != 1 {
		t.Fatalf("expected 1 service bundle result, got %d", len(bundle.Results))
	}
}

func TestEngine_Run_NilCache_StillWorks(t *testing.T) {
	vm := &fakeVMQuerier{samples: []ingestion.Sample{{Value: 0.5}}}
	e := NewEngine(podEnrichmentCfg(), vm, nil, nil) // nil cache

	bundle := e.Run(context.Background(), podIdentity())
	if len(bundle.Results) != 1 {
		t.Errorf("nil cache should not break engine, got %d results", len(bundle.Results))
	}
}

func TestKindLabel(t *testing.T) {
	if kindLabel(KindPod) != "pod" {
		t.Errorf("KindPod: want pod, got %q", kindLabel(KindPod))
	}
	if kindLabel(KindService) != "service" {
		t.Errorf("KindService: want service, got %q", kindLabel(KindService))
	}
	if kindLabel(KindUnknown) != "unknown" {
		t.Errorf("KindUnknown: want unknown, got %q", kindLabel(KindUnknown))
	}
}
