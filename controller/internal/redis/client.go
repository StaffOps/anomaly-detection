package redis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"

	"github.com/staffops/staffops-anomaly-detection/internal/config"
	"github.com/staffops/staffops-anomaly-detection/internal/metrics"
)

// Client wraps go-redis with a circuit breaker.
type Client struct {
	rdb *redis.Client
	cb  *gobreaker.CircuitBreaker
}

func NewClient(cfg config.Redis) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		DB:       cfg.DB,
		Password: cfg.Password,
	})

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "redis",
		MaxRequests: 3,
		Interval:    30 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			slog.Warn("redis circuit breaker state change", "from", from, "to", to)
		},
	})

	return &Client{rdb: rdb, cb: cb}, nil
}

// Ping checks Redis connectivity (used for readiness).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cb.Execute(func() (interface{}, error) {
		return nil, c.rdb.Ping(ctx).Err()
	})
	return err
}

// HSet sets fields in a hash.
func (c *Client) HSet(ctx context.Context, key string, values map[string]interface{}) error {
	start := time.Now()
	_, err := c.cb.Execute(func() (interface{}, error) {
		return nil, c.rdb.HSet(ctx, key, values).Err()
	})
	metrics.WorkerRedisDuration.Observe(time.Since(start).Seconds())
	metrics.WorkerRedisOps.WithLabelValues("hset").Inc()
	if err != nil {
		metrics.WorkerRedisErrors.Inc()
	}
	return err
}

// HGetAll returns all fields of a hash.
func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	start := time.Now()
	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.rdb.HGetAll(ctx, key).Result()
	})
	metrics.WorkerRedisDuration.Observe(time.Since(start).Seconds())
	metrics.WorkerRedisOps.WithLabelValues("hgetall").Inc()
	if err != nil {
		metrics.WorkerRedisErrors.Inc()
		return nil, err
	}
	return result.(map[string]string), nil
}

// SetWithTTL sets a key with expiration (used for dedup/cooldown).
func (c *Client) SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	start := time.Now()
	_, err := c.cb.Execute(func() (interface{}, error) {
		return nil, c.rdb.Set(ctx, key, value, ttl).Err()
	})
	metrics.WorkerRedisDuration.Observe(time.Since(start).Seconds())
	metrics.WorkerRedisOps.WithLabelValues("set").Inc()
	if err != nil {
		metrics.WorkerRedisErrors.Inc()
	}
	return err
}

// Exists checks if a key exists (used for dedup check).
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	start := time.Now()
	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.rdb.Exists(ctx, key).Result()
	})
	metrics.WorkerRedisDuration.Observe(time.Since(start).Seconds())
	metrics.WorkerRedisOps.WithLabelValues("exists").Inc()
	if err != nil {
		metrics.WorkerRedisErrors.Inc()
		return false, err
	}
	return result.(int64) > 0, nil
}

// Get returns a string value for a key. Returns empty string and nil if missing.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	start := time.Now()
	result, err := c.cb.Execute(func() (interface{}, error) {
		v, err := c.rdb.Get(ctx, key).Result()
		if err == redis.Nil {
			return "", nil
		}
		return v, err
	})
	metrics.WorkerRedisDuration.Observe(time.Since(start).Seconds())
	metrics.WorkerRedisOps.WithLabelValues("get").Inc()
	if err != nil {
		metrics.WorkerRedisErrors.Inc()
		return "", err
	}
	return result.(string), nil
}

// Close shuts down the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// CircuitBreakerState returns the current state for observability.
func (c *Client) CircuitBreakerState() gobreaker.State {
	return c.cb.State()
}

// ReadinessCheck returns a function compatible with metrics.ReadinessChecker.
func (c *Client) ReadinessCheck() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if c.cb.State() == gobreaker.StateOpen {
			return fmt.Errorf("redis circuit breaker open")
		}
		return c.Ping(ctx)
	}
}
