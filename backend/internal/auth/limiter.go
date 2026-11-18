package auth

import (
	"sync"
	"time"
)

// attemptLimiter is a fixed-window counter for login attempts. It is per process, which is
// fine for a single API instance; a multi-instance deployment would move this to Postgres.
type attemptLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]window
	now    func() time.Time
}

type window struct {
	start time.Time
	count int
}

func newAttemptLimiter(max int, w time.Duration) *attemptLimiter {
	return &attemptLimiter{max: max, window: w, hits: map[string]window{}, now: time.Now}
}

func (l *attemptLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) > 10000 {
		for k, v := range l.hits {
			if now.Sub(v.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w := l.hits[key]
	if now.Sub(w.start) > l.window {
		w = window{start: now}
	}
	w.count++
	l.hits[key] = w
	return w.count <= l.max
}

func (l *attemptLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
