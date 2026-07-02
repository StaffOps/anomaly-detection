package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── splitCSV ─────────────────────────────────────────────────────────────────

func TestSplitCSV_Empty(t *testing.T) {
	got := splitCSV("")
	if got != nil {
		t.Errorf("empty string should return nil, got %v", got)
	}
}

func TestSplitCSV_Single(t *testing.T) {
	got := splitCSV("kube-system")
	if len(got) != 1 || got[0] != "kube-system" {
		t.Errorf("single value: want [kube-system], got %v", got)
	}
}

func TestSplitCSV_Multiple(t *testing.T) {
	got := splitCSV("kube-system,monitoring,kube-public")
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(got), got)
	}
	if got[0] != "kube-system" || got[1] != "monitoring" || got[2] != "kube-public" {
		t.Errorf("unexpected values: %v", got)
	}
}

func TestSplitCSV_TrimsWhitespace(t *testing.T) {
	got := splitCSV(" kube-system , monitoring ")
	if len(got) != 2 {
		t.Fatalf("expected 2 items after trim, got %d: %v", len(got), got)
	}
	if got[0] != "kube-system" || got[1] != "monitoring" {
		t.Errorf("whitespace not trimmed: %v", got)
	}
}

func TestSplitCSV_DropsEmptyEntries(t *testing.T) {
	got := splitCSV(",kube-system,,monitoring,")
	if len(got) != 2 {
		t.Fatalf("expected 2 items (empty entries dropped), got %d: %v", len(got), got)
	}
}

// ─── setDefaults ─────────────────────────────────────────────────────────────

func TestSetDefaults_SetsAllZeroValues(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	if cfg.Controller.JobInterval != 30*time.Second {
		t.Errorf("JobInterval default: want 30s, got %v", cfg.Controller.JobInterval)
	}
	if cfg.Controller.CorrelationWindow != 2*time.Minute {
		t.Errorf("CorrelationWindow default: want 2m, got %v", cfg.Controller.CorrelationWindow)
	}
	if cfg.Controller.Cooldown != 5*time.Minute {
		t.Errorf("Cooldown default: want 5m, got %v", cfg.Controller.Cooldown)
	}
	if cfg.Controller.MetricsPort != 8080 {
		t.Errorf("MetricsPort default: want 8080, got %d", cfg.Controller.MetricsPort)
	}
	if cfg.Worker.GRPCPort != 50052 {
		t.Errorf("GRPCPort default: want 50052, got %d", cfg.Worker.GRPCPort)
	}
	if cfg.Baseline.EWMAAlpha != 0.3 {
		t.Errorf("EWMAAlpha default: want 0.3, got %v", cfg.Baseline.EWMAAlpha)
	}
	if cfg.Baseline.ZScoreThreshold != 3.0 {
		t.Errorf("ZScoreThreshold default: want 3.0, got %v", cfg.Baseline.ZScoreThreshold)
	}
	if cfg.Baseline.WarmUpSamples != 60 {
		t.Errorf("WarmUpSamples default: want 60, got %d", cfg.Baseline.WarmUpSamples)
	}
}

func TestSetDefaults_DoesNotOverrideExisting(t *testing.T) {
	cfg := &Config{}
	cfg.Controller.JobInterval = 10 * time.Second
	cfg.Baseline.EWMAAlpha = 0.5
	setDefaults(cfg)

	if cfg.Controller.JobInterval != 10*time.Second {
		t.Errorf("JobInterval should not be overridden: got %v", cfg.Controller.JobInterval)
	}
	if cfg.Baseline.EWMAAlpha != 0.5 {
		t.Errorf("EWMAAlpha should not be overridden: got %v", cfg.Baseline.EWMAAlpha)
	}
}

func TestSetDefaults_ClusterFallsBackToEnv(t *testing.T) {
	t.Setenv("CLUSTER_NAME", "test-cluster")
	cfg := &Config{}
	setDefaults(cfg)
	if cfg.Cluster != "test-cluster" {
		t.Errorf("Cluster should come from CLUSTER_NAME env, got %q", cfg.Cluster)
	}
}

func TestSetDefaults_ClusterDefaultsToUnknown(t *testing.T) {
	os.Unsetenv("CLUSTER_NAME")
	cfg := &Config{}
	setDefaults(cfg)
	if cfg.Cluster != "unknown" {
		t.Errorf("Cluster should default to 'unknown', got %q", cfg.Cluster)
	}
}

func TestSetDefaults_SetsSuppressionLists(t *testing.T) {
	cfg := &Config{}
	cfg.Suppression.ExcludeNamespacesCSV = "kube-system,monitoring"
	cfg.Suppression.ExcludeStaticOnlyCSV = "batch"
	setDefaults(cfg)

	if len(cfg.Suppression.ExcludeNamespaces) != 2 {
		t.Errorf("ExcludeNamespaces: expected 2, got %d", len(cfg.Suppression.ExcludeNamespaces))
	}
	if len(cfg.Suppression.ExcludeStaticOnly) != 1 {
		t.Errorf("ExcludeStaticOnly: expected 1, got %d", len(cfg.Suppression.ExcludeStaticOnly))
	}
}

// ─── Load ─────────────────────────────────────────────────────────────────────

func TestLoad_ValidMinimalConfig(t *testing.T) {
	t.Setenv("VM_URL", "http://vm:9090")
	t.Setenv("LOKI_URL", "http://loki:3100")
	t.Setenv("ALERTMANAGER_URL", "http://am:9093")

	content := `
cluster: test
datasources:
  prometheus:
    url: ${VM_URL}
  loki:
    url: ${LOKI_URL}
  alertmanager:
    url: ${ALERTMANAGER_URL}
`
	f := writeTempConfig(t, content)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cluster != "test" {
		t.Errorf("cluster: want test, got %q", cfg.Cluster)
	}
	if cfg.Datasources.Prometheus.URL != "http://vm:9090" {
		t.Errorf("VM URL: want http://vm:9090, got %q", cfg.Datasources.Prometheus.URL)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_MissingRequiredEnvVar(t *testing.T) {
	os.Unsetenv("REQUIRED_MISSING_VAR_12345")
	content := "url: ${REQUIRED_MISSING_VAR_12345}\n"
	f := writeTempConfig(t, content)
	_, err := Load(f)
	if err == nil {
		t.Error("expected error for missing required env var")
	}
}

func TestLoad_SetsDefaults(t *testing.T) {
	content := "cluster: test\n"
	f := writeTempConfig(t, content)
	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Defaults should be applied
	if cfg.Controller.JobInterval == 0 {
		t.Error("JobInterval should have a default value")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
