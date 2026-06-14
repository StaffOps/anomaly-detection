package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ReadinessChecker is called by /readyz to verify dependencies.
type ReadinessChecker func(ctx context.Context) error

// Server exposes /metrics, /healthz, and /readyz on the given port.
type Server struct {
	port     int
	ready    atomic.Bool
	checkers []ReadinessChecker
	srv      *http.Server
}

func NewServer(port int, reg *prometheus.Registry) *Server {
	s := &Server{port: port}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
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
// =============================================================================

// --- Controller metrics ---

var ControllerRegistry = prometheus.NewRegistry()

var (
	// Controller lifecycle
	DetectionCycles = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_cycles_total",
		Help: "Total detection cycles executed by the controller",
	}, []string{"status"})

	CycleDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "staffops_ad_controller_cycle_duration_seconds",
		Help: "Duration of each detection cycle",
		// Custom buckets: cycles routinely take 1-3s in healthy state, 5-10s
		// when workers struggle, 30s+ on degraded backends. Default buckets
		// (cap at 10s) lose the right-tail signal we need for slow-cycle alerts.
		Buckets: []float64{1, 2.5, 5, 10, 20, 30, 60},
	})

	JobsDispatched = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_jobs_dispatched_total",
		Help: "Jobs sent to workers by type",
	}, []string{"type"})

	JobsFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_jobs_failed_total",
		Help: "Jobs that failed during processing",
	}, []string{"worker", "reason"})

	WorkersAvailable = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staffops_ad_controller_workers_available",
		// 1 if the gRPC connection to workers is Ready (≥1 backend reachable),
		// 0 otherwise. This is a connectivity flag, not a backend count —
		// gRPC's round_robin balancer doesn't expose per-backend state via
		// public APIs. A true count will come with proper service discovery
		// in P5 (cluster deploy). Setting only on cycle tick is acceptable:
		// gauge value reflects the most recent dispatch attempt.
		Help: "1 if at least one worker is reachable via gRPC, 0 otherwise",
	})

	IsLeader = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staffops_ad_controller_is_leader",
		Help: "1 if this controller instance is the active leader",
	})

	LeaderTransitions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_controller_leader_transitions_total",
		Help: "Number of leader transitions observed",
	})

	BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "staffops_ad_controller_build_info",
		Help: "Build metadata (always 1)",
	}, []string{"version"})

	// Detection (cross-cutting — populated by controller from worker results)
	AnomalyDetected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_detection_anomalies_total",
		Help: "Anomalies detected, by severity and signal",
	}, []string{"severity", "signal"})

	// AnomalyByWorkload tracks anomalies sliced by namespace+workload for
	// dashboards (top noisy workloads, suppression tuning, drift detection).
	//
	// Cardinality: severity(3) × namespace(~50) × workload(~20-50/ns) ≈ 3-7k
	// series per cluster — well below the steering-mandated 2k/metric limit
	// applied per cluster (constLabels add `cluster` so multi-cluster series
	// scale linearly).
	//
	// The `workload` label is bounded by deployment count: extracted from pod
	// names via correlation.ExtractWorkload, never raw pod IDs.
	// Service-level anomalies use service_name as workload.
	// Empty namespace/workload (degenerate cases) are normalized to "unknown".
	AnomalyByWorkload = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_detection_anomalies_by_workload_total",
		Help: "Anomalies detected, sliced by namespace and workload (bounded labels for dashboards)",
	}, []string{"namespace", "workload", "severity"})

	AnomalyCorrelated = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_detection_correlated_total",
		Help: "Anomaly groups produced by the correlator",
	}, []string{"severity"})

	WorkloadPatterns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_detection_workload_patterns_total",
		Help: "Workload-level patterns detected (≥N replicas of same workload anomalous)",
	}, []string{"severity"})

	PodAlertsSuppressed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_detection_pod_alerts_suppressed_total",
		Help: "Pod-level alerts suppressed because they belong to a workload-level pattern",
	})

	// Alerts
	AlertsFired = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_alert_fired_total",
		Help: "Alerts sent to Alertmanager",
	}, []string{"severity"})

	AlertsDeduplicated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_alert_deduplicated_total",
		Help: "Alerts suppressed by Redis-backed dedup",
	})

	AlertsDispatchErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_alert_dispatch_errors_total",
		Help: "Errors sending alerts to Alertmanager",
	})

	// ML client (controller-side)
	MLCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_ml_calls_total",
		Help: "ML gRPC calls made by the controller, by method and status",
	}, []string{"method", "status"})

	MLCallDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "staffops_ad_controller_ml_call_duration_seconds",
		Help:    "Duration of ML gRPC calls",
		Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method"})

	// Enrichment (controller-side label-based pivot)
	EnrichmentRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_enrichment_runs_total",
		Help: "Enrichment bundles executed, by identity kind",
	}, []string{"kind"})

	EnrichmentDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "staffops_ad_controller_enrichment_duration_seconds",
		Help:    "Duration of full enrichment bundles",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"kind"})

	EnrichmentCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_controller_enrichment_cache_hits_total",
		Help: "Enrichment results served from Redis cache",
	})

	EnrichmentCacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_controller_enrichment_cache_misses_total",
		Help: "Enrichment cache misses (bundle ran fresh)",
	})

	EnrichmentQueryErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_enrichment_query_errors_total",
		Help: "Enrichment query errors, by datasource",
	}, []string{"source"})

	// Readiness probes for upstream dependencies
	ReadinessChecksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_controller_readiness_checks_total",
		Help: "Readiness check executions, by dependency and result",
	}, []string{"dependency", "result"})
)

// --- Worker metrics ---

var WorkerRegistry = prometheus.NewRegistry()

var (
	WorkerJobsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_worker_jobs_processed_total",
		Help: "Jobs processed by this worker, by type and status",
	}, []string{"type", "status"})

	WorkerJobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "staffops_ad_worker_job_duration_seconds",
		Help:    "Duration of job processing",
		Buckets: prometheus.DefBuckets,
	}, []string{"type"})

	WorkerQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "staffops_ad_worker_query_duration_seconds",
		Help:    "Duration of datasource queries",
		Buckets: prometheus.DefBuckets,
	}, []string{"datasource"})

	WorkerQueryErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_worker_query_errors_total",
		Help: "Query errors by datasource",
	}, []string{"datasource"})

	WorkerDetections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_worker_detections_total",
		Help: "Anomalies detected by detector type (worker-side)",
	}, []string{"detector"})

	WorkerBaselineUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_worker_baseline_updates_total",
		Help: "Baseline updates written to Redis",
	})

	WorkerBaselineSeries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staffops_ad_worker_baseline_series_tracked",
		Help: "Number of series with active baselines",
	})

	WorkerEventsReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_worker_events_received_total",
		Help: "K8s events received, by reason",
	}, []string{"reason"})

	WorkerRedisOps = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staffops_ad_worker_redis_operations_total",
		Help: "Redis operations issued by workers, by op",
	}, []string{"op"})

	WorkerRedisDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "staffops_ad_worker_redis_operation_duration_seconds",
		Help:    "Redis operation latency",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25},
	})

	WorkerRedisErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "staffops_ad_worker_redis_errors_total",
		Help: "Redis operation errors",
	})
)

func init() {
	// Metrics are intentionally NOT registered in init(). Registration happens
	// in MustRegisterController / MustRegisterWorker, called from main with a
	// registerer that wraps the registry with constant labels (cluster +
	// eks_cluster). This guarantees every metric carries the cluster identity
	// without per-call boilerplate, per multicluster-label-strategy steering.
}

// MustRegisterController registers all controller-side metrics on reg.
//
// Pass `prometheus.WrapRegistererWith(constLabels, ControllerRegistry)` from
// main so that constant labels (`cluster`, `eks_cluster`) are added to every
// time series. ControllerRegistry itself is the actual storage, used by the
// /metrics HTTP handler.
//
// Panics if registration fails (programmer error: duplicate registration).
func MustRegisterController(reg prometheus.Registerer) {
	reg.MustRegister(
		DetectionCycles, CycleDuration,
		JobsDispatched, JobsFailed,
		WorkersAvailable, IsLeader, LeaderTransitions, BuildInfo,
		AnomalyDetected, AnomalyCorrelated,
		AnomalyByWorkload,
		WorkloadPatterns, PodAlertsSuppressed,
		AlertsFired, AlertsDeduplicated, AlertsDispatchErrors,
		MLCalls, MLCallDuration,
		EnrichmentRuns, EnrichmentDuration,
		EnrichmentCacheHits, EnrichmentCacheMisses, EnrichmentQueryErrors,
		ReadinessChecksTotal,
	)
}

// MustRegisterWorker registers all worker-side metrics on reg.
// See MustRegisterController for usage notes — same wrapping pattern applies.
func MustRegisterWorker(reg prometheus.Registerer) {
	reg.MustRegister(
		WorkerJobsProcessed, WorkerJobDuration,
		WorkerQueryDuration, WorkerQueryErrors,
		WorkerDetections, WorkerBaselineUpdates, WorkerBaselineSeries,
		WorkerEventsReceived,
		WorkerRedisOps, WorkerRedisDuration, WorkerRedisErrors,
		BuildInfo,
	)
}
