package enrichment

import (
	"fmt"
	"sort"
	"strings"
)

// Identity represents the labels available for an anomaly that can be used
// to fan out enrichment queries. Empty strings mean the label was absent.
type Identity struct {
	Namespace   string
	Pod         string
	Container   string
	ServiceName string
	Workload    string
	Node        string
}

// Kind reports which enrichment bundle should run for this identity.
type Kind int

const (
	KindUnknown Kind = iota
	KindPod
	KindService
)

// Kind picks the most specific identity available.
// Pod is preferred over Service (more specific blast radius).
func (i Identity) Kind() Kind {
	if i.Pod != "" && i.Namespace != "" {
		return KindPod
	}
	if i.ServiceName != "" {
		return KindService
	}
	return KindUnknown
}

// IdentityFromLabels extracts well-known identity fields from anomaly labels.
// Returns the empty Identity if no usable label was found.
func IdentityFromLabels(labels map[string]string) Identity {
	return Identity{
		Namespace:   firstNonEmpty(labels, "namespace", "k8s_namespace_name", "k8s.namespace.name"),
		Pod:         firstNonEmpty(labels, "pod", "k8s_pod_name", "k8s.pod.name"),
		Container:   firstNonEmpty(labels, "container", "container_name", "k8s.container.name"),
		ServiceName: firstNonEmpty(labels, "service_name", "service.name"),
		Workload:    firstNonEmpty(labels, "workload", "deployment", "k8s_deployment_name"),
		Node:        firstNonEmpty(labels, "node", "k8s_node_name", "k8s.node.name"),
	}
}

// CacheKey returns a stable key for caching enrichment results for this identity.
func (i Identity) CacheKey() string {
	parts := []string{}
	if i.Namespace != "" {
		parts = append(parts, "ns="+i.Namespace)
	}
	if i.Pod != "" {
		parts = append(parts, "pod="+i.Pod)
	}
	if i.ServiceName != "" {
		parts = append(parts, "svc="+i.ServiceName)
	}
	sort.Strings(parts)
	return "enrichment:" + strings.Join(parts, ",")
}

// String returns a human-readable summary of the identity (used in logs).
func (i Identity) String() string {
	switch i.Kind() {
	case KindPod:
		return fmt.Sprintf("pod=%s/%s", i.Namespace, i.Pod)
	case KindService:
		return fmt.Sprintf("service=%s", i.ServiceName)
	}
	return "unknown"
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}
