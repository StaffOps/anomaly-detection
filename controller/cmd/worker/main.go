package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	otelhelper "github.com/staffops/staffops-otel-libs/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/staffops/staffops-anomaly-detection/internal/baseline"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	"github.com/staffops/staffops-anomaly-detection/internal/ratelimit"
	redisclient "github.com/staffops/staffops-anomaly-detection/internal/redis"
	"github.com/staffops/staffops-anomaly-detection/internal/suppression"
	"github.com/staffops/staffops-anomaly-detection/internal/version"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

func main() {
	configPath := flag.String("config", "config/config.local.yaml", "path to config file")
	flag.Parse()

	// OTel SDK: traces + logs via OTLP when a collector is configured; metrics
	// always route through the lib's Prometheus reader (otelhelper.MetricsHandler,
	// mounted below). Without OTEL_EXPORTER_OTLP_ENDPOINT, the lib itself falls
	// back to in-process traces + stdout logs (see otel-libs HOW-TO.md #13).
	otelShutdown, otelErr := otelhelper.Setup(context.Background(),
		otelhelper.WithServiceName("staffops-ad-worker"),
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
	metricsSrv := metrics.NewServer(cfg.Worker.MetricsPort, otelhelper.MetricsHandler())
	metricsSrv.AddReadinessCheck(redis.ReadinessCheck())
	metricsSrv.Start()

	// Baseline store
	store := baseline.NewStore(redis, cfg.Baseline)
	seasonal := baseline.NewSeasonalProfile(redis, cfg.Baseline)

	// Detection engine
	engine := detection.NewEngine(cfg.Detection, store)

	// Suppression filter
	suppressionFilter := suppression.NewFilter(cfg.Suppression)

	// Rate limiters
	vmLimiter := ratelimit.New(20)   // 20 queries/s to Prometheus (conservative to avoid overloading vmselect)
	lokiLimiter := ratelimit.New(50) // 50 queries/s to Loki

	// Ingestion
	metricsPoller := ingestion.NewMetricsPoller(cfg.Datasources.Prometheus)
	logsPoller := ingestion.NewLogsPoller(cfg.Datasources.Loki)

	// K8s client (optional, for event watcher)
	var k8sClient kubernetes.Interface
	if cfg.Kubeconfig != "" {
		restCfg, err := clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
		if err != nil {
			slog.Warn("kubeconfig not available, event watcher disabled", "error", err)
		} else {
			k8sClient, err = kubernetes.NewForConfig(restCfg)
			if err != nil {
				slog.Warn("k8s client failed, event watcher disabled", "error", err)
			}
		}
	}

	// gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Worker.GRPCPort))
	if err != nil {
		slog.Error("failed to listen", "port", cfg.Worker.GRPCPort, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(otelhelper.UnaryServerInterceptor()),
		grpc.StreamInterceptor(otelhelper.StreamServerInterceptor()),
	)
	srv := &workerServer{
		cfg:           cfg,
		engine:        engine,
		seasonal:      seasonal,
		suppression:   suppressionFilter,
		vmLimiter:     vmLimiter,
		lokiLimiter:   lokiLimiter,
		metricsPoller: metricsPoller,
		logsPoller:    logsPoller,
		k8sClient:     k8sClient,
	}
	pb.RegisterWorkerServiceServer(grpcServer, srv)

	metricsSrv.SetReady(true)
	metrics.BuildInfo.Record(ctx, 1, metric.WithAttributes(attribute.String("version", version.Version)))
	slog.Info("worker started", "version", version.Version, "grpc_port", cfg.Worker.GRPCPort, "metrics_port", cfg.Worker.MetricsPort)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("grpc server error", "error", err)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		slog.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	grpcServer.GracefulStop()
	metricsSrv.Shutdown(ctx)
	cancel()
	slog.Info("worker stopped")
}

// workerServer implements pb.WorkerServiceServer.
type workerServer struct {
	pb.UnimplementedWorkerServiceServer
	cfg           *config.Config
	engine        *detection.Engine
	seasonal      *baseline.SeasonalProfile
	suppression   *suppression.Filter
	vmLimiter     *ratelimit.Limiter
	lokiLimiter   *ratelimit.Limiter
	metricsPoller *ingestion.MetricsPoller
	logsPoller    *ingestion.LogsPoller
	k8sClient     kubernetes.Interface
}

// ProcessJobs handles a batch of detection jobs from the controller.
func (s *workerServer) ProcessJobs(ctx context.Context, batch *pb.JobBatch) (*pb.JobResults, error) {
	results := &pb.JobResults{}
	queryCache := make(map[string][]ingestion.Sample) // dedup identical queries within a batch

	for _, job := range batch.Jobs {
		start := time.Now()

		anomalies, tested, err := s.processJob(ctx, job, queryCache)
		results.AdaptiveSeriesTested += int64(tested)
		if err != nil {
			slog.Warn("job failed", "job_id", job.Id, "name", job.Name, "error", err)
			results.Errors = append(results.Errors, &pb.JobError{
				JobId: job.Id,
				Error: err.Error(),
			})
			metrics.WorkerJobsProcessed.Add(ctx, 1, metric.WithAttributes(
				attribute.String("type", job.Type.String()), attribute.String("status", "error")))
			continue
		}

		// Apply suppression filter, counting drops by detector + reason so the
		// effect is observable (staffops_ad_worker_anomalies_suppressed_total).
		kept := anomalies[:0]
		for _, a := range anomalies {
			if reason := s.suppression.SuppressReason(a); reason != "" {
				metrics.WorkerAnomaliesSuppressed.Add(ctx, 1, metric.WithAttributes(
					attribute.String("detector", a.Detector), attribute.String("reason", reason)))
				continue
			}
			kept = append(kept, a)
		}
		anomalies = kept

		for _, a := range anomalies {
			// Update seasonal profile
			s.seasonal.Update(ctx, a.MetricName, a.Labels, a.Value)

			results.Anomalies = append(results.Anomalies, &pb.AnomalyResult{
				JobId:          job.Id,
				MetricName:     a.MetricName,
				Labels:         a.Labels,
				CurrentValue:   a.Value,
				BaselineMean:   a.Mean,
				BaselineStddev: a.Stddev,
				AnomalyScore:   a.Score,
				Severity:       a.Severity,
				Signal:         a.Signal,
				Detector:       a.Detector,
				Timestamp:      a.Timestamp.Unix(),
			})
		}

		metrics.WorkerJobsProcessed.Add(ctx, 1, metric.WithAttributes(
			attribute.String("type", job.Type.String()), attribute.String("status", "success")))
		metrics.WorkerJobDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
			attribute.String("type", job.Type.String())))
	}

	return results, nil
}

// processJob executes one detection job. The int is the number of adaptive
// evaluations past warm-up (FDR family contribution) — always 0 for static.
func (s *workerServer) processJob(ctx context.Context, job *pb.DetectionJob, cache map[string][]ingestion.Sample) ([]detection.Anomaly, int, error) {
	switch job.Type {
	case pb.JobType_JOB_TYPE_METRICS_STATIC:
		samples, err := s.cachedQueryVM(ctx, job.Query, cache)
		if err != nil {
			return nil, 0, err
		}
		rule := config.StaticRule{
			Name:      job.Name,
			Threshold: job.Threshold,
			Operator:  job.Operator,
			Severity:  job.Severity,
		}
		return s.engine.EvaluateMetricsStatic(rule, samples), 0, nil

	case pb.JobType_JOB_TYPE_METRICS_ADAPTIVE:
		samples, err := s.cachedQueryVM(ctx, job.Query, cache)
		if err != nil {
			return nil, 0, err
		}
		anomalies, tested := s.engine.EvaluateMetricsAdaptive(ctx, job.Name, samples)
		return anomalies, tested, nil

	case pb.JobType_JOB_TYPE_LOGS:
		if err := s.lokiLimiter.Wait(ctx); err != nil {
			return nil, 0, err
		}
		samples, err := s.logsPoller.QueryMetric(ctx, job.Query)
		if err != nil {
			return nil, 0, err
		}
		anomalies, tested := s.engine.EvaluateLogRate(ctx, job.Name, samples)
		return anomalies, tested, nil

	default:
		return nil, 0, fmt.Errorf("unknown job type: %v", job.Type)
	}
}

func (s *workerServer) cachedQueryVM(ctx context.Context, query string, cache map[string][]ingestion.Sample) ([]ingestion.Sample, error) {
	if cached, ok := cache[query]; ok {
		return cached, nil
	}
	if err := s.vmLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	samples, err := s.metricsPoller.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	cache[query] = samples
	return samples, nil
}
