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

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// LogsPoller queries Loki via LogQL.
type LogsPoller struct {
	client  *http.Client
	baseURL string
}

func NewLogsPoller(cfg config.DatasourceEndpoint) *LogsPoller {
	return &LogsPoller{
		client:  &http.Client{Timeout: cfg.Timeout},
		baseURL: cfg.URL,
	}
}

// QueryMetric executes a LogQL metric query (e.g. sum(rate(...))) and returns samples.
func (p *LogsPoller) QueryMetric(ctx context.Context, query string) ([]Sample, error) {
	start := time.Now()
	defer func() {
		metrics.WorkerQueryDuration.WithLabelValues("loki").Observe(time.Since(start).Seconds())
	}()

	u, err := url.Parse(p.baseURL + "/loki/api/v1/query")
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
		metrics.WorkerQueryErrors.WithLabelValues("loki").Inc()
		return nil, fmt.Errorf("query loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		metrics.WorkerQueryErrors.WithLabelValues("loki").Inc()
		return nil, fmt.Errorf("loki returned %d: %s", resp.StatusCode, body)
	}

	var result lokiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("loki query failed: %s", result.Error)
	}

	return parseLokiSamples(result.Data), nil
}

// QueryMetricRange executes a LogQL metric range query and returns one
// TimeSeries per label set. Used by replay mode for log-based detectors.
//
// Loki's range API returns `resultType: matrix` (same shape as PromQL) for
// metric queries. step is honored as a duration string.
func (p *LogsPoller) QueryMetricRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]TimeSeries, error) {
	t0 := time.Now()
	defer func() {
		metrics.WorkerQueryDuration.WithLabelValues("loki_range").Observe(time.Since(t0).Seconds())
	}()

	u, err := url.Parse(p.baseURL + "/loki/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(start.UnixNano(), 10)},
		"end":   {strconv.FormatInt(end.UnixNano(), 10)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64) + "s"},
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		metrics.WorkerQueryErrors.WithLabelValues("loki_range").Inc()
		return nil, fmt.Errorf("query_range loki: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		metrics.WorkerQueryErrors.WithLabelValues("loki_range").Inc()
		return nil, fmt.Errorf("loki returned %d: %s", resp.StatusCode, body)
	}

	var result lokiRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Status != "success" {
		return nil, fmt.Errorf("loki query_range failed: %s", result.Error)
	}
	return parseLokiRangeSeries(result.Data), nil
}

// lokiResponse matches Loki's query response for metric queries.
type lokiResponse struct {
	Status string   `json:"status"`
	Error  string   `json:"error,omitempty"`
	Data   lokiData `json:"data"`
}

type lokiData struct {
	ResultType string       `json:"resultType"`
	Result     []lokiResult `json:"result"`
}

type lokiResult struct {
	Metric map[string]string `json:"metric"`
	Value  [2]interface{}    `json:"value"` // [timestamp, "value"] for vector
}

func parseLokiSamples(data lokiData) []Sample {
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

// lokiRangeResponse matches Loki's range query response (`resultType: matrix`)
// for metric queries.
type lokiRangeResponse struct {
	Status string        `json:"status"`
	Error  string        `json:"error,omitempty"`
	Data   lokiRangeData `json:"data"`
}

type lokiRangeData struct {
	ResultType string            `json:"resultType"`
	Result     []lokiRangeResult `json:"result"`
}

type lokiRangeResult struct {
	Metric map[string]string `json:"metric"`
	// Loki returns values as [[ "<unix_nano_string>", "<value_string>" ], ...]
	// for matrix results, but JSON decodes them as [string, string] pairs.
	Values [][2]interface{} `json:"values"`
}

func parseLokiRangeSeries(data lokiRangeData) []TimeSeries {
	out := make([]TimeSeries, 0, len(data.Result))
	for _, r := range data.Result {
		points := make([]Point, 0, len(r.Values))
		for _, pair := range r.Values {
			// Loki may return either string or float for the timestamp
			// depending on version. Handle both defensively.
			var tNanos int64
			switch v := pair[0].(type) {
			case string:
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					continue
				}
				tNanos = n
			case float64:
				tNanos = int64(v)
			default:
				continue
			}
			vStr, ok := pair[1].(string)
			if !ok {
				continue
			}
			val, err := strconv.ParseFloat(vStr, 64)
			if err != nil {
				continue
			}
			points = append(points, Point{
				T: time.Unix(0, tNanos).UTC(),
				V: val,
			})
		}
		out = append(out, TimeSeries{
			Labels: r.Metric,
			Points: points,
		})
	}
	return out
}
