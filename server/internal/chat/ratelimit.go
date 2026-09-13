package chat

import (
	"sync"
	"time"
)

// Limiter is a simple per-key token bucket rate limiter.
type Limiter struct {
	mu           sync.Mutex
	buckets      map[string]*bucket
	capacity     float64
	refillPerSec float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter creates a limiter allowing burstN events, refilling to burstN
// over refillPeriod.
func NewLimiter(burstN int, refillPeriod time.Duration) *Limiter {
	return &Limiter{
		buckets:      make(map[string]*bucket),
		capacity:     float64(burstN),
		refillPerSec: float64(burstN) / refillPeriod.Seconds(),
	}
}

// Allow reports whether an event for key is allowed right now, consuming a
// token if so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * l.refillPerSec
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
