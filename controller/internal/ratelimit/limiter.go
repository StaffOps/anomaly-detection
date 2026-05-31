package ratelimit

import (
	"context"
	"time"
)

// Limiter is a simple token bucket rate limiter.
type Limiter struct {
	tokens chan struct{}
	rate   time.Duration
}

// New creates a limiter that allows maxPerSecond operations per second.
func New(maxPerSecond int) *Limiter {
	l := &Limiter{
		tokens: make(chan struct{}, maxPerSecond),
		rate:   time.Second / time.Duration(maxPerSecond),
	}
	// Fill initial tokens
	for i := 0; i < maxPerSecond; i++ {
		l.tokens <- struct{}{}
	}
	// Refill goroutine
	go func() {
		ticker := time.NewTicker(l.rate)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case l.tokens <- struct{}{}:
			default: // bucket full
			}
		}
	}()
	return l
}

// Wait blocks until a token is available or ctx is cancelled.
func (l *Limiter) Wait(ctx context.Context) error {
	select {
	case <-l.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
