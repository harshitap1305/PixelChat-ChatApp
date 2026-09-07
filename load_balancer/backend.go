package main

import (
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

// BackendHealth is the parsed /health JSON from Python backends
type BackendHealth struct {
	CPUPercent  float64 `json:"cpu_percent"`
	LoadAvg1m   float64 `json:"load_avg_1m"`
	WSConns     int     `json:"active_ws_connections"`
	ValkeyRTTms float64 `json:"valkey_rtt_ms"`
}

// Backend represents a single upstream server in the pool.
type Backend struct {
	URL       *url.URL
	alive     atomic.Bool
	loadScore atomic.Value // stores float64 — for monitoring only
	inFlight  atomic.Int64
	proxy     *httputil.ReverseProxy
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

func (b *Backend) computeLoadScore(h BackendHealth) float64 {
	return float64(b.InFlightCount())*2.0 +
		h.CPUPercent +
		float64(h.WSConns)*0.5 +
		h.ValkeyRTTms*3.0
}

func (b *Backend) SetLoadScore(s float64) { b.loadScore.Store(s) }
func (b *Backend) LoadScore() float64 {
	if v := b.loadScore.Load(); v != nil {
		return v.(float64)
	}
	return 0
}

// IsOverloaded returns true if the backend has too many live in-flight requests.
// Uses real-time atomic in_flight, NOT stale health-check score.
func (b *Backend) IsOverloaded() bool {
	return b.inFlight.Load() > overloadThreshold
}
