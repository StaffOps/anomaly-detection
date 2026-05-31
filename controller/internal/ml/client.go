package ml

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/detection"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
	pb "github.com/staffops/staffops-anomaly-detection/proto"
)

// Client wraps the ML gRPC service.
type Client struct {
	client  pb.MLDetectorClient
	conn    *grpc.ClientConn
	timeout time.Duration
	enabled bool
}

// New creates a new ML client. If disabled, all methods are no-ops.
func New(cfg config.ML) (*Client, error) {
	if !cfg.Enabled {
		slog.Info("ml client disabled")
		return &Client{enabled: false}, nil
	}

	conn, err := grpc.Dial(
		cfg.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	slog.Info("ml client connected", "endpoint", cfg.Endpoint)
	return &Client{
		client:  pb.NewMLDetectorClient(conn),
		conn:    conn,
		timeout: cfg.Timeout,
		enabled: true,
	}, nil
}

// Close shuts down the gRPC connection.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Health calls the ML service Health RPC. Returns nil if the service reports
// ready=true, error otherwise. No-op (returns nil) when the client is disabled.
func (c *Client) Health(ctx context.Context) error {
	if !c.enabled {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resp, err := c.client.Health(callCtx, &pb.Empty{})
	if err != nil {
		return err
	}
	if !resp.Ready {
		return fmt.Errorf("ml not ready")
	}
	return nil
}

// Enabled reports whether ML calls will be sent.
func (c *Client) Enabled() bool { return c.enabled }

// Forecast calls Prophet to predict if a metric will breach a threshold.
// Returns an anomaly if breach is predicted within the horizon.
func (c *Client) Forecast(ctx context.Context, metricName string, values []float64, timestamps []int64, threshold float64) (*detection.Anomaly, error) {
	if !c.enabled || len(values) < 10 {
		return nil, nil
	}

	start := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Forecast(callCtx, &pb.ForecastRequest{
		MetricName:      metricName,
		Values:          values,
		Timestamps:      timestamps,
		HorizonMinutes:  30,
		BreachThreshold: threshold,
	})
	metrics.MLCallDuration.WithLabelValues("forecast").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.MLCalls.WithLabelValues("forecast", "error").Inc()
		return nil, err
	}
	metrics.MLCalls.WithLabelValues("forecast", "ok").Inc()

	if !resp.WillBreachThreshold {
		return nil, nil
	}

	return &detection.Anomaly{
		MetricName: metricName,
		Labels:     map[string]string{"forecast": "true"},
		Value:      resp.Predicted[len(resp.Predicted)-1],
		Score:      resp.Confidence,
		Severity:   "warning",
		Signal:     "metrics",
		Detector:   "ml_forecast",
		Timestamp:  time.Now(),
	}, nil
}

// DetectMultivariate calls Isolation Forest with current metric values.
// Returns an anomaly if the multivariate combination is anomalous.
func (c *Client) DetectMultivariate(ctx context.Context, samples map[string]float64) (*detection.Anomaly, error) {
	if !c.enabled || len(samples) < 2 {
		return nil, nil
	}

	start := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	pbSamples := make([]*pb.MetricSample, 0, len(samples))
	for name, val := range samples {
		pbSamples = append(pbSamples, &pb.MetricSample{Name: name, Value: val})
	}

	resp, err := c.client.DetectMultivariate(callCtx, &pb.MultivariateRequest{Samples: pbSamples})
	metrics.MLCallDuration.WithLabelValues("multivariate").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.MLCalls.WithLabelValues("multivariate", "error").Inc()
		return nil, err
	}
	metrics.MLCalls.WithLabelValues("multivariate", "ok").Inc()

	if !resp.IsAnomaly {
		return nil, nil
	}

	return &detection.Anomaly{
		MetricName: "multivariate_anomaly",
		Labels:     map[string]string{"contributing": joinMetrics(resp.ContributingMetrics)},
		Value:      resp.AnomalyScore,
		Score:      resp.AnomalyScore,
		Severity:   severityFromScore(resp.AnomalyScore),
		Signal:     "metrics",
		Detector:   "ml_isolation_forest",
		Timestamp:  time.Now(),
	}, nil
}

func severityFromScore(score float64) string {
	if score > 0.8 {
		return "critical"
	}
	return "warning"
}

func joinMetrics(metrics []string) string {
	if len(metrics) == 0 {
		return ""
	}
	result := metrics[0]
	for _, m := range metrics[1:] {
		result += "," + m
	}
	return result
}
