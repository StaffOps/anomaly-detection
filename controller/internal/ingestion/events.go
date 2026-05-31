package ingestion

import (
	"context"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// EventAnomaly represents a detected anomalous K8s event.
type EventAnomaly struct {
	Reason    string
	Namespace string
	Pod       string
	Message   string
	Count     int32
	Timestamp time.Time
}

// EventWatcher watches K8s events for anomalous patterns.
type EventWatcher struct {
	client   kubernetes.Interface
	patterns map[string]bool
}

func NewEventWatcher(client kubernetes.Interface, patterns []string) *EventWatcher {
	m := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		m[p] = true
	}
	return &EventWatcher{client: client, patterns: m}
}

// Watch starts watching events and sends anomalies to the channel.
// Blocks until ctx is cancelled.
func (w *EventWatcher) Watch(ctx context.Context, anomalies chan<- EventAnomaly) error {
	watcher, err := w.client.CoreV1().Events("").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	slog.Info("event watcher started", "patterns", len(w.patterns))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-watcher.ResultChan():
			if !ok {
				return nil
			}
			if evt.Type != watch.Added && evt.Type != watch.Modified {
				continue
			}
			event, ok := evt.Object.(*corev1.Event)
			if !ok {
				continue
			}
			if !w.patterns[event.Reason] {
				continue
			}

			metrics.WorkerEventsReceived.WithLabelValues(event.Reason).Inc()

			anomalies <- EventAnomaly{
				Reason:    event.Reason,
				Namespace: event.InvolvedObject.Namespace,
				Pod:       event.InvolvedObject.Name,
				Message:   event.Message,
				Count:     event.Count,
				Timestamp: event.LastTimestamp.Time,
			}
		}
	}
}
