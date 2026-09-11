package main

import (
	"math"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"
)

// BackendHealth is the parsed /health JSON from Python backends
type BackendHealth struct {
	CPUPercent  float64 `json:"cpu_percent"`
	LoadAvg1m   float64 `json:"load_avg_1m"`
	WSConns     int     `json:"active_ws_connections"`
	ValkeyRTTms float64 `json:"valkey_rtt_ms"`
	LagMs       float64 `json:"lag_ms"`
}

// Backend represents a single upstream server in the pool.
type Backend struct {
	URL               *url.URL
	alive             atomic.Bool
	loadScore         atomic.Value // stores float64 — for monitoring only
	inFlight          atomic.Int64
	latencyEWMAMicros atomic.Int64
	reportedLagMicros atomic.Int64
	lastGoodNanos     atomic.Int64
	failStreak        atomic.Int32
	okStreak          atomic.Int32
	proxy             *httputil.ReverseProxy
}

// IsAlive returns the current health status.
func (b *Backend) IsAlive() bool {
	return b.alive.Load()
}

// SetAlive updates the health status.
func (b *Backend) SetAlive(alive bool) {
	b.alive.Store(alive)
}

// IncrementInFlight marks one more request in progress.
func (b *Backend) IncrementInFlight() {
	b.inFlight.Add(1)
}

// DecrementInFlight marks one request as completed.
func (b *Backend) DecrementInFlight() {
	b.inFlight.Add(-1)
}

// InFlightCount returns the number of active requests.
func (b *Backend) InFlightCount() int64 {
	return b.inFlight.Load()
}

func (b *Backend) ObserveLatency(d time.Duration) {
	b.lastGoodNanos.Store(time.Now().UnixNano())
	micros := d.Microseconds()
	const alphaNum, alphaDen = 1, 5
	for {
		old := b.latencyEWMAMicros.Load()
		if old == 0 {
			if b.latencyEWMAMicros.CompareAndSwap(0, micros) {
				return
			}
			continue
		}
		newVal := old + (micros-old)*alphaNum/alphaDen
		if b.latencyEWMAMicros.CompareAndSwap(old, newVal) {
			return
		}
	}
}

func (b *Backend) UpdateHealth(h BackendHealth) {
	b.reportedLagMicros.Store(int64(h.LagMs * 1000.0))
}

func (b *Backend) Score() float64 {
	svcMs := float64(b.latencyEWMAMicros.Load()) / 1000.0
	const scoreHalfLife = 5 * time.Second
	if last := b.lastGoodNanos.Load(); last > 0 {
		if idle := time.Since(time.Unix(0, last)); idle > 0 {
			svcMs /= math.Exp2(float64(idle) / float64(scoreHalfLife))
		}
	}
	queue := float64(b.InFlightCount() + 1)
	lagMs := float64(b.reportedLagMicros.Load()) / 1000.0
	
	score := queue*svcMs + lagMs
	if score == 0 {
		score = 1.0 // baseline
	}
	return score
}

// Used for monitoring endpoint compatibility
func (b *Backend) LoadScore() float64 {
	return b.Score()
}

func (b *Backend) IsOverloaded() bool {
	// A backend is overloaded if its estimated response time is > 100ms
	return b.Score() > 100.0
}
