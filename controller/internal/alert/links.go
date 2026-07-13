package alert

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
)

// LinkBuilder renders deep links into Grafana/Tempo/Loki/runbooks
// for inclusion in Alertmanager annotations.
//
// All builders are best-effort: if required labels are missing, they fall
// back to broader queries. Empty-string returns mean "no link applicable".
type LinkBuilder struct {
	cfg config.Links
}

func NewLinkBuilder(cfg config.Links) *LinkBuilder {
	return &LinkBuilder{cfg: cfg}
}

// Grafana returns a Grafana Explore URL focused on the workload, ±15min
// around the anomaly timestamp, querying a Prometheus-compatible TSDB via the configured
// datasource UID.
func (b *LinkBuilder) Grafana(a detection.Anomaly) string {
	if b.cfg.GrafanaBaseURL == "" {
		return ""
	}
	expr := buildPromQLExpr(a)
	from := a.Timestamp.Add(-15 * time.Minute).UnixMilli()
	to := a.Timestamp.Add(15 * time.Minute).UnixMilli()

	// Grafana Explore URL format (left-pane JSON-encoded).
	pane := fmt.Sprintf(
		`{"datasource":"%s","queries":[{"refId":"A","expr":%q,"datasource":{"type":"prometheus","uid":"%s"}}],"range":{"from":"%d","to":"%d"}}`,
		b.cfg.GrafanaPromDatasourceUID, expr, b.cfg.GrafanaPromDatasourceUID, from, to,
	)
	q := url.Values{}
	q.Set("panes", `{"a":`+pane+`}`)
	q.Set("schemaVersion", "1")
	q.Set("orgId", "1")
	return strings.TrimRight(b.cfg.GrafanaBaseURL, "/") + "/explore?" + q.Encode()
}

// Tempo returns a Grafana Explore URL targeting Tempo with a TraceQL search
// scoped to the anomaly's service or pod.
func (b *LinkBuilder) Tempo(a detection.Anomaly) string {
	if b.cfg.TempoBaseURL == "" {
		return ""
	}
	tq := buildTraceQL(a)
	if tq == "" {
		return ""
	}
	from := a.Timestamp.Add(-15 * time.Minute).UnixMilli()
	to := a.Timestamp.Add(15 * time.Minute).UnixMilli()

	pane := fmt.Sprintf(
		`{"datasource":"%s","queries":[{"refId":"A","queryType":"traceql","query":%q,"datasource":{"type":"tempo","uid":"%s"}}],"range":{"from":"%d","to":"%d"}}`,
		b.cfg.GrafanaTempoDatasourceUID, tq, b.cfg.GrafanaTempoDatasourceUID, from, to,
	)
	q := url.Values{}
	q.Set("panes", `{"a":`+pane+`}`)
	q.Set("schemaVersion", "1")
	q.Set("orgId", "1")
	return strings.TrimRight(b.cfg.TempoBaseURL, "/") + "/explore?" + q.Encode()
}

// Loki returns a Grafana Explore URL targeting Loki with a LogQL query
// scoped to the anomaly's pod or service.
func (b *LinkBuilder) Loki(a detection.Anomaly) string {
	if b.cfg.LokiBaseURL == "" {
		return ""
	}
	expr := buildLogQL(a)
	if expr == "" {
		return ""
	}
	from := a.Timestamp.Add(-5 * time.Minute).UnixMilli()
	to := a.Timestamp.Add(5 * time.Minute).UnixMilli()

	pane := fmt.Sprintf(
		`{"datasource":"%s","queries":[{"refId":"A","expr":%q,"datasource":{"type":"loki","uid":"%s"}}],"range":{"from":"%d","to":"%d"}}`,
		b.cfg.GrafanaLokiDatasourceUID, expr, b.cfg.GrafanaLokiDatasourceUID, from, to,
	)
	q := url.Values{}
	q.Set("panes", `{"a":`+pane+`}`)
	q.Set("schemaVersion", "1")
	q.Set("orgId", "1")
	return strings.TrimRight(b.cfg.LokiBaseURL, "/") + "/explore?" + q.Encode()
}

// Runbook returns a runbook URL for the given detector. Returns empty if
// no base is configured.
func (b *LinkBuilder) Runbook(detector string) string {
	if b.cfg.RunbookBaseURL == "" || detector == "" {
		return ""
	}
	return strings.TrimRight(b.cfg.RunbookBaseURL, "/") + "/" + detector
}

// buildPromQLExpr crafts a PromQL filter using the most specific labels available.
// Falls back to the raw metric name if no usable labels are present.
func buildPromQLExpr(a detection.Anomaly) string {
	if a.MetricName == "" {
		return "up"
	}
	filters := []string{}
	if v := a.Labels["namespace"]; v != "" {
		filters = append(filters, fmt.Sprintf(`namespace="%s"`, v))
	}
	if v := a.Labels["pod"]; v != "" {
		filters = append(filters, fmt.Sprintf(`pod="%s"`, v))
	}
	if v := a.Labels["service_name"]; v != "" && len(filters) == 0 {
		filters = append(filters, fmt.Sprintf(`service_name="%s"`, v))
	}
	if len(filters) == 0 {
		return a.MetricName
	}
	return fmt.Sprintf("%s{%s}", a.MetricName, strings.Join(filters, ","))
}

func buildTraceQL(a detection.Anomaly) string {
	if v := a.Labels["service_name"]; v != "" {
		return fmt.Sprintf(`{ resource.service.name = "%s" && status = error }`, v)
	}
	if v := a.Labels["pod"]; v != "" {
		return fmt.Sprintf(`{ resource.k8s.pod.name = "%s" }`, v)
	}
	return ""
}

func buildLogQL(a detection.Anomaly) string {
	ns := a.Labels["namespace"]
	pod := a.Labels["pod"]
	svc := a.Labels["service_name"]

	switch {
	case ns != "" && pod != "":
		return fmt.Sprintf(`{k8s_namespace_name="%s",k8s_pod_name="%s"}`, ns, pod)
	case svc != "":
		return fmt.Sprintf(`{service_name="%s"}`, svc)
	case ns != "":
		return fmt.Sprintf(`{k8s_namespace_name="%s"}`, ns)
	}
	return ""
}
