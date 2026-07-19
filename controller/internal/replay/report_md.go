package replay

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// WriteMarkdown renders the report as a Markdown document to w.
func (r *Report) WriteMarkdown(w io.Writer) error {
	var writeErr error
	p := func(format string, args ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(w, format+"\n", args...)
	}

	p("# Replay Report")
	p("")
	p("**Ran at**: %s  ", r.Metadata.RanAt.UTC().Format(time.RFC3339))
	p("**Controller**: %s  ", r.Metadata.ControllerVersion)
	p("**Status**: %s  ", r.Metadata.ResultStatus)
	p("")

	// Window
	p("## Window")
	p("")
	p("| Field | Value |")
	p("|-------|-------|")
	p("| Start | %s |", r.Metadata.WindowStart.UTC().Format(time.RFC3339))
	p("| End | %s |", r.Metadata.WindowEnd.UTC().Format(time.RFC3339))
	p("| Warmup end | %s |", r.Metadata.WarmupEnd.UTC().Format(time.RFC3339))
	p("| Warmup fraction | %.2f |", r.Metadata.WarmupFraction)
	p("| Tick interval | %ds |", r.Metadata.TickIntervalSec)
	p("")

	// Totals
	p("## Totals")
	p("")
	p("| Metric | Count |")
	p("|--------|-------|")
	p("| Anomalies | %d |", r.Totals.Anomalies)
	p("| Warmup skipped | %d |", r.Totals.WarmupSkipped)
	p("| Query errors | %d |", r.Totals.QueryErrors)
	p("| FDR rejected | %d |", r.Totals.FDRRejected)
	p("")

	// By severity
	if len(r.Totals.BySeverity) > 0 {
		p("### By Severity")
		p("")
		p("| Severity | Count |")
		p("|----------|-------|")
		for _, sev := range sortedKeys(r.Totals.BySeverity) {
			p("| %s | %d |", sev, r.Totals.BySeverity[sev])
		}
		p("")
	}

	// By detector
	if len(r.Totals.ByDetector) > 0 {
		p("### By Detector")
		p("")
		p("| Detector | Count |")
		p("|----------|-------|")
		for _, det := range sortedKeys(r.Totals.ByDetector) {
			p("| %s | %d |", det, r.Totals.ByDetector[det])
		}
		p("")
	}

	// By signal
	if len(r.Totals.BySignal) > 0 {
		p("### By Signal")
		p("")
		p("| Signal | Count |")
		p("|--------|-------|")
		for _, sig := range sortedKeys(r.Totals.BySignal) {
			p("| %s | %d |", sig, r.Totals.BySignal[sig])
		}
		p("")
	}

	// By kind
	if len(r.Totals.ByKind) > 0 {
		p("### By Kind")
		p("")
		p("| Kind | Count |")
		p("|------|-------|")
		for _, k := range sortedKeys(r.Totals.ByKind) {
			p("| %s | %d |", k, r.Totals.ByKind[k])
		}
		p("")
	}

	// Top workloads
	if len(r.TopWorkloads) > 0 {
		p("## Top Workloads")
		p("")
		p("| # | Namespace | Workload | Count |")
		p("|---|-----------|----------|-------|")
		for i, wl := range r.TopWorkloads {
			p("| %d | %s | %s | %d |", i+1, wl.Namespace, wl.Workload, wl.Count)
		}
		p("")
	}

	// Timeline with sparkline
	if len(r.Timeline) > 0 {
		p("## Timeline")
		p("")
		p("```")
		p("%s", sparkline(r.Timeline))
		p("```")
		p("")
		p("| Hour (UTC) | Anomalies | Severity |")
		p("|------------|-----------|----------|")
		for _, te := range r.Timeline {
			sevParts := make([]string, 0)
			for _, k := range sortedKeys(te.BySeverity) {
				sevParts = append(sevParts, fmt.Sprintf("%s:%d", k, te.BySeverity[k]))
			}
			p("| %s | %d | %s |", te.Hour.UTC().Format("2006-01-02 15:04"), te.Anomalies, strings.Join(sevParts, " "))
		}
		p("")
	}

	// Injection scoring (only populated when --inject was used).
	if r.Scoring != nil {
		s := r.Scoring
		p("## Injection Scoring")
		p("")
		p("| Metric | Value |")
		p("|--------|-------|")
		p("| Precision | %.3f |", s.Precision)
		p("| Recall | %.3f |", s.Recall)
		p("| F1 | %.3f |", s.F1)
		p("| TP / FP / FN | %d / %d / %d |", s.TP, s.FP, s.FN)
		p("")
		if len(s.RecallByType) > 0 {
			p("| Fault type | Recall |")
			p("|------------|--------|")
			ftypes := make([]string, 0, len(s.RecallByType))
			for k := range s.RecallByType {
				ftypes = append(ftypes, k)
			}
			sort.Strings(ftypes)
			for _, k := range ftypes {
				p("| %s | %.3f |", k, s.RecallByType[k])
			}
			p("")
		}
		if s.FPCaveat != "" {
			p("> %s", s.FPCaveat)
			p("")
		}
	}

	// Execution metrics
	p("## Execution")
	p("")
	p("| Metric | Value |")
	p("|--------|-------|")
	em := r.Metadata.ExecutionMetrics
	p("| Duration | %.1fs |", em.DurationSeconds)
	p("| Ticks processed | %d |", em.TicksProcessed)
	p("| Ticks skipped | %d |", em.TicksSkippedQueryError)
	p("| Prometheus queries | %d |", em.PromQueriesTotal)
	p("| Prometheus p95 latency | %.3fs |", em.PromQueryDurationP95)
	p("| Loki queries | %d |", em.LokiQueriesTotal)
	p("| Memory peak | %.1f MB |", em.MemoryPeakMB)
	p("")

	return writeErr
}

var sparkChars = []rune("▁▂▃▄▅▆▇█")

func sparkline(timeline []TimelineEntry) string {
	if len(timeline) == 0 {
		return ""
	}
	max := 0
	for _, te := range timeline {
		if te.Anomalies > max {
			max = te.Anomalies
		}
	}
	if max == 0 {
		return strings.Repeat(string(sparkChars[0]), len(timeline))
	}
	var sb strings.Builder
	for _, te := range timeline {
		idx := (te.Anomalies * (len(sparkChars) - 1)) / max
		sb.WriteRune(sparkChars[idx])
	}
	return sb.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
