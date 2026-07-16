package readiness

import (
	"context"
	"net/http"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// LokiChecker probes Loki by listing labels (a cheap, always-available endpoint).
func LokiChecker(cfg config.DatasourceEndpoint) metrics.ReadinessChecker {
	client := &http.Client{Timeout: clamp(cfg.Timeout)}
	url := cfg.URL + "/loki/api/v1/labels"
	return func(ctx context.Context) error {
		err := probeHTTP(ctx, client, url)
		recordResult(ctx, "loki", err)
		return err
	}
}

// AlertmanagerChecker probes Alertmanager via the v2 status endpoint.
func AlertmanagerChecker(cfg config.DatasourceEndpoint) metrics.ReadinessChecker {
	client := &http.Client{Timeout: clamp(cfg.Timeout)}
	url := cfg.URL + "/api/v2/status"
	return func(ctx context.Context) error {
		err := probeHTTP(ctx, client, url)
		recordResult(ctx, "alertmanager", err)
		return err
	}
}
