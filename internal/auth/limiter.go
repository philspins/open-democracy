package auth

import (
	"sync"
	"time"
)

type rateRecord struct {
	windowStart time.Time
	count       int
}

type simpleRateLimiter struct {
	mu      sync.Mutex
	records map[string]rateRecord
}

func newSimpleRateLimiter() *simpleRateLimiter {
	return &simpleRateLimiter{records: map[string]rateRecord{}}
}

func (r *simpleRateLimiter) allow(key string, limit int, window time.Duration, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[key]
	if !ok || now.Sub(rec.windowStart) >= window {
		r.records[key] = rateRecord{windowStart: now, count: 1}
		return true
	}
	if rec.count >= limit {
		return false
	}
	rec.count++
	r.records[key] = rec
	return true
}
