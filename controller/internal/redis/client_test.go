package redis

import (
	"context"
	"testing"
	"time"

	"github.com/sony/gobreaker"
	"github.com/staffops/staffops-anomaly-detection/internal/config"
)

// newUnreachableClient creates a Client pointing to a non-existent Redis.
// go-redis is lazy — NewClient succeeds; actual commands will fail with
// a connection error, but that's enough to exercise the code paths.
func newUnreachableClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(config.Redis{Addr: "127.0.0.1:1", DB: 0})
	if err != nil {
		t.Fatalf("NewClient should not fail (lazy connection): %v", err)
	}
	return c
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	return c
}

func TestNewClient_ReturnsClient(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	if c == nil {
		t.Fatal("NewClient should not return nil")
	}
}

func TestCircuitBreakerState_InitiallyClosed(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	if c.CircuitBreakerState() != gobreaker.StateClosed {
		t.Errorf("circuit breaker should start closed, got %v", c.CircuitBreakerState())
	}
}

func TestReadinessCheck_ReturnsFunction(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	check := c.ReadinessCheck()
	if check == nil {
		t.Error("ReadinessCheck should return a non-nil function")
	}
}

func TestClose_NoOp_NoConnected(t *testing.T) {
	c := newUnreachableClient(t)
	// Should not panic
	if err := c.Close(); err != nil {
		// go-redis Close() on unconnected client returns nil
		t.Logf("Close returned: %v (acceptable)", err)
	}
}

func TestPing_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	err := c.Ping(ctx(t))
	if err == nil {
		t.Error("Ping to unreachable server should return error")
	}
}

func TestHSet_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	err := c.HSet(ctx(t), "testkey", map[string]interface{}{"field": "value"})
	if err == nil {
		t.Error("HSet to unreachable server should return error")
	}
}

func TestHGetAll_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	_, err := c.HGetAll(ctx(t), "testkey")
	if err == nil {
		t.Error("HGetAll to unreachable server should return error")
	}
}

func TestSetWithTTL_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	err := c.SetWithTTL(ctx(t), "key", "value", time.Minute)
	if err == nil {
		t.Error("SetWithTTL to unreachable server should return error")
	}
}

func TestExists_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	_, err := c.Exists(ctx(t), "key")
	if err == nil {
		t.Error("Exists to unreachable server should return error")
	}
}

func TestGet_Unreachable_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	_, err := c.Get(ctx(t), "key")
	if err == nil {
		t.Error("Get to unreachable server should return error")
	}
}

func TestReadinessCheck_Invocation_ReturnsError(t *testing.T) {
	c := newUnreachableClient(t)
	defer c.Close()
	check := c.ReadinessCheck()
	// Invoking the readiness check on unreachable Redis should return error
	err := check(ctx(t))
	if err == nil {
		t.Error("readiness check against unreachable Redis should fail")
	}
}
