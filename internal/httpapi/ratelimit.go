package httpapi

import (
	"sync"
	"time"
)

type ipLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newIPLimiter() *ipLimiter {
	return &ipLimiter{hits: map[string][]time.Time{}}
}

func (l *ipLimiter) allow(ip string, n int, window time.Duration) bool {
	if l == nil {
		return true
	}
	now := time.Now()
	cut := now.Add(-window)
	l.mu.Lock()
	defer l.mu.Unlock()
	q := l.hits[ip]
	k := 0
	for _, t := range q {
		if t.After(cut) {
			q[k] = t
			k++
		}
	}
	q = q[:k]
	if len(q) >= n {
		l.hits[ip] = q
		return false
	}
	l.hits[ip] = append(q, now)
	return true
}
