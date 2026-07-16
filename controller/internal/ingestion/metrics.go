package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Sample represents a single metric value with its label set.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Point is a single (timestamp, value) sample within a TimeSeries.
type Point struct {
	T time.Time
	V float64
}

// TimeSeries is a labeled sequence of points returned by range queries.
// Used by replay mode (range queries) but kept here next to Sample so all
// ingestion types stay together.
type TimeSeries struct {
	Labels map[string]string
	Points []Point
}

// MetricsPoller queries a Prometheus-compatible TSDB via PromQL.
type MetricsPoller struct {
	client  *http.Client
	baseURL string
}

func NewMetricsPoller(cfg config.DatasourceEndpoint) *MetricsPoller {
	return &MetricsPoller{
		client:  &http.Client{Timeout: cfg.Timeout},
		baseURL: cfg.URL,
	}
}

// Query executes a PromQL instant query and returns samples.
func (p *MetricsPoller) Query(ctx context.Context, query string) ([]Sample, error) {
	start := time.Now()
	defer func() {
		metrics.WorkerQueryDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("datasource", "prometheus")))
	}()

	u, err := url.Parse(p.baseURL + "/api/v1/query")
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	u.RawQuery = url.Values{"query": {query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		metrics.WorkerQueryErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("datasource", "prometheus")))
		return nil, fmt.Errorf("query vm: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		metrics.WorkerQueryErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("datasource", "prometheus")))
		return nil, fmt.Errorf("vm returned %d: %s", resp.StatusCode, body)
	}

	var result promResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("vm query failed: %s", result.Error)
	}

	return parseSamples(result.Data), nil
}

// QueryRange executes a PromQL range query and returns one TimeSeries per
// label set. Used by replay mode to load historical data in chunks.
//
// step controls the resolution; for typical replay use 30s (matching
// controller.JobInterval). Smaller steps return more points; larger steps
// risk skipping anomalies.
//
// Returns an error for non-2xx responses, malformed payloads, or empty
// status. Callers in replay mode are expected to skip the affected tick on
// error rather than abort the whole replay.
func (p *MetricsPoller) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]TimeSeries, error) {
	t0 := time.Now()
	defer func() {
		metrics.WorkerQueryDuration.Record(ctx, time.Since(t0).Seconds(),
			metric.WithAttributes(attribute.String("datasource", "prometheus_range")))
	}()

	u, err := url.Parse(p.baseURL + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64) + "s"},
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		metrics.WorkerQueryErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("datasource", "prometheus_range")))
		return nil, fmt.Errorf("query_range vm: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		metrics.WorkerQueryErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("datasource", "prometheus_range")))
		return nil, fmt.Errorf("vm returned %d: %s", resp.StatusCode, body)
	}

	var result promRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("vm query_range failed: %s", result.Error)
	}
	return parseRangeSeries(result.Data), nil
}

// promResponse is the Prometheus HTTP API response format.
type promResponse struct {
	Status string   `json:"status"`
	Error  string   `json:"error,omitempty"`
	Data   promData `json:"data"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promResult `json:"result"`
}

type promResult struct {
	Metric map[string]string `json:"metric"`
	Value  [2]interface{}    `json:"value"` // [timestamp, "value"]
}

func parseSamples(data promData) []Sample {
	samples := make([]Sample, 0, len(data.Result))
	for _, r := range data.Result {
		if len(r.Value) < 2 {
			continue
		}
		valStr, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue
		}
		samples = append(samples, Sample{
			Labels: r.Metric,
			Value:  val,
		})
	}
	return samples
}

// promRangeResponse is the Prometheus HTTP API response format for /query_range
// (`resultType: matrix`). Each result entry has a `values` array of
// [timestamp, "value"] pairs.
type promRangeResponse struct {
	Status string        `json:"status"`
	Error  string        `json:"error,omitempty"`
	Data   promRangeData `json:"data"`
}

type promRangeData struct {
	ResultType string            `json:"resultType"`
	Result     []promRangeResult `json:"result"`
}

type promRangeResult struct {
	Metric map[string]string `json:"metric"`
	Values [][2]interface{}  `json:"values"` // [[timestamp, "value"], ...]
}

func parseRangeSeries(data promRangeData) []TimeSeries {
	out := make([]TimeSeries, 0, len(data.Result))
	for _, r := range data.Result {
		points := make([]Point, 0, len(r.Values))
		for _, pair := range r.Values {
			tFloat, ok := pair[0].(float64)
			if !ok {
				continue
			}
			vStr, ok := pair[1].(string)
			if !ok {
				continue
			}
			v, err := strconv.ParseFloat(vStr, 64)
			if err != nil {
				continue
			}
			points = append(points, Point{
				T: time.Unix(int64(tFloat), 0).UTC(),
				V: v,
			})
		}
		out = append(out, TimeSeries{
			Labels: r.Metric,
			Points: points,
		})
	}
	return out
}
