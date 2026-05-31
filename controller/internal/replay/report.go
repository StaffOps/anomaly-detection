package replay

import (
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/version"
)

// Report is the full replay output, serializable to JSON and Markdown.
type Report struct {
	Metadata     Metadata        `json:"metadata"`
	Totals       Totals          `json:"totals"`
	TopWorkloads []WorkloadCount `json:"top_workloads"`
	Timeline     []TimelineEntry `json:"timeline"`
	Anomalies    []AnomalyEntry  `json:"anomalies"`
}

// Metadata holds replay run context.
type Metadata struct {
	SchemaVersion      string        `json:"schema_version"`
	ControllerVersion  string        `json:"controller_version"`
	RanAt              time.Time     `json:"ran_at"`
	WindowStart        time.Time     `json:"window_start"`
	WindowEnd          time.Time     `json:"window_end"`
	WarmupStart        time.Time     `json:"warmup_start"`
	WarmupEnd          time.Time     `json:"warmup_end"`
	WarmupFraction     float64       `json:"warmup_fraction"`
	TickIntervalSec    int           `json:"tick_interval_seconds"`
	ResultStatus       string        `json:"result_status"`
	ConfigSummary      ConfigSummary `json:"config_summary"`
	ExecutionMetrics   ExecutionMetrics `json:"execution_metrics"`
}

// ConfigSummary counts detection rules in the config.
type ConfigSummary struct {
	StaticRules     int `json:"static_rules"`
	AdaptiveMetrics int `json:"adaptive_metrics"`
	LogPatterns     int `json:"log_patterns"`
}

// Totals aggregates anomaly counts.
type Totals struct {
	Anomalies      int            `json:"anomalies"`
	BySeverity     map[string]int `json:"by_severity"`
	BySignal       map[string]int `json:"by_signal"`
	ByDetector     map[string]int `json:"by_detector"`
	ByKind         map[string]int `json:"by_kind"`
	WarmupSkipped  int            `json:"warmup_skipped"`
	QueryErrors    int            `json:"query_errors"`
}

// WorkloadCount is a top-workload entry.
type WorkloadCount struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Count     int    `json:"count"`
}

// TimelineEntry is a per-hour bucket.
type TimelineEntry struct {
	Hour       time.Time      `json:"hour"`
	Anomalies  int            `json:"anomalies"`
	BySeverity map[string]int `json:"by_severity"`
}

// AnomalyEntry is the JSON-serializable form of a detection.Anomaly.
type AnomalyEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Namespace    string    `json:"namespace"`
	Pod          string    `json:"pod,omitempty"`
	Workload     string    `json:"workload,omitempty"`
	Metric       string    `json:"metric"`
	Severity     string    `json:"severity"`
	Signal       string    `json:"signal"`
	Detector     string    `json:"detector"`
	Score        float64   `json:"score"`
	Current      float64   `json:"current"`
	BaselineMean float64   `json:"baseline_mean"`
}

// WriteJSON serializes the report as indented JSON to w.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// reportBuilder accumulates anomalies and builds the final Report.
type reportBuilder struct {
	anomalies     []detection.Anomaly
	maxAnomalies  int
	warmupSkipped int
	queryErrors   int
	timeline      map[time.Time]*TimelineEntry
	workloads     map[string]int // "ns/workload" → count
}

func newReportBuilder(maxAnomalies int) *reportBuilder {
	return &reportBuilder{
		maxAnomalies: maxAnomalies,
		timeline:     make(map[time.Time]*TimelineEntry),
		workloads:    make(map[string]int),
	}
}

func (b *reportBuilder) addAnomaly(a detection.Anomaly) {
	if b.maxAnomalies > 0 && len(b.anomalies) >= b.maxAnomalies {
		return
	}
	b.anomalies = append(b.anomalies, a)

	// Timeline bucketing (truncate to hour).
	hour := a.Timestamp.UTC().Truncate(time.Hour)
	te, ok := b.timeline[hour]
	if !ok {
		te = &TimelineEntry{Hour: hour, BySeverity: make(map[string]int)}
		b.timeline[hour] = te
	}
	te.Anomalies++
	te.BySeverity[a.Severity]++

	// Workload counting.
	ns := a.Labels["namespace"]
	wl := workloadName(a)
	key := ns + "/" + wl
	b.workloads[key]++
}

func (b *reportBuilder) addWarmupSkipped() { b.warmupSkipped++ }
func (b *reportBuilder) addQueryError()    { b.queryErrors++ }

func (b *reportBuilder) build(meta Metadata) *Report {
	totals := Totals{
		Anomalies:     len(b.anomalies),
		BySeverity:    make(map[string]int),
		BySignal:      make(map[string]int),
		ByDetector:    make(map[string]int),
		ByKind:        make(map[string]int),
		WarmupSkipped: b.warmupSkipped,
		QueryErrors:   b.queryErrors,
	}
	entries := make([]AnomalyEntry, 0, len(b.anomalies))
	for _, a := range b.anomalies {
		totals.BySeverity[a.Severity]++
		totals.BySignal[a.Signal]++
		totals.ByDetector[a.Detector]++
		totals.ByKind[anomalyKind(a)]++
		entries = append(entries, toAnomalyEntry(a))
	}

	// Top workloads (top 20).
	type wc struct {
		key   string
		count int
	}
	wcs := make([]wc, 0, len(b.workloads))
	for k, c := range b.workloads {
		wcs = append(wcs, wc{k, c})
	}
	sort.Slice(wcs, func(i, j int) bool { return wcs[i].count > wcs[j].count })
	if len(wcs) > 20 {
		wcs = wcs[:20]
	}
	topWL := make([]WorkloadCount, len(wcs))
	for i, w := range wcs {
		ns, wl := splitWorkloadKey(w.key)
		topWL[i] = WorkloadCount{Namespace: ns, Workload: wl, Count: w.count}
	}

	// Timeline sorted.
	tl := make([]TimelineEntry, 0, len(b.timeline))
	for _, te := range b.timeline {
		tl = append(tl, *te)
	}
	sort.Slice(tl, func(i, j int) bool { return tl[i].Hour.Before(tl[j].Hour) })

	// Result status (only set if not already forced, e.g. by SIGINT → "partial").
	if meta.ResultStatus == "" {
		switch {
		case b.queryErrors > 0:
			meta.ResultStatus = "partial"
		case totals.Anomalies > 0:
			meta.ResultStatus = "anomalies_detected"
		default:
			meta.ResultStatus = "no_anomalies"
		}
	}

	return &Report{
		Metadata:     meta,
		Totals:       totals,
		TopWorkloads: topWL,
		Timeline:     tl,
		Anomalies:    entries,
	}
}

// BuildReport constructs a Report from collected anomalies and metadata.
// Exported for testing.
func BuildReport(anomalies []detection.Anomaly, maxAnomalies int, meta Metadata) *Report {
	rb := newReportBuilder(maxAnomalies)
	for _, a := range anomalies {
		rb.addAnomaly(a)
	}
	return rb.build(meta)
}

// NewMetadata creates a Metadata with standard fields populated.
func NewMetadata(rcfg ReplayConfig, tickInterval time.Duration, warmupEnd time.Time, configSummary ConfigSummary, execMetrics ExecutionMetrics) Metadata {
	return Metadata{
		SchemaVersion:     "1",
		ControllerVersion: version.Version,
		RanAt:             time.Now().UTC(),
		WindowStart:       rcfg.From.UTC(),
		WindowEnd:         rcfg.To.UTC(),
		WarmupStart:       rcfg.From.UTC(),
		WarmupEnd:         warmupEnd.UTC(),
		WarmupFraction:    rcfg.WarmupFraction,
		TickIntervalSec:   int(tickInterval.Seconds()),
		ConfigSummary:     configSummary,
		ExecutionMetrics:  execMetrics,
	}
}

func toAnomalyEntry(a detection.Anomaly) AnomalyEntry {
	return AnomalyEntry{
		Timestamp:    a.Timestamp.UTC(),
		Namespace:    a.Labels["namespace"],
		Pod:          a.Labels["pod"],
		Workload:     workloadName(a),
		Metric:       a.MetricName,
		Severity:     a.Severity,
		Signal:       a.Signal,
		Detector:     a.Detector,
		Score:        a.Score,
		Current:      a.Value,
		BaselineMean: a.Mean,
	}
}

func anomalyKind(a detection.Anomaly) string {
	if a.Labels["pod"] != "" {
		return "pod"
	}
	return "workload"
}

func workloadName(a detection.Anomaly) string {
	if wl := a.Labels["workload"]; wl != "" {
		return wl
	}
	if dep := a.Labels["deployment"]; dep != "" {
		return dep
	}
	if sn := a.Labels["service_name"]; sn != "" {
		return sn
	}
	return a.Labels["pod"]
}

func splitWorkloadKey(key string) (ns, wl string) {
	for i := range key {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}
