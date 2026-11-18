package crmapi

import (
	"sync"
	"time"
)

type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	counts map[string]*bucket
}

type bucket struct {
	start time.Time
	used  int
}

type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAfter time.Duration
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, now: time.Now, counts: map[string]*bucket{}}
}

func (l *Limiter) Allow(key string) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.counts[key]
	if !ok || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.counts[key] = b
	}

	reset := b.start.Add(l.window).Sub(now)
	if b.used >= l.limit {
		return Decision{Allowed: false, Limit: l.limit, Remaining: 0, ResetAfter: reset}
	}
	b.used++
	return Decision{Allowed: true, Limit: l.limit, Remaining: l.limit - b.used, ResetAfter: reset}
}
