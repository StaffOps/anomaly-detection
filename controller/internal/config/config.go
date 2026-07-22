package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode        string      `yaml:"mode"` // "controller" or "worker"
	Cluster     string      `yaml:"cluster"`
	Kubeconfig  string      `yaml:"kubeconfig"`
	Redis       Redis       `yaml:"redis"`
	Datasources Datasources `yaml:"datasources"`
	ML          ML          `yaml:"ml"`
	Controller  Controller  `yaml:"controller"`
	Worker      Worker      `yaml:"worker"`
	Baseline    Baseline    `yaml:"baseline"`
	Detection   Detection   `yaml:"detection"`
	Suppression Suppression `yaml:"suppression"`
	Enrichment  Enrichment  `yaml:"enrichment"`
	Links       Links       `yaml:"links"`
}

type Redis struct {
	Addr     string `yaml:"addr"`
	DB       int    `yaml:"db"`
	Password string `yaml:"password"`
}

type Datasources struct {
	Prometheus   DatasourceEndpoint `yaml:"prometheus"`
	Loki         DatasourceEndpoint `yaml:"loki"`
	Alertmanager DatasourceEndpoint `yaml:"alertmanager"`
}

type DatasourceEndpoint struct {
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

type ML struct {
	Endpoint string        `yaml:"endpoint"`
	Enabled  bool          `yaml:"enabled"`
	Timeout  time.Duration `yaml:"timeout"`
}

type Controller struct {
	JobInterval            time.Duration  `yaml:"job_interval"`
	CorrelationWindow      time.Duration  `yaml:"correlation_window"`
	Cooldown               time.Duration  `yaml:"cooldown"`
	FDRTarget              float64        `yaml:"fdr_target"`
	LeaseName              string         `yaml:"lease_name"`
	LeaseNamespace         string         `yaml:"lease_namespace"`
	MetricsPort            int            `yaml:"metrics_port"`
	WorkerEndpoint         string         `yaml:"worker_endpoint"`
	WorkloadPatternMinPods int            `yaml:"workload_pattern_min_pods"`
	LeaderElection         LeaderElection `yaml:"leader_election"`
}

// LeaderElection controls K8s Lease-based leader election for HA.
//
// When Enabled=false (default for local docker-compose), the controller acts
// as if it's always the leader — single-replica behavior. When Enabled=true
// (cluster deploy), N replicas race for the Lease and only the holder runs
// the detection cycle. Followers stay warm and take over on lease expiry.
//
// Identity defaults to POD_NAME env var (set via downward API) or hostname.
// The default lease/lock parameters follow Kubernetes controller-manager
// conventions: 15s lease duration, 10s renew deadline, 2s retry period —
// gives a ~17s worst-case failover window.
type LeaderElection struct {
	Enabled       bool          `yaml:"enabled"`
	Identity      string        `yaml:"identity"`       // defaults to POD_NAME or hostname
	LeaseDuration time.Duration `yaml:"lease_duration"` // default 15s
	RenewDeadline time.Duration `yaml:"renew_deadline"` // default 10s
	RetryPeriod   time.Duration `yaml:"retry_period"`   // default 2s
}

type Worker struct {
	GRPCPort    int `yaml:"grpc_port"`
	MetricsPort int `yaml:"metrics_port"`
	Concurrency int `yaml:"concurrency"`
}

type Baseline struct {
	WindowSize      int      `yaml:"window_size"`
	EWMAAlpha       float64  `yaml:"ewma_alpha"`
	ZScoreThreshold float64  `yaml:"zscore_threshold"`
	PoisonThreshold float64  `yaml:"poison_threshold"`
	WarmUpSamples   int      `yaml:"warm_up_samples"`
	SeasonalMinDays int      `yaml:"seasonal_min_days"`
	EphemeralLabels []string `yaml:"ephemeral_labels"`
}

type Detection struct {
	StaticRules     []StaticRule     `yaml:"static_rules"`
	AdaptiveMetrics []AdaptiveMetric `yaml:"adaptive_metrics"`
	LogPatterns     []LogPattern     `yaml:"log_patterns"`
	EventPatterns   []string         `yaml:"event_patterns"`
}

type StaticRule struct {
	Name      string  `yaml:"name"`
	Query     string  `yaml:"query"`
	Threshold float64 `yaml:"threshold"`
	Operator  string  `yaml:"operator"`
	Severity  string  `yaml:"severity"`
}

type AdaptiveMetric struct {
	Name    string   `yaml:"name"`
	Query   string   `yaml:"query"`
	GroupBy []string `yaml:"group_by"`
	// Direction of badness. The adaptive detector fires on |z| (symmetric), but
	// most metrics are only anomalous in ONE direction: latency/errors/queue
	// depth are bad when they RISE, ready-replicas/throughput when they FALL.
	// Declaring it drops the false positives from the harmless direction (e.g.
	// latency improving). One of: "up_bad", "down_bad", "both_bad".
	// Empty = "both_bad" (backward-compatible: fire on any deviation).
	Direction string `yaml:"direction"`
	// MinValue is an absolute floor the current reading must reach for the
	// anomaly to fire, applied on top of the z-score test. It fixes the
	// near-zero-baseline false positive: a gauge that idles at ~0.1 (e.g.
	// http_client_active_requests on a quiet service) has a tiny stddev, so any
	// value of a few units is a large z-score — statistically anomalous but
	// operationally noise. With min_value the rule fires only when the deviation
	// is BOTH significant (z > threshold) AND the reading crosses a floor of
	// operational relevance. Zero (the default) disables the floor —
	// backward-compatible.
	MinValue float64 `yaml:"min_value"`
}

type LogPattern struct {
	Name    string   `yaml:"name"`
	Query   string   `yaml:"query"`
	GroupBy []string `yaml:"group_by"`
	Type    string   `yaml:"type"` // "rate" (default) or "pattern_match"
}

// Suppression buckets. The YAML uses CSV strings (env-friendly) and the loader
// splits them into the Lists below. Operators set EXCLUDE_NAMESPACES_CSV and
// EXCLUDE_STATIC_ONLY_CSV env vars; the org-specific values never live in code.
type Suppression struct {
	ExcludeNamespacesCSV string `yaml:"exclude_namespaces_csv"`
	ExcludeStaticOnlyCSV string `yaml:"exclude_static_only_csv"`
	// Workloads whose ADAPTIVE (EWMA Z-Score) detections are suppressed while
	// static/log detections still fire. For inherently bursty infra (message
	// brokers, telemetry collectors, service mesh) that the adaptive detector
	// flags constantly — the dominant false-positive source. Matched against the
	// workload extracted from the pod name (see correlation.ExtractWorkload).
	ExcludeAdaptiveWorkloadsCSV string `yaml:"exclude_adaptive_workloads_csv"`

	// Populated by setDefaults from the CSVs.
	ExcludeNamespaces        []string `yaml:"-"`
	ExcludeStaticOnly        []string `yaml:"-"`
	ExcludeAdaptiveWorkloads []string `yaml:"-"`
}

// Enrichment configures contextual queries that fan out when an anomaly fires.
//
// When the correlator emits a CorrelatedAlert for a workload, the enrichment
// engine runs the matching bundle (PodBundle for pod-level, ServiceBundle for
// service-level) with template-substituted queries (e.g. $pod, $namespace,
// $service_name) to build a diagnostic context attached to the alert payload.
type Enrichment struct {
	Enabled       bool              `yaml:"enabled"`
	CacheTTL      time.Duration     `yaml:"cache_ttl"`      // dedup repeat queries
	QueryTimeout  time.Duration     `yaml:"query_timeout"`  // per-query timeout
	MaxConcurrent int               `yaml:"max_concurrent"` // cap parallelism
	PodBundle     []EnrichmentQuery `yaml:"pod_bundle"`
	ServiceBundle []EnrichmentQuery `yaml:"service_bundle"`
}

// EnrichmentQuery is a single query in an enrichment bundle.
// Source can be "prometheus" (Prometheus-compatible TSDB) or "loki" (Loki). Default: prometheus.
type EnrichmentQuery struct {
	Name   string `yaml:"name"`
	Query  string `yaml:"query"`
	Source string `yaml:"source"` // "prometheus" or "loki"
}

// Links configures URL templates rendered into Alertmanager annotations
// so operators can jump from an alert to Grafana/Tempo/Loki/runbooks in one click.
type Links struct {
	GrafanaBaseURL            string `yaml:"grafana_base_url"`
	TempoBaseURL              string `yaml:"tempo_base_url"`
	LokiBaseURL               string `yaml:"loki_base_url"`
	RunbookBaseURL            string `yaml:"runbook_base_url"`
	GrafanaPromDatasourceUID  string `yaml:"grafana_prometheus_datasource_uid"`
	GrafanaTempoDatasourceUID string `yaml:"grafana_tempo_datasource_uid"`
	GrafanaLokiDatasourceUID  string `yaml:"grafana_loki_datasource_uid"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand ${VAR} and ${VAR:default} placeholders before YAML parse.
	// This is the single source of truth for runtime configuration —
	// no URLs or endpoints are hardcoded in code.
	expanded, err := expandEnv(string(data))
	if err != nil {
		return nil, fmt.Errorf("expand env: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DirectionMap returns rule name → direction-of-badness for the adaptive rules
// that declare one. Rules with an empty direction are omitted (the filter treats
// a missing entry as "both_bad", the permissive default).
//
// Both the controller cycle and the replay engine need this; deriving it in one
// place keeps the two post-filter paths from drifting apart, which would show up
// as replay and production disagreeing on FP counts.
func (d Detection) DirectionMap() map[string]string {
	m := make(map[string]string, len(d.AdaptiveMetrics))
	for _, am := range d.AdaptiveMetrics {
		if am.Direction != "" {
			m[am.Name] = am.Direction
		}
	}
	return m
}

// FloorMap returns rule name → min_value for the adaptive rules that declare a
// positive floor. See DirectionMap for why this lives here.
func (d Detection) FloorMap() map[string]float64 {
	m := make(map[string]float64, len(d.AdaptiveMetrics))
	for _, am := range d.AdaptiveMetrics {
		if am.MinValue > 0 {
			m[am.Name] = am.MinValue
		}
	}
	return m
}

// validate rejects rule combinations that are silently wrong at runtime.
//
// min_value + down_bad is the one that matters: the floor drops readings BELOW
// it, but a down_bad rule fires precisely because the reading fell. Combining
// them suppresses exactly the anomaly the rule exists to catch — and the lower
// the reading (the worse the incident), the more certain the suppression. There
// is no sane reading of "minimum value" for a metric whose badness is downward,
// so this fails fast at load instead of quietly detecting nothing.
func validate(cfg *Config) error {
	for _, am := range cfg.Detection.AdaptiveMetrics {
		if am.MinValue > 0 && am.Direction == "down_bad" {
			return fmt.Errorf(
				"adaptive metric %q: min_value cannot be combined with direction: down_bad — "+
					"the floor would drop the low readings the rule is meant to catch", am.Name)
		}
	}
	return nil
}

// envVarPattern matches ${NAME} and ${NAME:default}. Default may contain any
// character except `}`. Names must match POSIX env var naming.
var envVarPattern = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)(?::([^}]*))?\}`)

// expandEnv substitutes ${VAR} and ${VAR:default} placeholders using os.Getenv.
// Returns an error when a variable is missing AND no default was provided —
// fail-fast is preferable to silently shipping empty strings as endpoints.
//
// Lines starting with `#` (after optional whitespace) are treated as YAML
// comments and skipped entirely so docs can mention placeholder syntax
// without triggering substitution.
func expandEnv(s string) (string, error) {
	var missing []string
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue // YAML comment — leave verbatim
		}
		lines[i] = envVarPattern.ReplaceAllStringFunc(line, func(match string) string {
			groups := envVarPattern.FindStringSubmatch(match)
			name := groups[1]
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			if strings.Contains(match, ":") {
				return groups[2] // default branch (may be empty)
			}
			missing = append(missing, name)
			return match
		})
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("required env vars not set: %s", strings.Join(missing, ", "))
	}
	return strings.Join(lines, "\n"), nil
}

func setDefaults(cfg *Config) {
	if cfg.Cluster == "" {
		cfg.Cluster = os.Getenv("CLUSTER_NAME")
		if cfg.Cluster == "" {
			cfg.Cluster = "unknown"
		}
	}
	if cfg.Controller.JobInterval == 0 {
		cfg.Controller.JobInterval = 30 * time.Second
	}
	if cfg.Controller.CorrelationWindow == 0 {
		cfg.Controller.CorrelationWindow = 2 * time.Minute
	}
	if cfg.Controller.Cooldown == 0 {
		cfg.Controller.Cooldown = 5 * time.Minute
	}
	if cfg.Controller.MetricsPort == 0 {
		cfg.Controller.MetricsPort = 8080
	}
	if cfg.Controller.WorkloadPatternMinPods == 0 {
		cfg.Controller.WorkloadPatternMinPods = 3
	}
	// Leader election timing defaults match k8s controller-manager conventions.
	// Worst-case failover ≈ LeaseDuration + RetryPeriod (15s + 2s = 17s).
	if cfg.Controller.LeaderElection.LeaseDuration == 0 {
		cfg.Controller.LeaderElection.LeaseDuration = 15 * time.Second
	}
	if cfg.Controller.LeaderElection.RenewDeadline == 0 {
		cfg.Controller.LeaderElection.RenewDeadline = 10 * time.Second
	}
	if cfg.Controller.LeaderElection.RetryPeriod == 0 {
		cfg.Controller.LeaderElection.RetryPeriod = 2 * time.Second
	}
	if cfg.Controller.LeaseName == "" {
		cfg.Controller.LeaseName = "staffops-ad-controller"
	}
	if cfg.Controller.LeaseNamespace == "" {
		cfg.Controller.LeaseNamespace = "monitoring"
	}
	if cfg.Worker.GRPCPort == 0 {
		cfg.Worker.GRPCPort = 50052
	}
	if cfg.Worker.MetricsPort == 0 {
		cfg.Worker.MetricsPort = 8081
	}
	if cfg.Worker.Concurrency == 0 {
		cfg.Worker.Concurrency = 5
	}
	if cfg.Baseline.WindowSize == 0 {
		cfg.Baseline.WindowSize = 60
	}
	if cfg.Baseline.EWMAAlpha == 0 {
		cfg.Baseline.EWMAAlpha = 0.3
	}
	if cfg.Baseline.ZScoreThreshold == 0 {
		cfg.Baseline.ZScoreThreshold = 3.0
	}
	if cfg.Baseline.WarmUpSamples == 0 {
		cfg.Baseline.WarmUpSamples = 60
	}
	if cfg.Baseline.SeasonalMinDays == 0 {
		cfg.Baseline.SeasonalMinDays = 7
	}
	if cfg.Datasources.Prometheus.Timeout == 0 {
		cfg.Datasources.Prometheus.Timeout = 10 * time.Second
	}
	if cfg.Datasources.Loki.Timeout == 0 {
		cfg.Datasources.Loki.Timeout = 15 * time.Second
	}
	if cfg.ML.Timeout == 0 {
		cfg.ML.Timeout = 5 * time.Second
	}
	if cfg.Enrichment.CacheTTL == 0 {
		cfg.Enrichment.CacheTTL = 10 * time.Second
	}
	if cfg.Enrichment.QueryTimeout == 0 {
		cfg.Enrichment.QueryTimeout = 5 * time.Second
	}
	if cfg.Enrichment.MaxConcurrent == 0 {
		cfg.Enrichment.MaxConcurrent = 5
	}
	// Note: Links.* URLs are NOT defaulted here on purpose.
	// All endpoints come from env vars (12-factor). Empty values mean
	// the corresponding link annotation simply won't be emitted.

	// Parse suppression CSVs into list slices. Empty CSV → empty list.
	cfg.Suppression.ExcludeNamespaces = splitCSV(cfg.Suppression.ExcludeNamespacesCSV)
	cfg.Suppression.ExcludeStaticOnly = splitCSV(cfg.Suppression.ExcludeStaticOnlyCSV)
	cfg.Suppression.ExcludeAdaptiveWorkloads = splitCSV(cfg.Suppression.ExcludeAdaptiveWorkloadsCSV)
}

// splitCSV parses a comma-separated string, trimming whitespace and dropping empty entries.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
