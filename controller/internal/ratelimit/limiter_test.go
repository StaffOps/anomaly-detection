package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestNew_ConsumesTokens(t *testing.T) {
	l := New(5)
	ctx := context.Background()

	// Should be able to consume up to maxPerSecond tokens immediately
	for i := 0; i < 5; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("token %d: unexpected error: %v", i, err)
		}
	}
}

func TestWait_CancelledContext(t *testing.T) {
	l := New(1)
	ctx := context.Background()

	// Drain the one token
	if err := l.Wait(ctx); err != nil {
		t.Fatalf("unexpected error draining token: %v", err)
	}

	// Now bucket is empty — cancel context should unblock Wait
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := l.Wait(cancelled)
	if err == nil {
		t.Error("expected context cancellation error, got nil")
	}
}

func TestWait_RefillsOverTime(t *testing.T) {
	l := New(10)
	ctx := context.Background()

	// Drain all initial tokens
	for i := 0; i < 10; i++ {
		_ = l.Wait(ctx)
	}

	// Wait for at least one refill tick
	time.Sleep(150 * time.Millisecond)

	// Should have at least 1 token now (10 tokens/s = 1 token per 100ms)
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctxTimeout); err != nil {
		t.Error("expected token to be available after refill, got timeout")
	}
}

func TestWait_TimeoutWithEmptyBucket(t *testing.T) {
	l := New(1)
	ctx := context.Background()

	// Drain the token
	_ = l.Wait(ctx)

	// With a very short timeout, should not get a token
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := l.Wait(ctxTimeout)
	if err == nil {
		t.Error("expected timeout error with empty bucket, got nil")
	}
}

func TestNew_HighRateAllowsConcurrentConsumption(t *testing.T) {
	l := New(100)
	ctx := context.Background()
	errors := 0
	for i := 0; i < 100; i++ {
		if err := l.Wait(ctx); err != nil {
			errors++
		}
	}
	if errors > 0 {
		t.Errorf("expected 0 errors consuming 100 tokens from rate-100 limiter, got %d", errors)
	}
}
