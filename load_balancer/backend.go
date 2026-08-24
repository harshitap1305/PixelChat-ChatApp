package main

import (
	"net/url"
	"sync/atomic"
)

// Backend represents a single upstream server in the pool.
type Backend struct {
	URL      *url.URL
	alive    atomic.Bool
	inFlight atomic.Int64
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
