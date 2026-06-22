package baseline

import (
	"context"
	"testing"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

func TestExtractWorkload(t *testing.T) {
	tests := []struct {
		name string
		pod  string
		want string
	}{
		{"deployment simple", "myapp-558596ddb7-4db97", "myapp"},
		{"deployment with hyphens", "my-cool-app-558596ddb7-4db97", "my-cool-app"},
		{"deployment 8-char hash", "myapp-5f8a9b2c-xk7q2", "myapp"},
		{"statefulset index 0", "redis-0", "redis"},
		{"statefulset index 3", "datastore-3", "datastore"},
		{"statefulset multi-digit", "kafka-12", "kafka"},
		{"daemonset", "node-agent-fhvpx", "node-agent"},
		{"bare pod no pattern", "custom-pod-name", "custom-pod-name"},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractWorkload(tt.pod)
			if got != tt.want {
				t.Errorf("extractWorkload(%q) = %q, want %q", tt.pod, got, tt.want)
			}
		})
	}
}

func TestNormalizeLabels(t *testing.T) {
	store := NewStore(noopHash{}, config.Baseline{
		EphemeralLabels: []string{"instance", "pod_template_hash"},
	})

	tests := []struct {
		name   string
		labels map[string]string
		want   map[string]string
	}{
		{
			name:   "nil labels returns nil",
			labels: nil,
			want:   nil,
		},
		{
			name:   "no pod label preserves non-ephemeral",
			labels: map[string]string{"namespace": "default", "job": "myapp"},
			want:   map[string]string{"namespace": "default", "job": "myapp"},
		},
		{
			name:   "pod replaced with workload name",
			labels: map[string]string{"pod": "myapp-558596ddb7-4db97", "namespace": "default"},
			want:   map[string]string{"pod": "myapp", "namespace": "default"},
		},
		{
			name:   "empty pod value preserved as-is",
			labels: map[string]string{"pod": "", "namespace": "default"},
			want:   map[string]string{"pod": "", "namespace": "default"},
		},
		{
			name:   "ephemeral labels dropped",
			labels: map[string]string{"namespace": "default", "instance": "10.0.0.1:8080", "pod_template_hash": "abc123"},
			want:   map[string]string{"namespace": "default"},
		},
		{
			name:   "combined: pod normalized + ephemeral dropped",
			labels: map[string]string{"pod": "my-cool-app-7f8b9c6d5e-zzzzz", "instance": "10.0.0.5:8080", "namespace": "prod"},
			want:   map[string]string{"pod": "my-cool-app", "namespace": "prod"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.normalizeLabels(tt.labels)
			if tt.want == nil {
				if got != nil {
					t.Errorf("want nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("len mismatch: got %v, want %v", got, tt.want)
				return
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("missing key %q", k)
				} else if gotV != wantV {
					t.Errorf("key %q = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestNormalizeLabels_NoEphemeralConfig(t *testing.T) {
	// When EphemeralLabels is empty, no labels are stripped.
	store := NewStore(noopHash{}, config.Baseline{})
	labels := map[string]string{"instance": "10.0.0.1:8080", "pod": "redis-0"}
	got := store.normalizeLabels(labels)

	if got["instance"] != "10.0.0.1:8080" {
		t.Errorf("instance should be preserved when no ephemeral config, got %v", got)
	}
	if got["pod"] != "redis" {
		t.Errorf("pod should still be normalized to workload, got %q", got["pod"])
	}
}

func TestKeyStability_SameWorkload(t *testing.T) {
	store := NewStore(noopHash{}, config.Baseline{
		EphemeralLabels: []string{"instance"},
	})

	metric := "http_request_duration_seconds"
	labelsA := map[string]string{"pod": "myapp-558596ddb7-4db97", "namespace": "default"}
	labelsB := map[string]string{"pod": "myapp-7f8b9c6d5e-p5mq9", "namespace": "default"}

	keyA := baselineKey(metric, store.normalizeLabels(labelsA))
	keyB := baselineKey(metric, store.normalizeLabels(labelsB))

	if keyA != keyB {
		t.Errorf("same workload should produce same key:\n  A: %s\n  B: %s", keyA, keyB)
	}
}

func TestKeyStability_DifferentWorkloads(t *testing.T) {
	store := NewStore(noopHash{}, config.Baseline{})

	metric := "http_request_duration_seconds"
	labelsA := map[string]string{"pod": "frontend-558596ddb7-4db97", "namespace": "default"}
	labelsB := map[string]string{"pod": "backend-558596ddb7-4db97", "namespace": "default"}

	keyA := baselineKey(metric, store.normalizeLabels(labelsA))
	keyB := baselineKey(metric, store.normalizeLabels(labelsB))

	if keyA == keyB {
		t.Errorf("different workloads should produce different keys, both got: %s", keyA)
	}
}

// noopHash satisfies hashStore without needing Redis.
type noopHash struct{}

func (noopHash) HSet(_ context.Context, _ string, _ map[string]interface{}) error { return nil }
func (noopHash) HGetAll(_ context.Context, _ string) (map[string]string, error)   { return nil, nil }
