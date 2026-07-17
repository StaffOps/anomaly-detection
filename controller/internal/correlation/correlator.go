package correlation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/enrichment"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Enricher is the contract the correlator uses to fetch enrichment bundles.
// Implemented by *enrichment.Engine. Decoupled to keep correlator testable.
type Enricher interface {
	Run(ctx context.Context, id enrichment.Identity) enrichment.Bundle
}

// Kind tells the dispatcher whether an alert represents a single pod or
// a workload-wide pattern (≥N replicas of the same workload anomalous).
type Kind string

const (
	KindPod      Kind = "pod"
	KindWorkload Kind = "workload"
)

// dedupStore is the minimal Redis interface needed for deduplication.
// Extracted as an interface so tests can substitute a fake without a real Redis.
type dedupStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error
}

// Correlator groups anomalies by workload within a time window,
// deduplicates via Redis TTL, and escalates severity.
//
// Detection strategies (run in this order):
//
//  1. Workload pattern: if ≥workloadMinPods distinct pods of the same
//     workload are anomalous in the same window, emit a single workload-level
//     alert and suppress the per-pod alerts. This collapses noisy "all replicas
//     under load" events into one signal.
//
//  2. Pod-level: any remaining group fires as before (one alert per pod).
//
// The pod->workload mapping is derived via regex on pod names (see workload.go).
type Correlator struct {
	redis           dedupStore
	enricher        Enricher
	window          time.Duration
	cooldown        time.Duration
	workloadMinPods int

	mu      sync.Mutex
	pending map[string]*group // key: namespace/pod
}

type group struct {
	anomalies []detection.Anomaly
	firstSeen time.Time
}

// CorrelatedAlert is the output: one or more anomalies grouped into a single alert.
//
// When Kind=KindWorkload, AffectedPods/AffectedReplicas are populated and
// Workload identifies the K8s workload (Deployment/StatefulSet/DaemonSet) instead
// of a single pod.
type CorrelatedAlert struct {
	Kind             Kind
	Anomalies        []detection.Anomaly
	Severity         string
	Cluster          string
	Namespace        string
	Workload         string
	Signals          []string // unique signals involved
	Enrichment       enrichment.Bundle
	MLDetection      *MLDetection // populated when DetectMultivariate runs
	AffectedPods     []string     // populated for KindWorkload
	AffectedReplicas int          // = len(AffectedPods)
}

// MLDetection is the multivariate verdict (Isolation Forest) for the alert.
// Pointer-typed so absence (nil) is distinguishable from a "no anomaly" result.
type MLDetection struct {
	IsAnomaly    bool
	Score        float64
	Contributors []string
	FeatureCount int
}

// NewCorrelator builds a Correlator. Pass enricher = nil to disable enrichment.
// workloadMinPods sets the threshold for workload-pattern detection (≥3 typical).
func NewCorrelator(redis dedupStore, enricher Enricher, window, cooldown time.Duration, workloadMinPods int) *Correlator {
	if workloadMinPods < 2 {
		workloadMinPods = 3 // sane default
	}
	return &Correlator{
		redis:           redis,
		enricher:        enricher,
		window:          window,
		cooldown:        cooldown,
		workloadMinPods: workloadMinPods,
		pending:         make(map[string]*group),
	}
}

// Add ingests an anomaly into the correlation window.
func (c *Correlator) Add(a detection.Anomaly) {
	key := workloadKey(a)
	c.mu.Lock()
	defer c.mu.Unlock()

	g, ok := c.pending[key]
	if !ok {
		g = &group{firstSeen: time.Now()}
		c.pending[key] = g
	}
	g.anomalies = append(g.anomalies, a)
}

// Flush returns correlated alerts for groups whose window has expired.
//
// Flow per cycle:
//  1. Collect groups that aged past the window
//  2. Detect workload patterns (≥workloadMinPods of same workload affected)
//  3. Emit one workload alert per pattern, mark contributing pod groups as suppressed
//  4. Emit pod-level alerts for remaining groups
//  5. Run dedup + enrichment on each emitted alert
func (c *Correlator) Flush(ctx context.Context) []CorrelatedAlert {
	c.mu.Lock()
	now := time.Now()
	var ready []string
	for key, g := range c.pending {
		if now.Sub(g.firstSeen) >= c.window {
			ready = append(ready, key)
		}
	}

	groups := make(map[string]*group, len(ready))
	for _, key := range ready {
		groups[key] = c.pending[key]
		delete(c.pending, key)
	}
	c.mu.Unlock()

	// Phase 1: detect workload-level patterns and decide which pod groups to suppress.
	workloadAlerts, suppressed := c.detectWorkloadPatterns(ctx, groups)

	// Phase 2: pod-level alerts for groups not absorbed into workload patterns.
	var alerts []CorrelatedAlert
	alerts = append(alerts, workloadAlerts...)

	for podKey, g := range groups {
		if suppressed[podKey] {
			metrics.PodAlertsSuppressed.Add(ctx, 1)
			continue
		}
		alert := c.buildPodAlert(g)
		alerts = append(alerts, alert)
	}

	// Apply dedup + enrichment to every alert that survives.
	return c.finalize(ctx, alerts)
}

// detectWorkloadPatterns scans the ready groups, identifies workloads with
// ≥workloadMinPods distinct affected pods, and returns workload-level alerts
// plus the set of pod-group keys that were absorbed (and should be suppressed).
func (c *Correlator) detectWorkloadPatterns(ctx context.Context, groups map[string]*group) ([]CorrelatedAlert, map[string]bool) {
	type wlAccumulator struct {
		anomalies []detection.Anomaly
		pods      map[string]bool
		podKeys   []string // cluster/namespace/pod keys for suppression marking
		cluster   string
		namespace string
	}
	wls := make(map[string]*wlAccumulator) // key: cluster/namespace/workload

	for podKey, g := range groups {
		if len(g.anomalies) == 0 {
			continue
		}
		first := g.anomalies[0]
		ns := first.Labels["namespace"]
		pod := first.Labels["pod"]
		if ns == "" || pod == "" {
			continue // service-level or unknown — not eligible
		}
		workload := ExtractWorkload(pod)
		if workload == pod {
			continue // unknown pattern — treat as bare pod
		}
		cluster := clusterOrUnknown(first.Labels)
		wlKey := cluster + "/" + ns + "/" + workload

		acc, ok := wls[wlKey]
		if !ok {
			acc = &wlAccumulator{
				pods:      make(map[string]bool),
				cluster:   cluster,
				namespace: ns,
			}
			wls[wlKey] = acc
		}
		acc.pods[pod] = true
		acc.podKeys = append(acc.podKeys, podKey)
		acc.anomalies = append(acc.anomalies, g.anomalies...)
	}

	var alerts []CorrelatedAlert
	suppressed := make(map[string]bool)

	for wlKey, acc := range wls {
		if len(acc.pods) < c.workloadMinPods {
			continue // not enough siblings — leave as pod-level alerts
		}
		// Promote: build single workload-level alert
		_ = wlKey
		signalSet := make(map[string]bool)
		maxSeverity := "warning"
		for _, a := range acc.anomalies {
			signalSet[a.Signal] = true
			if a.Severity == "critical" {
				maxSeverity = "critical"
			}
		}
		signals := make([]string, 0, len(signalSet))
		for s := range signalSet {
			signals = append(signals, s)
		}
		sort.Strings(signals)

		pods := make([]string, 0, len(acc.pods))
		for p := range acc.pods {
			pods = append(pods, p)
		}
		sort.Strings(pods)

		// Use first anomaly as representative (mainly for metric_name + value reference)
		rep := acc.anomalies[0]
		// Override label so dispatcher / enricher use the workload identity
		repLabels := make(map[string]string, len(rep.Labels)+1)
		for k, v := range rep.Labels {
			repLabels[k] = v
		}
		repLabels["workload"] = ExtractWorkloadFromKey(wlKey)
		repLabels["pod"] = "" // workload-level — no single pod
		rep.Labels = repLabels

		alerts = append(alerts, CorrelatedAlert{
			Kind:             KindWorkload,
			Anomalies:        []detection.Anomaly{rep},
			Severity:         maxSeverity,
			Cluster:          acc.cluster,
			Namespace:        acc.namespace,
			Workload:         ExtractWorkloadFromKey(wlKey),
			Signals:          signals,
			AffectedPods:     pods,
			AffectedReplicas: len(pods),
		})
		metrics.WorkloadPatterns.Add(ctx, 1, metric.WithAttributes(attribute.String("severity", maxSeverity)))

		// Mark contributing pod groups as suppressed
		for _, pk := range acc.podKeys {
			suppressed[pk] = true
		}
	}

	return alerts, suppressed
}

// ExtractWorkloadFromKey extracts the workload portion from a
// "cluster/namespace/workload" key (the trailing segment).
func ExtractWorkloadFromKey(key string) string {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return key[i+1:]
		}
	}
	return key
}

func (c *Correlator) buildPodAlert(g *group) CorrelatedAlert {
	signalSet := make(map[string]bool)
	maxSeverity := "warning"

	for _, a := range g.anomalies {
		signalSet[a.Signal] = true
		if a.Severity == "critical" {
			maxSeverity = "critical"
		}
	}

	// Escalate: multiple signals = critical
	if len(signalSet) >= 2 && maxSeverity == "warning" {
		maxSeverity = "critical"
	}

	signals := make([]string, 0, len(signalSet))
	for s := range signalSet {
		signals = append(signals, s)
	}

	ns := g.anomalies[0].Labels["namespace"]
	workload := g.anomalies[0].Labels["pod"]
	cluster := clusterOrUnknown(g.anomalies[0].Labels)

	return CorrelatedAlert{
		Kind:      KindPod,
		Anomalies: g.anomalies,
		Severity:  maxSeverity,
		Cluster:   cluster,
		Namespace: ns,
		Workload:  workload,
		Signals:   signals,
	}
}

func (c *Correlator) finalize(ctx context.Context, alerts []CorrelatedAlert) []CorrelatedAlert {
	out := make([]CorrelatedAlert, 0, len(alerts))
	for i := range alerts {
		ca := alerts[i]
		dedupKey := c.fingerprint(ca)

		exists, err := c.redis.Exists(ctx, dedupKey)
		if err != nil {
			slog.Warn("dedup check failed", "error", err)
		}
		if exists {
			metrics.AlertsDeduplicated.Add(ctx, 1)
			continue
		}
		if err := c.redis.SetWithTTL(ctx, dedupKey, "1", c.cooldown); err != nil {
			slog.Warn("dedup set failed", "error", err)
		}

		if c.enricher != nil && len(ca.Anomalies) > 0 {
			id := enrichment.IdentityFromLabels(ca.Anomalies[0].Labels)
			ca.Enrichment = c.enricher.Run(ctx, id)
		}

		metrics.AnomalyCorrelated.Add(ctx, 1, metric.WithAttributes(attribute.String("severity", ca.Severity)))
		out = append(out, ca)
	}
	return out
}

func (c *Correlator) fingerprint(alert CorrelatedAlert) string {
	data, _ := json.Marshal(map[string]string{
		"kind":     string(alert.Kind),
		"cluster":  alert.Cluster,
		"ns":       alert.Namespace,
		"workload": alert.Workload,
		"severity": alert.Severity,
	})
	h := sha256.Sum256(data)
	return fmt.Sprintf("alert:dedup:%x", h[:8])
}

// clusterOrUnknown returns the anomaly's cluster label, or "unknown" when
// absent — keeps cluster-prefixed keys unambiguous (vs. an empty segment).
func clusterOrUnknown(labels map[string]string) string {
	if c := labels["cluster"]; c != "" {
		return c
	}
	return "unknown"
}

func workloadKey(a detection.Anomaly) string {
	ns := a.Labels["namespace"]
	id := a.Labels["pod"]
	if id == "" {
		id = a.Labels["service_name"]
	}
	if ns == "" && id == "" {
		return "_unknown_"
	}
	return clusterOrUnknown(a.Labels) + "/" + ns + "/" + id
}
