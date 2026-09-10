package main

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics stores load-balancer-level counters and latency samples.
// All counter fields use atomics so goroutines can update them without locks.
type Metrics struct {
	Total         atomic.Uint64
	Success       atomic.Uint64
	Failed        atomic.Uint64
	BackendErrors atomic.Uint64

	mu        sync.Mutex
	latencies []time.Duration
}

// RecordLatency appends a successful request latency.
func (m *Metrics) RecordLatency(d time.Duration) {
	m.mu.Lock()
	m.latencies = append(m.latencies, d)
	m.mu.Unlock()
}

// Percentiles returns p50, p95 and p99 latencies in milliseconds.
func (m *Metrics) Percentiles() (p50, p95, p99 float64) {
	m.mu.Lock()
	lats := make([]time.Duration, len(m.latencies))
	copy(lats, m.latencies)
	m.mu.Unlock()

	n := len(lats)
	if n == 0 {
		return 0, 0, 0
	}

	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })

	idx := func(pct float64) time.Duration {
		i := int(float64(n)*pct/100) - 1
		if i < 0 {
			i = 0
		}
		if i >= n {
			i = n - 1
		}
		return lats[i]
	}

	toMs := func(d time.Duration) float64 {
		return float64(d.Microseconds()) / 1000.0
	}

	return toMs(idx(50)), toMs(idx(95)), toMs(idx(99))
}
