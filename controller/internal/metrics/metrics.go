package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// ReadinessChecker is called by /readyz to verify dependencies.
type ReadinessChecker func(ctx context.Context) error

// Server exposes /metrics, /healthz, and /readyz on the given port.
// metricsHandler is the OTel Prometheus scrape handler (otelhelper.MetricsHandler()),
// wired in from main so this package stays decoupled from the otelhelper library.
type Server struct {
	port     int
	ready    atomic.Bool
	checkers []ReadinessChecker
	srv      *http.Server
}

func NewServer(port int, metricsHandler http.Handler) *Server {
	s := &Server{port: port}

	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	s.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	return s
}

// AddReadinessCheck registers a dependency check for /readyz.
func (s *Server) AddReadinessCheck(c ReadinessChecker) {
	s.checkers = append(s.checkers, c)
}

// SetReady marks the service as ready.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Start runs the HTTP server in a goroutine.
func (s *Server) Start() {
	go func() {
		slog.Info("metrics server starting", "port", s.port)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	for _, check := range s.checkers {
		if err := check(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// =============================================================================
// Metric naming convention: staffops_ad_<component>_<metric>
//
//   staffops_ad_controller_*  — orchestration, leader, dispatch, cycles
//   staffops_ad_worker_*      — query execution, baselines, Redis
//   staffops_ad_detection_*   — anomalies detected (cross-cutting)
//   staffops_ad_alert_*       — alert pipeline (fired, dedup, errors)
//   staffops_ad_ml_*          — ML service ops (Python side)
//
// Instruments are created eagerly at package load, against the global OTel
// meter (otel.Meter) — the same delegating-provider pattern the OTel API
// uses for tracing. This means metrics are safe to record from anywhere
// (including tests that construct a component directly, without going
// through main/otelhelper.Setup) before a real MeterProvider exists: the
// global API buffers instrument creation and retroactively wires it once
// otelhelper.Setup calls otel.SetMeterProvider. Exported through
// otelhelper.MetricsHandler() — no direct client_golang registration.
// Cluster/environment identity is added at the scrape layer (VMServiceScrape/
// ServiceMonitor externalLabels), not emitted by the app.
// =============================================================================

var meter = otel.Meter("github.com/staffops/staffops-anomaly-detection/internal/metrics")

// defBuckets mirrors prometheus.DefBuckets, kept explicit since the OTel
// Histogram API takes bucket boundaries per-instrument, not a package default.
var defBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// --- Controller metrics ---

var (
	DetectionCycles = mustInt64Counter("staffops_ad_controller_cycles_total",
		"Total detection cycles executed by the controller")

	CycleDuration = mustFloat64Histogram("staffops_ad_controller_cycle_duration_seconds",
		"Duration of each detection cycle",
		// Custom buckets: cycles routinely take 1-3s in healthy state, 5-10s
		// when workers struggle, 30s+ on degraded backends. Default buckets
		// (cap at 10s) lose the right-tail signal we need for slow-cycle alerts.
		1, 2.5, 5, 10, 20, 30, 60)

	JobsDispatched = mustInt64Counter("staffops_ad_controller_jobs_dispatched_total",
		"Jobs sent to workers by type")
	JobsFailed = mustInt64Counter("staffops_ad_controller_jobs_failed_total",
		"Jobs that failed during processing")

	WorkersAvailable = mustInt64Gauge("staffops_ad_controller_workers_available",
		// 1 if the gRPC connection to workers is Ready (≥1 backend reachable),
		// 0 otherwise. This is a connectivity flag, not a backend count —
		// gRPC's round_robin balancer doesn't expose per-backend state via
		// public APIs. A true count will come with proper service discovery
		// in P5 (cluster deploy). Setting only on cycle tick is acceptable:
		// gauge value reflects the most recent dispatch attempt.
		"1 if at least one worker is reachable via gRPC, 0 otherwise")

	IsLeader = mustInt64Gauge("staffops_ad_controller_is_leader",
		"1 if this controller instance is the active leader")
	LeaderTransitions = mustInt64Counter("staffops_ad_controller_leader_transitions_total",
		"Number of leader transitions observed")
	BuildInfo = mustInt64Gauge("staffops_ad_controller_build_info",
		"Build metadata (always 1)")

	// Detection (cross-cutting — populated by controller from worker results)
	AnomalyDetected = mustInt64Counter("staffops_ad_detection_anomalies_total",
		"Anomalies detected, by severity and signal")

	// AnomalyByWorkload tracks anomalies sliced by cluster+namespace+workload
	// for dashboards (top noisy workloads, suppression tuning, drift detection).
	//
	// Cardinality: severity(3) × cluster(4, one per monitored K8s cluster) ×
	// namespace(~50) × workload(~20-50/ns) ≈ 12-28k series total — the
	// `cluster` dimension is monitored-workload cardinality (this deployment
	// queries a federated multi-cluster VictoriaMetrics/Loki), not a
	// per-cluster-deployment multiplier, so it does NOT divide across
	// separate Prometheus instances the way `namespace`/`workload` do.
	// Revisit if the steering-mandated 2k/metric limit is measured on this
	// single series (not per label combination).
	//
	// The `workload` label is bounded by deployment count: extracted from pod
	// names via correlation.ExtractWorkload, never raw pod IDs.
	// Service-level anomalies use service_name as workload.
	// Empty cluster/namespace/workload (degenerate cases) are normalized to "unknown".
	AnomalyByWorkload = mustInt64Counter("staffops_ad_detection_anomalies_by_workload_total",
		"Anomalies detected, sliced by cluster, namespace, and workload (bounded labels for dashboards)")
	AnomalyCorrelated = mustInt64Counter("staffops_ad_detection_correlated_total",
		"Anomaly groups produced by the correlator")

	// FDR (Benjamini-Hochberg) metrics
	FDRAccepted = mustInt64Counter("staffops_ad_detection_fdr_accepted_total",
		"Adaptive anomalies accepted after FDR correction")
	FDRRejected = mustInt64Counter("staffops_ad_detection_fdr_rejected_total",
		"Adaptive anomalies rejected by FDR correction")

	WorkloadPatterns = mustInt64Counter("staffops_ad_detection_workload_patterns_total",
		"Workload-level patterns detected (≥N replicas of same workload anomalous)")
	PodAlertsSuppressed = mustInt64Counter("staffops_ad_detection_pod_alerts_suppressed_total",
		"Pod-level alerts suppressed because they belong to a workload-level pattern")

	// Alerts
	AlertsFired = mustInt64Counter("staffops_ad_alert_fired_total",
		"Alerts sent to Alertmanager")
	AlertsDeduplicated = mustInt64Counter("staffops_ad_alert_deduplicated_total",
		"Alerts suppressed by Redis-backed dedup")
	AlertsDispatchErrors = mustInt64Counter("staffops_ad_alert_dispatch_errors_total",
		"Errors sending alerts to Alertmanager")

	// ML client (controller-side)
	MLCalls = mustInt64Counter("staffops_ad_controller_ml_calls_total",
		"ML gRPC calls made by the controller, by method and status")
	MLCallDuration = mustFloat64Histogram("staffops_ad_controller_ml_call_duration_seconds",
		"Duration of ML gRPC calls", .01, .05, .1, .25, .5, 1, 2.5, 5)

	// Enrichment (controller-side label-based pivot)
	EnrichmentRuns = mustInt64Counter("staffops_ad_controller_enrichment_runs_total",
		"Enrichment bundles executed, by identity kind")
	EnrichmentDuration = mustFloat64Histogram("staffops_ad_controller_enrichment_duration_seconds",
		"Duration of full enrichment bundles", .05, .1, .25, .5, 1, 2.5, 5)
	EnrichmentCacheHits = mustInt64Counter("staffops_ad_controller_enrichment_cache_hits_total",
		"Enrichment results served from Redis cache")
	EnrichmentCacheMisses = mustInt64Counter("staffops_ad_controller_enrichment_cache_misses_total",
		"Enrichment cache misses (bundle ran fresh)")
	EnrichmentQueryErrors = mustInt64Counter("staffops_ad_controller_enrichment_query_errors_total",
		"Enrichment query errors, by datasource")

	ReadinessChecksTotal = mustInt64Counter("staffops_ad_controller_readiness_checks_total",
		"Readiness check executions, by dependency and result")
)

// --- Worker metrics ---

var (
	WorkerJobsProcessed = mustInt64Counter("staffops_ad_worker_jobs_processed_total",
		"Jobs processed by this worker, by type and status")
	WorkerJobDuration = mustFloat64Histogram("staffops_ad_worker_job_duration_seconds",
		"Duration of job processing", defBuckets...)
	WorkerQueryDuration = mustFloat64Histogram("staffops_ad_worker_query_duration_seconds",
		"Duration of datasource queries", defBuckets...)
	WorkerQueryErrors = mustInt64Counter("staffops_ad_worker_query_errors_total",
		"Query errors by datasource")

	WorkerDetections = mustInt64Counter("staffops_ad_worker_detections_total",
		"Anomalies detected by detector type (worker-side, pre-suppression)")
	WorkerAnomaliesSuppressed = mustInt64Counter("staffops_ad_worker_anomalies_suppressed_total",
		"Anomalies dropped by the suppression filter, by detector and reason "+
			"(namespace_all, namespace_static, adaptive_workload)")

	WorkerBaselineUpdates = mustInt64Counter("staffops_ad_worker_baseline_updates_total",
		"Baseline updates written to Redis")
	WorkerBaselinePoisonRejected = mustInt64Counter("staffops_ad_worker_baseline_poison_rejected_total",
		"Baseline updates skipped due to anti-poisoning gate (sample too anomalous)")
	WorkerBaselineSeries = mustInt64Gauge("staffops_ad_worker_baseline_series_tracked",
		"Number of series with active baselines")

	WorkerEventsReceived = mustInt64Counter("staffops_ad_worker_events_received_total",
		"K8s events received, by reason")

	WorkerRedisOps = mustInt64Counter("staffops_ad_worker_redis_operations_total",
		"Redis operations issued by workers, by op")
	WorkerRedisDuration = mustFloat64Histogram("staffops_ad_worker_redis_operation_duration_seconds",
		"Redis operation latency", .001, .005, .01, .025, .05, .1, .25)
	WorkerRedisErrors = mustInt64Counter("staffops_ad_worker_redis_errors_total",
		"Redis operation errors")
)

func mustInt64Counter(name, help string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(help))
	if err != nil {
		panic(fmt.Errorf("metrics: create counter %s: %w", name, err))
	}
	return c
}

func mustInt64Gauge(name, help string) metric.Int64Gauge {
	g, err := meter.Int64Gauge(name, metric.WithDescription(help))
	if err != nil {
		panic(fmt.Errorf("metrics: create gauge %s: %w", name, err))
	}
	return g
}

func mustFloat64Histogram(name, help string, buckets ...float64) metric.Float64Histogram {
	h, err := meter.Float64Histogram(name,
		metric.WithDescription(help),
		metric.WithExplicitBucketBoundaries(buckets...))
	if err != nil {
		panic(fmt.Errorf("metrics: create histogram %s: %w", name, err))
	}
	return h
}
