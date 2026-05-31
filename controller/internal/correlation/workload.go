package correlation

import "regexp"

// Compiled once at package init.
var (
	// Deployment pattern: <workload>-<rs_hash>-<pod_hash>
	// Example: my-app-558596ddb7-4db97
	// rs_hash is typically 8-10 hex chars; pod_hash is 5 alphanumeric.
	deploymentPattern = regexp.MustCompile(`^(.+)-[a-f0-9]{8,10}-[a-z0-9]{5}$`)

	// StatefulSet pattern: <workload>-<N>
	// Example: datastore-0
	statefulSetPattern = regexp.MustCompile(`^(.+)-(\d+)$`)

	// DaemonSet pattern: <workload>-<random_suffix>
	// Example: node-agent-fhvpx (5 lowercase alphanumeric)
	daemonSetPattern = regexp.MustCompile(`^(.+)-[a-z0-9]{5}$`)
)

// ExtractWorkload returns the workload (Deployment/StatefulSet/DaemonSet) name
// derived from the pod name. Returns the original pod name when no pattern matches.
//
// The function is intentionally regex-only (no K8s API calls) to keep correlation
// hot-path fast. It correctly handles ~95% of real-world K8s naming. False positives
// (e.g., legitimate hyphenated names without owner) just collapse a single pod into
// a "workload" of size 1, which doesn't trigger pattern detection (≥3 pods needed).
//
// Patterns tried, in order of specificity:
//   - Deployment: <name>-<rsHash>-<podHash>  → returns <name>
//   - StatefulSet: <name>-<N>                → returns <name>
//   - DaemonSet: <name>-<5-char-suffix>      → returns <name>
//   - Otherwise: returns pod unchanged (treated as bare pod / unknown)
func ExtractWorkload(pod string) string {
	if pod == "" {
		return ""
	}
	if m := deploymentPattern.FindStringSubmatch(pod); len(m) > 1 {
		return m[1]
	}
	if m := statefulSetPattern.FindStringSubmatch(pod); len(m) > 1 {
		return m[1]
	}
	if m := daemonSetPattern.FindStringSubmatch(pod); len(m) > 1 {
		return m[1]
	}
	return pod
}
