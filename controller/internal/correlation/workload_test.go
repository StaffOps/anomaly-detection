package correlation

import "testing"

// Test data uses generic K8s naming patterns (no organization-specific names).
// Each pattern below is realistic but agnostic — the regex must work the same
// regardless of the workload's actual purpose.
func TestExtractWorkload(t *testing.T) {
	cases := []struct {
		name string
		pod  string
		want string
	}{
		// Deployment patterns: <workload>-<rs_hash 8-10 hex>-<pod_hash 5 alnum>
		{"deployment_typical", "my-app-558596ddb7-4db97", "my-app"},
		{"deployment_short_name", "api-745759b65d-8x7b7", "api"},
		{"deployment_long_hyphenated", "service-with-many-hyphens-1780173900-deploy-1779774225", "service-with-many-hyphens-1780173900-deploy"},
		{"deployment_8char_hash", "ingress-controller-64fdb4b6bb-strxb", "ingress-controller"},

		// StatefulSet patterns: <workload>-<N>
		{"statefulset_zero", "datastore-0", "datastore"},
		{"statefulset_two_digits", "queue-12", "queue"},
		{"statefulset_simple", "redis-0", "redis"},

		// DaemonSet patterns: <workload>-<5-char-suffix>
		{"daemonset_typical", "node-agent-fhvpx", "node-agent"},
		{"daemonset_long_name", "log-collector-c8cx8", "log-collector"},
		{"daemonset_short", "agent-2g986", "agent"},

		// Edge cases
		{"empty_input", "", ""},
		{"single_word", "redis", "redis"},
		{"two_words_no_match", "my-app", "my-app"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractWorkload(tc.pod)
			if got != tc.want {
				t.Errorf("ExtractWorkload(%q) = %q; want %q", tc.pod, got, tc.want)
			}
		})
	}
}

func TestExtractWorkload_DistinctReplicasShareWorkload(t *testing.T) {
	// Sibling pods of same Deployment must collapse to the same workload key
	pods := []string{
		"my-app-558596ddb7-4db97",
		"my-app-558596ddb7-p5mq9",
		"my-app-558596ddb7-mmhnf",
	}
	want := "my-app"
	for _, p := range pods {
		if got := ExtractWorkload(p); got != want {
			t.Errorf("expected all replicas to map to %q, got %q for %q", want, got, p)
		}
	}
}

func TestExtractWorkload_StatefulSetReplicas(t *testing.T) {
	// Each StatefulSet replica → same workload prefix
	pods := []string{"datastore-0", "datastore-1", "datastore-2"}
	want := "datastore"
	for _, p := range pods {
		if got := ExtractWorkload(p); got != want {
			t.Errorf("expected %q, got %q for %q", want, got, p)
		}
	}
}
