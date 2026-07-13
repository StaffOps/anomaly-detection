package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/correlation"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Dispatcher sends alerts to Alertmanager.
type Dispatcher struct {
	client  *http.Client
	url     string
	dryRun  bool
	cluster string
	links   *LinkBuilder
}

func NewDispatcher(cfg config.DatasourceEndpoint, dryRun bool, cluster string, links *LinkBuilder) *Dispatcher {
	return &Dispatcher{
		client:  &http.Client{Timeout: 10 * time.Second},
		url:     cfg.URL + "/api/v2/alerts",
		dryRun:  dryRun,
		cluster: cluster,
		links:   links,
	}
}

// FireCorrelated sends a correlated alert (with optional enrichment context) to Alertmanager.
// Handles both pod-level and workload-level alerts.
func (d *Dispatcher) FireCorrelated(ctx context.Context, ca correlation.CorrelatedAlert) error {
	if len(ca.Anomalies) == 0 {
		return nil
	}
	rep := ca.Anomalies[0]
	rep.Severity = ca.Severity

	// Resolve identity: workload (group of replicas) or single pod (or service if pod absent).
	identity := identityOf(ca, rep)
	workloadLabel := amWorkloadLabel(ca, rep)
	reason := buildReason(ca, rep)

	alert := d.buildAlert(rep, ca.Kind, identity, workloadLabel, reason)
	d.attachContext(&alert, ca)
	d.attachWorkloadFields(&alert, ca)

	// Audit log — always emitted, even in dry-run. Always include the metric name
	// and a human-readable reason so operators can identify the trigger at a glance.
	enrichLog := []string{}
	for _, r := range ca.Enrichment.Results {
		if r.Error == "" {
			enrichLog = append(enrichLog, fmt.Sprintf("%s=%.4f", r.Name, r.Value))
		}
	}
	logFields := []any{
		"alert_name", "AnomalyDetected",
		"kind", string(ca.Kind),
		"severity", ca.Severity,
		"cluster", d.cluster,
		"namespace", ca.Namespace,
		"identity", identity,
		"signals", strings.Join(ca.Signals, ","),
		"detector", rep.Detector,
		"metric", rep.MetricName,
		"reason", reason,
		"current", rep.Value,
		"baseline", rep.Mean,
		"anomaly_score", rep.Score,
		"enrichments", strings.Join(enrichLog, " "),
		"enrichment_cached", ca.Enrichment.Cached,
		"dry_run", d.dryRun,
	}
	if ca.Kind == correlation.KindWorkload {
		logFields = append(logFields,
			"affected_replicas", ca.AffectedReplicas,
			"affected_pods", strings.Join(ca.AffectedPods, ","),
		)
	}
	if ca.MLDetection != nil {
		logFields = append(logFields,
			"ml_anomaly", ca.MLDetection.IsAnomaly,
			"ml_score", ca.MLDetection.Score,
			"ml_features", ca.MLDetection.FeatureCount,
		)
		if ca.MLDetection.IsAnomaly {
			logFields = append(logFields, "ml_contributors", strings.Join(ca.MLDetection.Contributors, ","))
		}
	}
	slog.Info("alert_fired", logFields...)

	// Increment counter BEFORE the dry-run check so the counter measures intent
	// (alerts that would have fired) rather than delivery. Dispatch errors are
	// counted separately in metrics.AlertsDispatchErrors.
	metrics.AlertsFired.WithLabelValues(ca.Severity).Inc()

	if d.dryRun {
		return nil
	}
	return d.send(ctx, alert, ca.Severity)
}

// Fire sends a single anomaly as an alert (legacy path, no enrichment).
func (d *Dispatcher) Fire(ctx context.Context, anomaly detection.Anomaly) error {
	identity := anomaly.Labels["pod"]
	if identity == "" {
		identity = anomaly.Labels["service_name"]
	}
	// Bounded workload label for AM (extracted from pod name when applicable).
	workloadLabel := identity
	if pod := anomaly.Labels["pod"]; pod != "" {
		if w := correlation.ExtractWorkload(pod); w != "" {
			workloadLabel = w
		}
	}
	reason := simpleReason(anomaly)
	alert := d.buildAlert(anomaly, correlation.KindPod, identity, workloadLabel, reason)

	slog.Info("alert_fired",
		"alert_name", "AnomalyDetected",
		"kind", "pod",
		"severity", anomaly.Severity,
		"cluster", d.cluster,
		"namespace", anomaly.Labels["namespace"],
		"identity", identity,
		"signal", anomaly.Signal,
		"detector", anomaly.Detector,
		"metric", anomaly.MetricName,
		"reason", reason,
		"current", anomaly.Value,
		"baseline", anomaly.Mean,
		"anomaly_score", anomaly.Score,
		"dry_run", d.dryRun,
	)

	// Increment counter BEFORE the dry-run check (see FireCorrelated for rationale).
	metrics.AlertsFired.WithLabelValues(anomaly.Severity).Inc()

	if d.dryRun {
		return nil
	}
	return d.send(ctx, alert, anomaly.Severity)
}

func (d *Dispatcher) send(ctx context.Context, alert alertmanagerAlert, severity string) error {
	body, err := json.Marshal([]alertmanagerAlert{alert})
	if err != nil {
		return fmt.Errorf("marshal alert: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		metrics.AlertsDispatchErrors.Inc()
		return fmt.Errorf("post alertmanager: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		metrics.AlertsDispatchErrors.Inc()
		return fmt.Errorf("alertmanager returned %d", resp.StatusCode)
	}

	// Note: AlertsFired is incremented by the caller (Fire / FireCorrelated)
	// BEFORE this function — to count intent regardless of dry-run.
	// Dispatch failures are counted in AlertsDispatchErrors above.
	_ = severity
	return nil
}

type alertmanagerAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
}

// identityOf resolves a human-readable identity for the alert.
// For workload-kind: <workload>; for pod-kind: <pod> or fallback to <service_name>.
//
// This identity goes into log messages and the summary annotation. It is NOT
// used as an Alertmanager label — see amWorkloadLabel for that, which must be
// bounded for routing tractability.
func identityOf(ca correlation.CorrelatedAlert, rep detection.Anomaly) string {
	if ca.Kind == correlation.KindWorkload {
		return ca.Workload
	}
	if rep.Labels["pod"] != "" {
		return rep.Labels["pod"]
	}
	if rep.Labels["service_name"] != "" {
		return rep.Labels["service_name"]
	}
	return ca.Workload // last resort
}

// amWorkloadLabel returns a low-cardinality value suitable for the
// Alertmanager `workload` label. Pod names rotate on every deploy/restart so
// using them here makes routing rules untestable and causes Prometheus
// cardinality explosion if the metric ever joins on this label.
//
// Resolution order:
//  1. KindWorkload → ca.Workload (already bounded by correlator)
//  2. KindPod with pod label → correlation.ExtractWorkload(pod)
//  3. service_name → as-is (bounded by service count)
//  4. ca.Workload → last resort (may be a pod name in current correlator)
//
// The full pod identity is preserved separately in the `pod` annotation
// and the `summary` text — this only affects the indexed AM label.
func amWorkloadLabel(ca correlation.CorrelatedAlert, rep detection.Anomaly) string {
	if ca.Kind == correlation.KindWorkload {
		return ca.Workload
	}
	if pod := rep.Labels["pod"]; pod != "" {
		if w := correlation.ExtractWorkload(pod); w != "" {
			return w
		}
		return pod
	}
	if svc := rep.Labels["service_name"]; svc != "" {
		return svc
	}
	return ca.Workload
}

// buildReason produces a one-line explanation of why the alert fired,
// adapted to the detector type.
func buildReason(ca correlation.CorrelatedAlert, rep detection.Anomaly) string {
	if ca.Kind == correlation.KindWorkload {
		return fmt.Sprintf("workload pattern: %s anomalous on %d replicas (%s, z=%.1fσ)",
			rep.MetricName, ca.AffectedReplicas, rep.Detector, rep.Score)
	}
	return simpleReason(rep)
}

func simpleReason(a detection.Anomaly) string {
	switch a.Detector {
	case "static":
		return fmt.Sprintf("%s breached threshold (current %.4g)", a.MetricName, a.Value)
	case "adaptive":
		return fmt.Sprintf("%s spiked %.1fσ (current %.4g vs baseline %.4g)",
			a.MetricName, a.Score, a.Value, a.Mean)
	case "ml_isolation_forest":
		return fmt.Sprintf("multivariate anomaly (score %.2f)", a.Score)
	case "pattern":
		return fmt.Sprintf("event pattern: %s", a.MetricName)
	}
	if a.MetricName != "" {
		return a.MetricName
	}
	return "anomaly detected"
}

func (d *Dispatcher) buildAlert(a detection.Anomaly, kind correlation.Kind, identity, workloadLabel, reason string) alertmanagerAlert {
	ns := a.Labels["namespace"]

	annotations := map[string]string{
		"summary":         fmt.Sprintf("[%s] %s/%s — %s", a.Severity, ns, identity, reason),
		"reason":          reason,
		"metric":          a.MetricName,
		"current_value":   strconv.FormatFloat(a.Value, 'f', 4, 64),
		"baseline_mean":   strconv.FormatFloat(a.Mean, 'f', 4, 64),
		"baseline_stddev": strconv.FormatFloat(a.Stddev, 'f', 4, 64),
		"anomaly_score":   strconv.FormatFloat(a.Score, 'f', 2, 64),
		// Full identity (may be high-cardinality pod name) goes to annotations,
		// not labels — annotations are not indexed by Alertmanager.
		"identity": identity,
	}
	if pod := a.Labels["pod"]; pod != "" {
		annotations["pod"] = pod
	}

	if d.links != nil {
		if u := d.links.Grafana(a); u != "" {
			annotations["grafana_url"] = u
		}
		if u := d.links.Tempo(a); u != "" {
			annotations["tempo_url"] = u
		}
		if u := d.links.Loki(a); u != "" {
			annotations["loki_url"] = u
		}
		if u := d.links.Runbook(a.Detector); u != "" {
			annotations["runbook_url"] = u
		}
	}

	return alertmanagerAlert{
		Labels: map[string]string{
			"alertname": "AnomalyDetected",
			"severity":  a.Severity,
			"cluster":   d.cluster,
			"namespace": ns,
			// workloadLabel is always bounded (deployment / statefulset / daemonset /
			// service name). Full pod identity lives in annotations.
			"workload": workloadLabel,
			"kind":     string(kind),
			"signal":   a.Signal,
			"detector": a.Detector,
		},
		Annotations: annotations,
		StartsAt:    a.Timestamp,
	}
}

// attachContext renders enrichment + ML results as alert annotations.
func (d *Dispatcher) attachContext(a *alertmanagerAlert, ca correlation.CorrelatedAlert) {
	if len(ca.Enrichment.Results) > 0 {
		parts := []string{}
		for _, r := range ca.Enrichment.Results {
			key := "enrich_" + r.Name
			if r.Error != "" {
				a.Annotations[key] = "error: " + r.Error
				continue
			}
			val := strconv.FormatFloat(r.Value, 'f', 4, 64)
			a.Annotations[key] = val
			parts = append(parts, fmt.Sprintf("%s=%s", r.Name, val))
		}
		sort.Strings(parts)
		a.Annotations["context"] = strings.Join(parts, " | ")
		a.Labels["enriched"] = "true"
	}

	if ca.MLDetection != nil {
		a.Annotations["ml_score"] = strconv.FormatFloat(ca.MLDetection.Score, 'f', 4, 64)
		a.Annotations["ml_features"] = strconv.Itoa(ca.MLDetection.FeatureCount)
		if ca.MLDetection.IsAnomaly {
			a.Labels["ml_confirmed"] = "true"
			a.Annotations["ml_contributors"] = strings.Join(ca.MLDetection.Contributors, ",")
		}
	}
}

// attachWorkloadFields adds workload-pattern annotations when applicable.
func (d *Dispatcher) attachWorkloadFields(a *alertmanagerAlert, ca correlation.CorrelatedAlert) {
	if ca.Kind != correlation.KindWorkload {
		return
	}
	a.Annotations["affected_replicas"] = strconv.Itoa(ca.AffectedReplicas)
	a.Annotations["affected_pods"] = strings.Join(ca.AffectedPods, ",")
	a.Labels["workload_pattern"] = "true"
}
