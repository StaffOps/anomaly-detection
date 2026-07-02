// Package readiness provides ReadinessChecker implementations for upstream
// dependencies (VictoriaMetrics, Loki, Alertmanager, ML service).
//
// All checkers follow metrics.ReadinessChecker (func(ctx) error) and are
// expected to return quickly (cap timeout to 3s) so /readyz stays responsive
// even when an upstream is degraded.
package readiness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

const readinessCap = 3 * time.Second

// VMChecker probes VictoriaMetrics by issuing a trivial PromQL query.
// Returns nil when status=success in the response; error otherwise.
func VMChecker(cfg config.DatasourceEndpoint) metrics.ReadinessChecker {
	client := &http.Client{Timeout: clamp(cfg.Timeout)}
	url := cfg.URL + "/api/v1/query?query=up"
	return func(ctx context.Context) error {
		err := probeJSON(ctx, client, url, func(body []byte) error {
			var r struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(body, &r); err != nil {
				return fmt.Errorf("decode: %w", err)
			}
			if r.Status != "success" {
				return fmt.Errorf("vm status=%q", r.Status)
			}
			return nil
		})
		recordResult("vm", err)
		return err
	}
}

func clamp(d time.Duration) time.Duration {
	if d == 0 || d > readinessCap {
		return readinessCap
	}
	return d
}

func recordResult(dep string, err error) {
	res := "ok"
	if err != nil {
		res = "error"
	}
	metrics.ReadinessChecksTotal.WithLabelValues(dep, res).Inc()
}
