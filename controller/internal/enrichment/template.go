package enrichment

import "strings"

// substitute replaces $variable placeholders in a query string using the identity.
// Unknown variables are replaced with empty strings, which is intentional —
// callers should validate the rendered query before executing.
//
// Supported placeholders:
//
//	$namespace, $pod, $container, $service_name, $workload, $node
func substitute(query string, id Identity) string {
	r := strings.NewReplacer(
		"$namespace", id.Namespace,
		"$pod", id.Pod,
		"$container", id.Container,
		"$service_name", id.ServiceName,
		"$workload", id.Workload,
		"$node", id.Node,
	)
	return r.Replace(query)
}
