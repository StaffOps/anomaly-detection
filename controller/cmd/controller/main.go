package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/staffops/staffops-anomaly-detection/internal/alert"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	"github.com/staffops/staffops-anomaly-detection/internal/ml"
	"github.com/staffops/staffops-anomaly-detection/internal/readiness"
	"github.com/staffops/staffops-anomaly-detection/internal/version"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
	redisclient "github.com/staffops/staffops-anomaly-detection/internal/redis"
)

func main() {
	configPath := flag.String("config", "config/config.local.yaml", "path to config file")
	dryRun := flag.Bool("dry-run", false, "run detection without firing alerts")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register metrics with `cluster` as a constant label so every time series
	// carries cluster identity (kubernetes-mixin convention). Org-specific
	// labels (e.g. eks_cluster, environment, team) are NOT added here on
	// purpose — they belong at the scrape layer (vmagent externalLabels in
	// production, prometheus.yml external_labels for local dev). This keeps
	// the app generic across organizations. See observability-principles.md.
	constLabels := prometheus.Labels{
		"cluster": cfg.Cluster,
	}
	metrics.MustRegisterController(prometheus.WrapRegistererWith(constLabels, metrics.ControllerRegistry))

	// Redis
	redis, err := redisclient.NewClient(cfg.Redis)
	if err != nil {
		slog.Error("failed to create redis client", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// Metrics server
	metricsSrv := metrics.NewServer(cfg.Controller.MetricsPort, metrics.ControllerRegistry)
	metricsSrv.AddReadinessCheck(redis.ReadinessCheck())
	metricsSrv.Start()

	// Alert dispatcher
	linkBuilder := alert.NewLinkBuilder(cfg.Links)
	dispatcher := alert.NewDispatcher(cfg.Datasources.Alertmanager, *dryRun, cfg.Cluster, linkBuilder)

	// Enrichment engine (label-based pivot for alert context)
	vmPoller := ingestion.NewMetricsPoller(cfg.Datasources.VictoriaMetrics)
	lokiPoller := ingestion.NewLogsPoller(cfg.Datasources.Loki)
	enricher := enrichment.NewEngine(cfg.Enrichment, vmPoller, lokiPoller, redis)

	// Correlator (with enrichment + workload-aware pattern detection)
	correlator := correlation.NewCorrelator(redis, enricher, cfg.Controller.CorrelationWindow, cfg.Controller.Cooldown, cfg.Controller.WorkloadPatternMinPods)

	// gRPC connection to workers
	workerConn, err := grpc.Dial(
		cfg.Controller.WorkerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	)
	if err != nil {
		slog.Error("failed to connect to workers", "error", err)
		os.Exit(1)
	}
	defer workerConn.Close()
	workerClient := pb.NewWorkerServiceClient(workerConn)

	// ML client
	mlClient, err := ml.New(cfg.ML)
	if err != nil {
		slog.Error("failed to connect to ML service", "error", err)
		os.Exit(1)
	}
	defer mlClient.Close()

	// Wire readiness checks for all upstream dependencies.
	metricsSrv.AddReadinessCheck(readiness.VMChecker(cfg.Datasources.VictoriaMetrics))
	metricsSrv.AddReadinessCheck(readiness.LokiChecker(cfg.Datasources.Loki))
	metricsSrv.AddReadinessCheck(readiness.AlertmanagerChecker(cfg.Datasources.Alertmanager))
	metricsSrv.AddReadinessCheck(readiness.MLChecker(mlClient))

	metricsSrv.SetReady(true)
	metrics.IsLeader.Set(1) // TODO: K8s Lease leader election
	metrics.BuildInfo.WithLabelValues(version.Version).Set(1)
	slog.Info("controller started",
		"version", version.Version,
		"metrics_port", cfg.Controller.MetricsPort,
		"worker_endpoint", cfg.Controller.WorkerEndpoint,
		"dry_run", *dryRun,
	)

	// Config hot-reload
	stopCh := make(chan struct{})
	defer close(stopCh)
	cfgWatcher := config.NewWatcher(*configPath, cfg, func(newCfg *config.Config) {
		cfg = newCfg
		slog.Info("detection rules updated", "static_rules", len(cfg.Detection.StaticRules), "adaptive_metrics", len(cfg.Detection.AdaptiveMetrics))
	})
	cfgWatcher.Start(stopCh)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(cfg.Controller.JobInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Update workers_available gauge based on gRPC connection state.
			// Ready/Idle = at least one worker reachable. Connecting/TransientFailure/Shutdown = none.
			// (Idle means no active RPC but the channel is healthy — still reachable.)
			if state := workerConn.GetState(); state == connectivity.Ready || state == connectivity.Idle {
				metrics.WorkersAvailable.Set(1)
			} else {
				metrics.WorkersAvailable.Set(0)
			}
			runCycle(ctx, cfg, workerClient, mlClient, correlator, dispatcher)
		case sig := <-sigCh:
			slog.Info("received signal, shutting down", "signal", sig)
			cancel()
			metricsSrv.Shutdown(ctx)
			slog.Info("controller stopped")
			return
		case <-ctx.Done():
			return
		}
	}
}

func runCycle(ctx context.Context, cfg *config.Config, client pb.WorkerServiceClient, mlClient *ml.Client, correlator *correlation.Correlator, dispatcher *alert.Dispatcher) {
	start := time.Now()

	// Build job batch from config
	batch := buildJobBatch(cfg)
	if len(batch.Jobs) == 0 {
		metrics.DetectionCycles.WithLabelValues("success").Inc()
		metrics.CycleDuration.Observe(time.Since(start).Seconds())
		return
	}

	metrics.JobsDispatched.WithLabelValues("metrics").Add(float64(len(batch.Jobs)))

	// Fan-out to workers via gRPC (round_robin distributes across workers)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results, err := client.ProcessJobs(callCtx, batch)
	if err != nil {
		slog.Error("worker call failed", "error", err)
		metrics.DetectionCycles.WithLabelValues("error").Inc()
		metrics.CycleDuration.Observe(time.Since(start).Seconds())
		return
	}

	// Log errors from workers
	for _, e := range results.Errors {
		slog.Warn("job error from worker", "job_id", e.JobId, "error", e.Error)
		metrics.JobsFailed.WithLabelValues("worker", "error").Inc()
	}

	// Feed anomalies into correlator (ML multivariate runs after correlation+enrichment,
	// so the feature vector includes contextual signals not just the triggering metric).
	for _, a := range results.Anomalies {
		metrics.AnomalyDetected.WithLabelValues(a.Severity, a.Signal).Inc()

		// Log each anomaly for visibility
		slog.Info("anomaly_detected",
			"metric", a.MetricName,
			"namespace", a.Labels["namespace"],
			"pod", a.Labels["pod"],
			"service_name", a.Labels["service_name"],
			"value", a.CurrentValue,
			"baseline_mean", a.BaselineMean,
			"baseline_stddev", a.BaselineStddev,
			"score", a.AnomalyScore,
			"severity", a.Severity,
			"signal", a.Signal,
			"detector", a.Detector,
		)

		correlator.Add(detection.Anomaly{
			MetricName: a.MetricName,
			Labels:     a.Labels,
			Value:      a.CurrentValue,
			Mean:       a.BaselineMean,
			Stddev:     a.BaselineStddev,
			Score:      a.AnomalyScore,
			Severity:   a.Severity,
			Signal:     a.Signal,
			Detector:   a.Detector,
			Timestamp:  time.Unix(a.Timestamp, 0),
		})
	}

	// Flush correlated alerts. Each correlated alert carries its enrichment bundle;
	// we use that to build a stable feature vector for ML multivariate detection.
	alerts := correlator.Flush(ctx)
	for i := range alerts {
		ca := &alerts[i]
		if mlClient.Enabled() && len(ca.Anomalies) > 0 {
			rep := ca.Anomalies[0]
			features := ml.BuildFeatureVector(rep, ca.Enrichment)
			if det, err := mlClient.DetectFromFeatures(ctx, features); err != nil {
				slog.Warn("ml detect failed", "error", err)
			} else if det != nil {
				ca.MLDetection = &correlation.MLDetection{
					IsAnomaly:    det.IsAnomaly,
					Score:        det.Score,
					Contributors: det.Contributors,
					FeatureCount: det.FeatureCount,
				}
				if det.IsAnomaly {
					slog.Info("ml_multivariate_confirmed",
						"namespace", ca.Namespace,
						"workload", ca.Workload,
						"score", det.Score,
						"contributors", det.Contributors,
						"features", det.FeatureCount,
					)
					// Escalate severity when ML confirms — multi-signal critical
					if ca.Severity == "warning" {
						ca.Severity = "critical"
					}
				}
			}
		}

		if err := dispatcher.FireCorrelated(ctx, *ca); err != nil {
			slog.Error("failed to fire alert", "error", err)
		}
	}

	metrics.DetectionCycles.WithLabelValues("success").Inc()
	metrics.CycleDuration.Observe(time.Since(start).Seconds())

	if len(results.Anomalies) > 0 {
		slog.Info("cycle complete",
			"duration", time.Since(start),
			"anomalies", len(results.Anomalies),
			"alerts_fired", len(alerts),
		)
	}
}

func buildJobBatch(cfg *config.Config) *pb.JobBatch {
	batch := &pb.JobBatch{}

	// Static rules
	for _, rule := range cfg.Detection.StaticRules {
		batch.Jobs = append(batch.Jobs, &pb.DetectionJob{
			Id:        uuid.NewString(),
			Type:      pb.JobType_JOB_TYPE_METRICS_STATIC,
			Name:      rule.Name,
			Query:     rule.Query,
			Threshold: rule.Threshold,
			Operator:  rule.Operator,
			Severity:  rule.Severity,
		})
	}

	// Adaptive metrics
	for _, m := range cfg.Detection.AdaptiveMetrics {
		batch.Jobs = append(batch.Jobs, &pb.DetectionJob{
			Id:      uuid.NewString(),
			Type:    pb.JobType_JOB_TYPE_METRICS_ADAPTIVE,
			Name:    m.Name,
			Query:   m.Query,
			GroupBy: m.GroupBy,
		})
	}

	// Log patterns (rate-based)
	for _, lp := range cfg.Detection.LogPatterns {
		if lp.Type == "pattern_match" {
			continue // skip non-metric log patterns for now
		}
		batch.Jobs = append(batch.Jobs, &pb.DetectionJob{
			Id:      uuid.NewString(),
			Type:    pb.JobType_JOB_TYPE_LOGS,
			Name:    lp.Name,
			Query:   lp.Query,
			GroupBy: lp.GroupBy,
		})
	}

	return batch
}
