package api

import (
	"sync"
	"time"
)

type loginLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	limit   int
	window  time.Duration
	history map[string][]time.Time
}

func newLoginLimiter(now func() time.Time, limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		now:     now,
		limit:   limit,
		window:  window,
		history: make(map[string][]time.Time),
	}
}

func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	return len(l.history[key]) < l.limit
}

func (l *loginLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	l.history[key] = append(l.history[key], l.now())
}

func (l *loginLimiter) prune(key string) {
	cutoff := l.now().Add(-l.window)
	entries := l.history[key]
	firstValid := 0
	for firstValid < len(entries) && !entries[firstValid].After(cutoff) {
		firstValid++
	}
	if firstValid > 0 {
		entries = entries[firstValid:]
	}
	if len(entries) == 0 {
		delete(l.history, key)
		return
	}
	l.history[key] = entries
}
