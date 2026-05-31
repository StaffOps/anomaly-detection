package replay

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/ingestion"
)

// Report holds the results of a replay run.
type Report struct {
	Anomalies    []detection.Anomaly
	TotalTicks   int
	TicksSkipped int
	Duration     time.Duration
	Status       string // "complete" or "partial"
}

// Run executes the replay tick simulator over the configured window.
// It loads metric/log data in 1h chunks, iterates ticks from warmup_end to To,
// and runs detection on each tick's samples.
//
// No side effects: no Redis, no Alertmanager, no gRPC workers, no ML calls.
func Run(ctx context.Context, rcfg ReplayConfig, cfg *config.Config) (*Report, error) {
	start := time.Now()

	// Set up signal handling for graceful partial flush.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Compute warmup end.
	window := rcfg.To.Sub(rcfg.From)
	fractionWarmup := time.Duration(float64(window) * rcfg.WarmupFraction)
	minWarmup := time.Duration(cfg.Baseline.WarmUpSamples) * cfg.Controller.JobInterval
	warmupDur := fractionWarmup
	if minWarmup > warmupDur {
		warmupDur = minWarmup
	}
	warmupEnd := rcfg.From.Add(warmupDur)

	slog.Info("[REPLAY] starting",
		"from", rcfg.From.Format(time.RFC3339),
		"to", rcfg.To.Format(time.RFC3339),
		"warmup_end", warmupEnd.Format(time.RFC3339),
		"tick_interval", cfg.Controller.JobInterval,
	)

	// Initialize pollers and in-memory baseline.
	vm := ingestion.NewMetricsPoller(cfg.Datasources.VictoriaMetrics)
	loki := ingestion.NewLogsPoller(cfg.Datasources.Loki)
	store := NewInMemStore(cfg.Baseline)
	engine := detection.NewEngine(cfg.Detection, store)

	step := cfg.Controller.JobInterval
	report := &Report{Status: "complete"}

	// Count total ticks for progress logging.
	totalTicks := int(rcfg.To.Sub(rcfg.From) / step)
	progressInterval := totalTicks / 10
	if progressInterval < 1 {
		progressInterval = 1
	}

	tickIdx := 0
	for tick := rcfg.From.Add(step); !tick.After(rcfg.To); tick = tick.Add(step) {
		// Check for cancellation (SIGINT/SIGTERM).
		select {
		case <-ctx.Done():
			slog.Warn("[REPLAY] interrupted, flushing partial report", "ticks_processed", tickIdx)
			report.Status = "partial"
			report.TotalTicks = tickIdx
			report.Duration = time.Since(start)
			return report, nil
		default:
		}

		tickIdx++
		isWarmup := tick.Before(warmupEnd)

		// Determine chunk boundaries (1h chunks).
		chunkStart := tick.Add(-time.Hour)
		if chunkStart.Before(rcfg.From) {
			chunkStart = rcfg.From
		}

		// Query metrics for all adaptive metrics + static rules.
		var metricSeries []ingestion.TimeSeries
		for _, am := range cfg.Detection.AdaptiveMetrics {
			series, err := vm.QueryRange(ctx, am.Query, chunkStart, tick, step)
			if err != nil {
				slog.Warn("[REPLAY] vm query failed, skipping tick",
					"tick", tick.Format(time.RFC3339), "metric", am.Name, "error", err)
				report.TicksSkipped++
				goto nextTick
			}
			metricSeries = append(metricSeries, series...)
		}

		// Run adaptive detection on metric samples at this tick.
		{
			samples := SamplesAt(tick, metricSeries)
			for _, am := range cfg.Detection.AdaptiveMetrics {
				anomalies := engine.EvaluateMetricsAdaptive(ctx, am.Name, samples)
				if !isWarmup {
					for i := range anomalies {
						anomalies[i].Timestamp = tick
					}
					report.Anomalies = append(report.Anomalies, anomalies...)
				}
			}
		}

		// Run static detection.
		for _, rule := range cfg.Detection.StaticRules {
			series, err := vm.QueryRange(ctx, rule.Query, chunkStart, tick, step)
			if err != nil {
				slog.Warn("[REPLAY] vm static query failed",
					"tick", tick.Format(time.RFC3339), "rule", rule.Name, "error", err)
				continue // skip this rule, not the whole tick
			}
			samples := SamplesAt(tick, series)
			if !isWarmup {
				anomalies := engine.EvaluateMetricsStatic(rule, samples)
				for i := range anomalies {
					anomalies[i].Timestamp = tick
				}
				report.Anomalies = append(report.Anomalies, anomalies...)
			}
		}

		// Run log-based detection.
		for _, lp := range cfg.Detection.LogPatterns {
			series, err := loki.QueryMetricRange(ctx, lp.Query, chunkStart, tick, step)
			if err != nil {
				slog.Warn("[REPLAY] loki query failed",
					"tick", tick.Format(time.RFC3339), "pattern", lp.Name, "error", err)
				continue
			}
			samples := SamplesAt(tick, series)
			anomalies := engine.EvaluateLogRate(ctx, lp.Name, samples)
			if !isWarmup {
				for i := range anomalies {
					anomalies[i].Timestamp = tick
				}
				report.Anomalies = append(report.Anomalies, anomalies...)
			}
		}

		// Enforce max anomalies cap.
		if rcfg.MaxAnomalies > 0 && len(report.Anomalies) > rcfg.MaxAnomalies {
			report.Anomalies = report.Anomalies[:rcfg.MaxAnomalies]
		}

		// Progress logging every 10%.
		if tickIdx%progressInterval == 0 {
			pct := (tickIdx * 100) / totalTicks
			slog.Info("[REPLAY] progress", "percent", pct, "ticks", tickIdx, "anomalies", len(report.Anomalies))
		}

	nextTick:
	}

	report.TotalTicks = tickIdx
	report.Duration = time.Since(start)
	return report, nil
}
