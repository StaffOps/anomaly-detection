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
	"github.com/staffops/staffops-anomaly-detection/internal/replay/inject"
)

// systemicFailureThreshold is how many adaptive queries may fail back-to-back
// from the start of a tick before the engine stops trying the rest. A handful of
// consecutive failures with nothing succeeding means the backend is unreachable,
// not that individual rules are too expensive — and hammering it with the full
// rule set every tick only makes it worse.
const systemicFailureThreshold = 3

// Run executes the replay tick simulator over the configured window.
// It loads metric/log data in 1h chunks, iterates ticks from warmup_end to To,
// and runs detection on each tick's samples.
//
// When injector is non-nil, it perturbs series in-memory after query and before
// detection. Scoring is computed at the end and attached to the Report.
//
// No side effects: no Redis, no Alertmanager, no gRPC workers, no ML calls.
func Run(ctx context.Context, rcfg ReplayConfig, cfg *config.Config, injector *inject.Injector) (*Report, error) {
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
	vm := ingestion.NewMetricsPoller(cfg.Datasources.Prometheus)
	loki := ingestion.NewLogsPoller(cfg.Datasources.Loki)
	store := NewInMemStore(cfg.Baseline)
	engine := detection.NewEngine(cfg.Detection, store)

	step := cfg.Controller.JobInterval
	rb := newReportBuilder(rcfg.MaxAnomalies)

	// Same BH FDR filter the controller applies per cycle — replay mirrors it
	// per tick so FP counts are comparable with production behavior.
	fdrFilter := detection.NewFDR(cfg.Controller.FDRTarget)

	// Direction-of-badness and absolute-floor (min_value) maps. Both post-filters
	// the controller applies per cycle are mirrored here, in the same order, so
	// replay FP counts are comparable with production instead of inflated by
	// firings production would have dropped.
	directions := cfg.Detection.DirectionMap()
	floors := cfg.Detection.FloorMap()

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

		// Anomalies from all detectors at this tick, filtered through FDR
		// before entering the report (mirrors the controller's cycle flow).
		// tickTested accumulates the adaptive evaluations past warm-up — the
		// BH family size.
		var tickAnomalies []detection.Anomaly
		tickTested := 0

		// Determine chunk boundaries (1h chunks).
		chunkStart := tick.Add(-time.Hour)
		if chunkStart.Before(rcfg.From) {
			chunkStart = rcfg.From
		}

		// Query metrics for all adaptive metrics + static rules.
		{
			// Series are kept per metric: each rule evaluates ONLY its own
			// series. Pooling all series and evaluating every rule against the
			// pool (as this loop once did) creates baselines for foreign label
			// sets under every rule name — cross-metric contamination the
			// production worker path never has.
			perMetric := make([][]ingestion.TimeSeries, len(cfg.Detection.AdaptiveMetrics))
			failed, succeeded := 0, 0
			for i, am := range cfg.Detection.AdaptiveMetrics {
				qStart := time.Now()
				series, err := vm.QueryRange(ctx, am.Query, chunkStart, tick, step)
				mc.recordPromQuery(time.Since(qStart))
				if err != nil {
					// Degrade per METRIC, not per tick. This loop used to abort the
					// whole tick on the first failing query, so a single expensive
					// rule (e.g. a histogram over a very large series family hitting
					// the backend's sample limit) blanked every other rule and the
					// run reported zero anomalies — an infrastructure artifact
					// indistinguishable from "detected nothing".
					// Production does not behave that way: one rule's query failing
					// does not blind the other rules, and the BH family is whatever
					// actually got evaluated. Skipping just this metric mirrors that.
					slog.Warn("[REPLAY] vm query failed, skipping this metric for the tick",
						"tick", tick.Format(time.RFC3339), "metric", am.Name, "error", err)
					rb.addQueryError()
					failed++
					// Per-metric degradation must not turn into a stampede: if the
					// backend is down, every rule will fail, and retrying all of
					// them each tick multiplies the load on the thing that is
					// already struggling. Once nothing has succeeded in this tick's
					// first few attempts, treat it as systemic and stop early —
					// the tick is lost either way.
					if failed >= systemicFailureThreshold && i+1 == failed {
						slog.Warn("[REPLAY] consecutive query failures from the start, treating as systemic",
							"tick", tick.Format(time.RFC3339), "failures", failed)
						break
					}
					continue
				}
				// Inject synthetic faults in-memory (no-op when injector is nil).
				if injector != nil {
					series = injector.Apply(am.Name, series)
				}
				perMetric[i] = series
				succeeded++
			}
			// A tick is genuinely lost only when nothing could be evaluated:
			// every adaptive query failed, or the systemic short-circuit fired.
			// Anything less still carries signal worth scoring.
			if len(cfg.Detection.AdaptiveMetrics) > 0 && succeeded == 0 {
				mc.recordSkip()
				goto progress
			}

			// Run adaptive detection on each metric's own samples at this tick.
			for i, am := range cfg.Detection.AdaptiveMetrics {
				samples := SamplesAt(tick, perMetric[i])
				anomalies, tested := engine.EvaluateMetricsAdaptive(ctx, am.Name, samples)
				if !isWarmup {
					tickTested += tested
					for j := range anomalies {
						anomalies[j].Timestamp = tick
					}
					tickAnomalies = append(tickAnomalies, anomalies...)
				} else {
					rb.warmupSkipped += len(anomalies)
				}
			}
		}

		// Run static detection.
		for _, rule := range cfg.Detection.StaticRules {
			qStart := time.Now()
			series, err := vm.QueryRange(ctx, rule.Query, chunkStart, tick, step)
			mc.recordPromQuery(time.Since(qStart))
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
				tickAnomalies = append(tickAnomalies, anomalies...)
			}
		}

		// Run log-based detection.
		for _, lp := range cfg.Detection.LogPatterns {
			qStart := time.Now()
			series, err := loki.QueryMetricRange(ctx, lp.Query, chunkStart, tick, step)
			mc.recordLokiQuery()
			_ = qStart // loki duration not tracked in p95 (only Prometheus)
			if err != nil {
				slog.Warn("[REPLAY] loki query failed",
					"tick", tick.Format(time.RFC3339), "pattern", lp.Name, "error", err)
				rb.addQueryError()
				continue
			}
			samples := SamplesAt(tick, series)
			anomalies, tested := engine.EvaluateLogRate(ctx, lp.Name, samples)
			if !isWarmup {
				tickTested += tested
				for i := range anomalies {
					anomalies[i].Timestamp = tick
				}
				tickAnomalies = append(tickAnomalies, anomalies...)
			} else {
				rb.warmupSkipped += len(anomalies)
			}
		}

		// Direction, then floor, then FDR — the same order as the controller
		// cycle, so neither wrong-direction nor floored firings consume BH
		// acceptance and the counts stay comparable with production.
		if len(tickAnomalies) > 0 {
			ptrs := make([]*detection.Anomaly, len(tickAnomalies))
			for i := range tickAnomalies {
				ptrs[i] = &tickAnomalies[i]
			}
			ptrs, dirDropped := detection.FilterByDirection(ptrs, directions)
			rb.directionFiltered += dirDropped
			ptrs, floorDropped := detection.FilterByFloor(ptrs, floors)
			rb.floorFiltered += floorDropped
			accepted, rejected := fdrFilter.Apply(ptrs, tickTested)
			rb.fdrRejected += rejected
			for _, a := range accepted {
				rb.addAnomaly(*a)
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
	// Attach injection scoring when injector is active.
	if injector != nil {
		truths := injector.GroundTruths()
		// Convert report anomalies to DetectedAnomaly for the scorer.
		detected := make([]inject.DetectedAnomaly, len(report.Anomalies))
		for i, ae := range report.Anomalies {
			detected[i] = inject.DetectedAnomaly{
				Metric:    ae.Metric,
				Labels:    ae.Labels,
				Timestamp: ae.Timestamp,
			}
		}
		scoring := inject.Score(detected, truths, step)
		injResult := inject.BuildInjectionResult(injector.Seed(), truths)

		report.Injection = &InjectionBlock{
			Seed:         injResult.Seed,
			GroundTruths: convertGroundTruths(injResult.GroundTruths),
		}
		report.Scoring = &ScoringBlock{
			Precision:        scoring.Precision,
			Recall:           scoring.Recall,
			F1:               scoring.F1,
			TP:               scoring.TP,
			FP:               scoring.FP,
			FN:               scoring.FN,
			RecallByType:     scoring.RecallByType,
			DetectionLatency: scoring.DetectionLatency,
			FPCaveat:         scoring.FPCaveat,
		}
	}
	return report, nil
}

func convertGroundTruths(gts []inject.GroundTruthJSON) []GroundTruthEntry {
	out := make([]GroundTruthEntry, len(gts))
	for i, gt := range gts {
		out[i] = GroundTruthEntry{
			Target:    gt.Target,
			Type:      gt.Type,
			Start:     gt.Start,
			End:       gt.End,
			Magnitude: gt.Magnitude,
		}
	}
	return out
}
