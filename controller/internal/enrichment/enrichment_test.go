package enrichment

import (
	"testing"
)

// ─── Identity tests ───────────────────────────────────────────────────────────

func TestIdentityKind_Pod(t *testing.T) {
	id := Identity{Namespace: "prod", Pod: "api-abc-123"}
	if id.Kind() != KindPod {
		t.Errorf("expected KindPod, got %v", id.Kind())
	}
}

func TestIdentityKind_Service(t *testing.T) {
	id := Identity{ServiceName: "api-service"}
	if id.Kind() != KindService {
		t.Errorf("expected KindService, got %v", id.Kind())
	}
}

func TestIdentityKind_Unknown(t *testing.T) {
	id := Identity{}
	if id.Kind() != KindUnknown {
		t.Errorf("expected KindUnknown, got %v", id.Kind())
	}
}

func TestIdentityKind_PodWithoutNamespaceIsService(t *testing.T) {
	// pod without namespace → not a valid pod identity
	id := Identity{Pod: "api-abc-123", ServiceName: "api"}
	// namespace missing → KindPod check fails → falls to service check
	if id.Kind() != KindService {
		t.Errorf("pod without namespace should resolve to KindService, got %v", id.Kind())
	}
}

func TestIdentityString_Pod(t *testing.T) {
	id := Identity{Namespace: "prod", Pod: "api-abc"}
	s := id.String()
	if s != "pod=prod/api-abc" {
		t.Errorf("unexpected string: %q", s)
	}
}

func TestIdentityString_Service(t *testing.T) {
	id := Identity{ServiceName: "api-svc"}
	s := id.String()
	if s != "service=api-svc" {
		t.Errorf("unexpected string: %q", s)
	}
}

func TestIdentityString_Unknown(t *testing.T) {
	id := Identity{}
	if id.String() != "unknown" {
		t.Errorf("unexpected string for empty identity: %q", id.String())
	}
}

func TestIdentityCacheKey_Pod(t *testing.T) {
	id := Identity{Namespace: "prod", Pod: "api-abc"}
	key := id.CacheKey()
	if key == "" {
		t.Error("cache key should not be empty")
	}
	// Should be stable
	if id.CacheKey() != key {
		t.Error("cache key should be deterministic")
	}
}

func TestIdentityCacheKey_Service(t *testing.T) {
	id := Identity{ServiceName: "api-svc"}
	key := id.CacheKey()
	if key == "" {
		t.Error("cache key should not be empty for service identity")
	}
}

func TestIdentityCacheKey_Empty(t *testing.T) {
	id := Identity{}
	key := id.CacheKey()
	// enrichment: prefix still present
	if key == "" {
		t.Error("cache key should not be empty even for empty identity")
	}
}

func TestIdentityFromLabels_Pod(t *testing.T) {
	labels := map[string]string{
		"namespace":    "prod",
		"pod":          "api-7f8-x2k",
		"container":    "api",
		"service_name": "api",
	}
	id := IdentityFromLabels(labels)
	if id.Namespace != "prod" {
		t.Errorf("namespace: want prod, got %q", id.Namespace)
	}
	if id.Pod != "api-7f8-x2k" {
		t.Errorf("pod: want api-7f8-x2k, got %q", id.Pod)
	}
	if id.Container != "api" {
		t.Errorf("container: want api, got %q", id.Container)
	}
}

func TestIdentityFromLabels_AltKeys(t *testing.T) {
	labels := map[string]string{
		"k8s_namespace_name": "staging",
		"k8s_pod_name":       "worker-1",
		"k8s_node_name":      "node-1",
	}
	id := IdentityFromLabels(labels)
	if id.Namespace != "staging" {
		t.Errorf("namespace alt key: want staging, got %q", id.Namespace)
	}
	if id.Pod != "worker-1" {
		t.Errorf("pod alt key: want worker-1, got %q", id.Pod)
	}
	if id.Node != "node-1" {
		t.Errorf("node alt key: want node-1, got %q", id.Node)
	}
}

func TestIdentityFromLabels_Empty(t *testing.T) {
	id := IdentityFromLabels(map[string]string{})
	if id.Kind() != KindUnknown {
		t.Errorf("empty labels should produce unknown kind, got %v", id.Kind())
	}
}

// ─── Template substitution tests ──────────────────────────────────────────────

func TestSubstitute_AllVariables(t *testing.T) {
	id := Identity{
		Namespace:   "prod",
		Pod:         "api-abc-123",
		Container:   "api",
		ServiceName: "api-svc",
		Workload:    "api",
		Node:        "node-1",
	}
	query := "metric{namespace=\"$namespace\",pod=\"$pod\",container=\"$container\",service=\"$service_name\",workload=\"$workload\",node=\"$node\"}"
	got := substitute(query, id)
	want := `metric{namespace="prod",pod="api-abc-123",container="api",service="api-svc",workload="api",node="node-1"}`
	if got != want {
		t.Errorf("substitution mismatch:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestSubstitute_MissingVariables(t *testing.T) {
	id := Identity{Namespace: "prod"}
	query := "rate({namespace=\"$namespace\",pod=\"$pod\"}[5m])"
	got := substitute(query, id)
	// $pod should be replaced with empty string
	want := `rate({namespace="prod",pod=""}[5m])`
	if got != want {
		t.Errorf("missing variable substitution:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestSubstitute_NoVariables(t *testing.T) {
	id := Identity{Namespace: "prod"}
	query := "rate(http_requests_total[5m])"
	got := substitute(query, id)
	if got != query {
		t.Errorf("query without variables should be unchanged, got %q", got)
	}
}

func TestSubstitute_EmptyQuery(t *testing.T) {
	id := Identity{}
	got := substitute("", id)
	if got != "" {
		t.Errorf("empty query should return empty string, got %q", got)
	}
}
