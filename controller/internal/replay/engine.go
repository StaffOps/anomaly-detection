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

// Run executes the replay tick simulator over the configured window.
// It loads metric/log data in 1h chunks, iterates ticks from warmup_end to To,
// and runs detection on each tick's samples.
//
// No side effects: no Redis, no Alertmanager, no gRPC workers, no ML calls.
func Run(ctx context.Context, rcfg ReplayConfig, cfg *config.Config) (*Report, error) {
	mc := newMetricsCollector()

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
	rb := newReportBuilder(rcfg.MaxAnomalies)

	// Count total ticks for progress logging.
	totalTicks := int(rcfg.To.Sub(rcfg.From) / step)
	progressInterval := totalTicks / 10
	if progressInterval < 1 {
		progressInterval = 1
	}

	partial := false
	tickIdx := 0
	for tick := rcfg.From.Add(step); !tick.After(rcfg.To); tick = tick.Add(step) {
		// Check for cancellation (SIGINT/SIGTERM).
		select {
		case <-ctx.Done():
			slog.Warn("[REPLAY] interrupted, flushing partial report", "ticks_processed", tickIdx)
			partial = true
			goto done
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
		{
			var metricSeries []ingestion.TimeSeries
			skipTick := false
			for _, am := range cfg.Detection.AdaptiveMetrics {
				qStart := time.Now()
				series, err := vm.QueryRange(ctx, am.Query, chunkStart, tick, step)
				mc.recordVMQuery(time.Since(qStart))
				if err != nil {
					slog.Warn("[REPLAY] vm query failed, skipping tick",
						"tick", tick.Format(time.RFC3339), "metric", am.Name, "error", err)
					rb.addQueryError()
					mc.recordSkip()
					skipTick = true
					break
				}
				metricSeries = append(metricSeries, series...)
			}
			if skipTick {
				goto progress
			}

			// Run adaptive detection on metric samples at this tick.
			samples := SamplesAt(tick, metricSeries)
			for _, am := range cfg.Detection.AdaptiveMetrics {
				anomalies := engine.EvaluateMetricsAdaptive(ctx, am.Name, samples)
				if !isWarmup {
					for i := range anomalies {
						anomalies[i].Timestamp = tick
					}
					addAll(rb, anomalies)
				} else {
					rb.warmupSkipped += len(anomalies)
				}
			}
		}

		// Run static detection.
		for _, rule := range cfg.Detection.StaticRules {
			qStart := time.Now()
			series, err := vm.QueryRange(ctx, rule.Query, chunkStart, tick, step)
			mc.recordVMQuery(time.Since(qStart))
			if err != nil {
				slog.Warn("[REPLAY] vm static query failed",
					"tick", tick.Format(time.RFC3339), "rule", rule.Name, "error", err)
				rb.addQueryError()
				continue
			}
			samples := SamplesAt(tick, series)
			if !isWarmup {
				anomalies := engine.EvaluateMetricsStatic(rule, samples)
				for i := range anomalies {
					anomalies[i].Timestamp = tick
				}
				addAll(rb, anomalies)
			}
		}

		// Run log-based detection.
		for _, lp := range cfg.Detection.LogPatterns {
			qStart := time.Now()
			series, err := loki.QueryMetricRange(ctx, lp.Query, chunkStart, tick, step)
			mc.recordLokiQuery()
			_ = qStart // loki duration not tracked in p95 (only VM)
			if err != nil {
				slog.Warn("[REPLAY] loki query failed",
					"tick", tick.Format(time.RFC3339), "pattern", lp.Name, "error", err)
				rb.addQueryError()
				continue
			}
			samples := SamplesAt(tick, series)
			anomalies := engine.EvaluateLogRate(ctx, lp.Name, samples)
			if !isWarmup {
				for i := range anomalies {
					anomalies[i].Timestamp = tick
				}
				addAll(rb, anomalies)
			} else {
				rb.warmupSkipped += len(anomalies)
			}
		}

		mc.recordTick()

		// Sample memory every 10% of ticks.
	progress:
		if tickIdx%progressInterval == 0 {
			mc.sampleMemory()
			pct := (tickIdx * 100) / totalTicks
			slog.Info("[REPLAY] progress", "percent", pct, "ticks", tickIdx, "anomalies", len(rb.anomalies))
		}
	}

done:
	mc.sampleMemory()
	execMetrics := mc.snapshot()

	configSummary := ConfigSummary{
		StaticRules:     len(cfg.Detection.StaticRules),
		AdaptiveMetrics: len(cfg.Detection.AdaptiveMetrics),
		LogPatterns:     len(cfg.Detection.LogPatterns),
	}

	meta := NewMetadata(rcfg, step, warmupEnd, configSummary, execMetrics)
	if partial {
		meta.ResultStatus = "partial"
	}

	report := rb.build(meta)
	return report, nil
}

func addAll(rb *reportBuilder, anomalies []detection.Anomaly) {
	for _, a := range anomalies {
		rb.addAnomaly(a)
	}
}
