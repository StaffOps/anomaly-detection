package readiness

import (
	"context"

	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	"github.com/staffops/staffops-anomaly-detection/internal/ml"
)

// MLChecker probes the ML service via its Health RPC. When the client is
// disabled (cfg.ML.Enabled = false), the checker returns nil immediately.
func MLChecker(client *ml.Client) metrics.ReadinessChecker {
	return func(ctx context.Context) error {
		if client == nil || !client.Enabled() {
			return nil
		}
		err := client.Health(ctx)
		recordResult("ml", err)
		return err
	}
}
