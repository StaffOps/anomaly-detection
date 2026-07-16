package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	otelhelper "github.com/staffops/staffops-otel-libs/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/staffops/staffops-anomaly-detection/internal/alert"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/leader"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	"github.com/staffops/staffops-anomaly-detection/internal/ml"
	"github.com/staffops/staffops-anomaly-detection/internal/readiness"
	redisclient "github.com/staffops/staffops-anomaly-detection/internal/redis"
	"github.com/staffops/staffops-anomaly-detection/internal/replay"
	"github.com/staffops/staffops-anomaly-detection/internal/replay/inject"
	"github.com/staffops/staffops-anomaly-detection/internal/version"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

func main() {
	configPath := flag.String("config", "config/config.local.yaml", "path to config file")
	dryRun := flag.Bool("dry-run", false, "run detection without firing alerts")

	// Replay mode flags.
	replayMode := flag.Bool("replay", false, "run in replay mode (offline analysis, no side effects). ML is V2 — not supported yet.")
	replayFrom := flag.String("from", "", "replay window start (duration like 24h or RFC3339 timestamp)")
	replayTo := flag.String("to", "", "replay window end (duration like 1h or RFC3339; default: now)")
	replayOutput := flag.String("output", "./replay-report.json", "output path for replay report (writes .json and .md)")
	replayWarmup := flag.Float64("warmup-fraction", 0.2, "fraction of window used for baseline warmup")
	replayMaxRange := flag.String("max-range", "7d", "maximum allowed replay window duration")
	replayMaxAnomalies := flag.Int("max-anomalies", 1000, "maximum anomalies in report output")
	replayInject := flag.String("inject", "", "path to injection profile YAML for synthetic fault injection scoring (empty or 'none' = no injection)")

	flag.Parse()

	// --- REPLAY MODE: short-circuit before any prod infrastructure ---
	if *replayMode {
		runReplay(*configPath, *replayFrom, *replayTo, *replayOutput, *replayWarmup, *replayMaxRange, *replayMaxAnomalies, *replayInject)
		return
	}

	// OTel SDK: traces + logs via OTLP when a collector is configured; metrics
	// always route through the lib's Prometheus reader (otelhelper.MetricsHandler,
	// mounted below). Without OTEL_EXPORTER_OTLP_ENDPOINT, the lib itself falls
	// back to in-process traces + stdout logs (see otel-libs HOW-TO.md #13) —
	// Setup() is always called, no manual endpoint branching needed.
	otelShutdown, otelErr := otelhelper.Setup(context.Background(),
		otelhelper.WithServiceName("staffops-ad-controller"),
		otelhelper.WithMetricExporters("prometheus"),
		otelhelper.WithoutMetricsListener(), // mounted on our own mux instead
	)
	if otelErr != nil {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
		slog.Warn("otel setup failed, using plain logger", "error", otelErr)
	} else {
		defer otelShutdown(context.Background())
		slog.SetDefault(otelhelper.NewLogger(otelhelper.LOCAL, os.Getenv("OTEL_HELPER_DEBUG_LEVEL") == "true"))
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Redis
	redis, err := redisclient.NewClient(cfg.Redis)
	if err != nil {
		slog.Error("failed to create redis client", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// Metrics server
	metricsSrv := metrics.NewServer(cfg.Controller.MetricsPort, otelhelper.MetricsHandler())
	metricsSrv.AddReadinessCheck(redis.ReadinessCheck())
	metricsSrv.Start()

	// Alert dispatcher
	linkBuilder := alert.NewLinkBuilder(cfg.Links)
	dispatcher := alert.NewDispatcher(cfg.Datasources.Alertmanager, *dryRun, cfg.Cluster, linkBuilder)

	// Enrichment engine (label-based pivot for alert context)
	vmPoller := ingestion.NewMetricsPoller(cfg.Datasources.Prometheus)
	lokiPoller := ingestion.NewLogsPoller(cfg.Datasources.Loki)
	enricher := enrichment.NewEngine(cfg.Enrichment, vmPoller, lokiPoller, redis)

	// Correlator (with enrichment + workload-aware pattern detection)
	correlator := correlation.NewCorrelator(redis, enricher, cfg.Controller.CorrelationWindow, cfg.Controller.Cooldown, cfg.Controller.WorkloadPatternMinPods)

	// gRPC connection to workers
	workerConn, err := grpc.Dial(
		cfg.Controller.WorkerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithUnaryInterceptor(otelhelper.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelhelper.StreamClientInterceptor()),
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
	metricsSrv.AddReadinessCheck(readiness.PromChecker(cfg.Datasources.Prometheus))
	metricsSrv.AddReadinessCheck(readiness.LokiChecker(cfg.Datasources.Loki))
	metricsSrv.AddReadinessCheck(readiness.AlertmanagerChecker(cfg.Datasources.Alertmanager))
	metricsSrv.AddReadinessCheck(readiness.MLChecker(mlClient))

	metricsSrv.SetReady(true)
	metrics.BuildInfo.Record(ctx, 1, metric.WithAttributes(attribute.String("version", version.Version)))
	slog.Info("controller started",
		"version", version.Version,
		"metrics_port", cfg.Controller.MetricsPort,
		"worker_endpoint", cfg.Controller.WorkerEndpoint,
		"dry_run", *dryRun,
		"leader_election", cfg.Controller.LeaderElection.Enabled,
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

	// runCycleLoop runs the detection cycle ticker until its context is cancelled.
	// Extracted as a closure so leader election (if enabled) can gate it: the
	// callback receives a leaderCtx that's cancelled on lease loss.
	runCycleLoop := func(loopCtx context.Context) {
		ticker := time.NewTicker(cfg.Controller.JobInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Update workers_available gauge based on gRPC connection state.
				if state := workerConn.GetState(); state == connectivity.Ready || state == connectivity.Idle {
					metrics.WorkersAvailable.Record(loopCtx, 1)
				} else {
					metrics.WorkersAvailable.Record(loopCtx, 0)
				}
				runCycle(loopCtx, cfg, workerClient, mlClient, correlator, dispatcher)
			case <-loopCtx.Done():
				return
			}
		}
	}

	// Wire shutdown signal into ctx so leader election + cycle both stop cleanly.
	go func() {
		sig := <-sigCh
		slog.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	if cfg.Controller.LeaderElection.Enabled {
		// HA mode: only the leader runs detection cycles. Followers wait.
		// On lease loss, leaderCtx is cancelled and the cycle loop exits;
		// leaderelection.RunOrDie keeps trying to re-acquire.
		err := leader.Run(ctx, leader.Config{
			Namespace:     cfg.Controller.LeaseNamespace,
			Name:          cfg.Controller.LeaseName,
			Identity:      cfg.Controller.LeaderElection.Identity,
			LeaseDuration: cfg.Controller.LeaderElection.LeaseDuration,
			RenewDeadline: cfg.Controller.LeaderElection.RenewDeadline,
			RetryPeriod:   cfg.Controller.LeaderElection.RetryPeriod,
			Kubeconfig:    cfg.Kubeconfig,
		}, leader.Callbacks{
			OnStartedLeading: runCycleLoop,
			OnStoppedLeading: func() {
				// runCycleLoop's ctx is already cancelled by the leaderelection
				// machinery — no extra action needed. Followers don't run cycles.
			},
		})
		if err != nil {
			slog.Error("leader election failed", "error", err)
			os.Exit(1)
		}
	} else {
		// Single-replica mode (local docker-compose, dev): act as if always leader.
		metrics.IsLeader.Record(ctx, 1)
		runCycleLoop(ctx)
	}

	metricsSrv.Shutdown(ctx)
	slog.Info("controller stopped")
}

func runCycle(ctx context.Context, cfg *config.Config, client pb.WorkerServiceClient, mlClient *ml.Client, correlator *correlation.Correlator, dispatcher *alert.Dispatcher) {
	start := time.Now()

	// Build job batch from config
	batch := buildJobBatch(cfg)
	if len(batch.Jobs) == 0 {
		metrics.DetectionCycles.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "success")))
		metrics.CycleDuration.Record(ctx, time.Since(start).Seconds())
		return
	}

	metrics.JobsDispatched.Add(ctx, int64(len(batch.Jobs)), metric.WithAttributes(attribute.String("type", "metrics")))

	// Fan-out to workers via gRPC (round_robin distributes across workers)
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	results, err := client.ProcessJobs(callCtx, batch)
	if err != nil {
		slog.Error("worker call failed", "error", err)
		metrics.DetectionCycles.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "error")))
		metrics.CycleDuration.Record(ctx, time.Since(start).Seconds())
		return
	}

	// Log errors from workers
	for _, e := range results.Errors {
		slog.Warn("job error from worker", "job_id", e.JobId, "error", e.Error)
		metrics.JobsFailed.Add(ctx, 1, metric.WithAttributes(
			attribute.String("worker", "worker"), attribute.String("reason", "error")))
	}

	// Feed anomalies into correlator (ML multivariate runs after correlation+enrichment,
	// so the feature vector includes contextual signals not just the triggering metric).
	//
	// FDR (Benjamini-Hochberg): filter adaptive anomalies to control false discovery
	// rate from multiple comparisons (~400 series at z>3 ≈ ~1000+ FP/day without).
	fdr := detection.NewFDR(cfg.Controller.FDRTarget)
	var allAnomalies []*detection.Anomaly
	for _, a := range results.Anomalies {
		allAnomalies = append(allAnomalies, &detection.Anomaly{
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

	accepted, rejected := fdr.Apply(allAnomalies)
	metrics.FDRAccepted.Add(ctx, int64(len(accepted)))
	metrics.FDRRejected.Add(ctx, int64(rejected))
	if rejected > 0 {
		slog.Info("fdr_applied", "accepted", len(accepted), "rejected", rejected, "target", cfg.Controller.FDRTarget)
	}

	for _, a := range accepted {
		metrics.AnomalyDetected.Add(ctx, 1, metric.WithAttributes(
			attribute.String("severity", a.Severity), attribute.String("signal", a.Signal)))

		// AnomalyByWorkload: bounded labels for dashboards (top noisy workloads).
		// Namespace resolution: try namespace → deployment_environment → service_namespace.
		// For span metrics that lack k8s namespace, service_name is the best identifier.
		ns := a.Labels["namespace"]
		if ns == "" {
			ns = a.Labels["deployment_environment"]
		}
		if ns == "" {
			ns = a.Labels["service_namespace"]
		}
		if ns == "" {
			ns = a.Labels["destination_workload_namespace"]
		}
		if ns == "" {
			ns = "unknown"
		}
		workload := "unknown"
		if pod := a.Labels["pod"]; pod != "" {
			if w := correlation.ExtractWorkload(pod); w != "" {
				workload = w
			} else {
				workload = pod // bounded fallback for unparseable pod names
			}
		} else if svc := a.Labels["service_name"]; svc != "" {
			workload = svc
		}
		metrics.AnomalyByWorkload.Add(ctx, 1, metric.WithAttributes(
			attribute.String("namespace", ns), attribute.String("workload", workload), attribute.String("severity", a.Severity)))

		// Log each anomaly for visibility
		slog.Info("anomaly_detected",
			"metric", a.MetricName,
			"namespace", a.Labels["namespace"],
			"pod", a.Labels["pod"],
			"service_name", a.Labels["service_name"],
			"value", a.Value,
			"baseline_mean", a.Mean,
			"baseline_stddev", a.Stddev,
			"score", a.Score,
			"severity", a.Severity,
			"signal", a.Signal,
			"detector", a.Detector,
		)

		correlator.Add(detection.Anomaly{
			MetricName: a.MetricName,
			Labels:     a.Labels,
			Value:      a.Value,
			Mean:       a.Mean,
			Stddev:     a.Stddev,
			Score:      a.Score,
			Severity:   a.Severity,
			Signal:     a.Signal,
			Detector:   a.Detector,
			Timestamp:  a.Timestamp,
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

	metrics.DetectionCycles.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "success")))
	metrics.CycleDuration.Record(ctx, time.Since(start).Seconds())

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

// runReplay handles the --replay mode: parse window, pre-flight checks, run engine, write output.
func runReplay(configPath, fromStr, toStr, outputPath string, warmupFraction float64, maxRangeStr string, maxAnomalies int, injectPath string) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Parse max-range duration.
	maxRange, err := parseDuration(maxRangeStr)
	if err != nil {
		slog.Error("invalid --max-range", "value", maxRangeStr, "error", err)
		os.Exit(1)
	}

	// Parse window.
	from, to, err := replay.ParseWindow(fromStr, toStr, maxRange)
	if err != nil {
		slog.Error("invalid replay window", "error", err)
		os.Exit(1)
	}

	// Load config.
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Load injection profile (if specified).
	var injector *inject.Injector
	switch injectPath {
	case "":
		// No flag → normal replay, no scoring block.
	case "none":
		// US-5: FP upper-bound baseline over a clean window. An empty injector
		// records zero ground truths, so the scorer classifies every detection
		// as a false positive — the detector's base noise on clean data.
		injector = inject.NewInjector(&inject.InjectionConfig{})
		slog.Info("[REPLAY] injection=none — FP upper-bound baseline (all detections scored as FP)")
	default:
		injCfg, err := inject.LoadConfig(injectPath)
		if err != nil {
			slog.Error("failed to load injection profile", "path", injectPath, "error", err)
			os.Exit(1)
		}
		injector = inject.NewInjector(injCfg)
		slog.Info("[REPLAY] injection active", "profile", injectPath, "seed", injCfg.Seed, "injections", len(injCfg.Injections))
	}

	// Banner.
	warmupDur := time.Duration(float64(to.Sub(from)) * warmupFraction)
	minWarmup := time.Duration(cfg.Baseline.WarmUpSamples) * cfg.Controller.JobInterval
	if minWarmup > warmupDur {
		warmupDur = minWarmup
	}
	warmupEnd := from.Add(warmupDur)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║         REPLAY MODE — no side effects               ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Printf("  Window:     %s → %s\n", from.Format(time.RFC3339), to.Format(time.RFC3339))
	fmt.Printf("  Warmup end: %s\n", warmupEnd.Format(time.RFC3339))
	fmt.Printf("  Tick:       %s\n", cfg.Controller.JobInterval)
	fmt.Printf("  Config:     %s\n", configPath)
	fmt.Printf("  Output:     %s (.json + .md)\n", outputPath)
	fmt.Println()

	// Pre-flight checks.
	if err := preflightProm(cfg.Datasources.Prometheus); err != nil {
		slog.Error("pre-flight: Prometheus-compatible TSDB unreachable", "error", err)
		os.Exit(1)
	}
	if err := preflightLoki(cfg.Datasources.Loki); err != nil {
		slog.Error("pre-flight: Loki unreachable", "error", err)
		os.Exit(1)
	}
	if err := preflightOutput(outputPath); err != nil {
		slog.Error("pre-flight: output path not writable", "error", err)
		os.Exit(1)
	}

	// Build replay config and run.
	rcfg := replay.ReplayConfig{
		From:           from,
		To:             to,
		ConfigPath:     configPath,
		OutputPath:     outputPath,
		WarmupFraction: warmupFraction,
		MaxAnomalies:   maxAnomalies,
	}

	ctx := context.Background()
	report, err := replay.Run(ctx, rcfg, cfg, injector)
	if err != nil {
		slog.Error("replay failed", "error", err)
		os.Exit(1)
	}

	// Write JSON.
	jsonPath := outputPath
	if !strings.HasSuffix(jsonPath, ".json") {
		jsonPath += ".json"
	}
	jf, err := os.Create(jsonPath)
	if err != nil {
		slog.Error("failed to create JSON output", "error", err)
		os.Exit(1)
	}
	if err := report.WriteJSON(jf); err != nil {
		jf.Close()
		slog.Error("failed to write JSON", "error", err)
		os.Exit(1)
	}
	jf.Close()

	// Write Markdown.
	mdPath := strings.TrimSuffix(jsonPath, ".json") + ".md"
	mf, err := os.Create(mdPath)
	if err != nil {
		slog.Error("failed to create MD output", "error", err)
		os.Exit(1)
	}
	if err := report.WriteMarkdown(mf); err != nil {
		mf.Close()
		slog.Error("failed to write Markdown", "error", err)
		os.Exit(1)
	}
	mf.Close()

	slog.Info("[REPLAY] complete",
		"json", jsonPath,
		"md", mdPath,
		"anomalies", report.Totals.Anomalies,
		"status", report.Metadata.ResultStatus,
		"duration", fmt.Sprintf("%.1fs", report.Metadata.ExecutionMetrics.DurationSeconds),
	)
}

// parseDuration parses durations like "7d", "24h", "30m".
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(s, "%d", &days); err != nil {
			return 0, fmt.Errorf("invalid day duration: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// preflightProm checks the Prometheus-compatible TSDB is reachable with query=up.
func preflightProm(ds config.DatasourceEndpoint) error {
	url := strings.TrimRight(ds.URL, "/") + "/api/v1/query?query=up"
	client := &http.Client{Timeout: ds.Timeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("prometheus query failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus returned status %d", resp.StatusCode)
	}
	return nil
}

// preflightLoki checks Loki is reachable via /loki/api/v1/labels.
func preflightLoki(ds config.DatasourceEndpoint) error {
	url := strings.TrimRight(ds.URL, "/") + "/loki/api/v1/labels"
	client := &http.Client{Timeout: ds.Timeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("Loki labels failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Loki returned status %d", resp.StatusCode)
	}
	return nil
}

// preflightOutput checks the output path is writable.
func preflightOutput(path string) error {
	f, err := os.Create(path + ".preflight")
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(path + ".preflight")
	return nil
}
