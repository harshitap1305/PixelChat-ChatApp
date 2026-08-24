package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Backend ──────────────────────────────────────────────────────────────────

type Backend struct {
	URL      *url.URL
	Alive    atomic.Bool
	InFlight atomic.Int64
}

// ─── Metrics ──────────────────────────────────────────────────────────────────

type Metrics struct {
	Total         atomic.Uint64
	Success       atomic.Uint64
	Failed        atomic.Uint64
	BackendErrors atomic.Uint64

	LatencyMu sync.Mutex
	Latencies []time.Duration
}

// ─── LoadBalancer ─────────────────────────────────────────────────────────────

type LoadBalancer struct {
	backends []*Backend
	next     atomic.Uint64
	metrics  Metrics
	timeout  time.Duration
}

func (lb *LoadBalancer) nextBackend() *Backend {
	n := len(lb.backends)
	for i := 0; i < n; i++ {
		index := lb.next.Add(1) % uint64(n)
		b := lb.backends[index]
		if b.Alive.Load() {
			return b
		}
	}
	return nil
}

func (lb *LoadBalancer) healthLoop(interval time.Duration) {
	// InsecureSkipVerify allows the health checker to reach backends
	// using self-signed TLS certificates (the Python messaging backend).
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for {
		for _, b := range lb.backends {
			resp, err := client.Get(b.URL.String() + "/health")
			if err != nil || resp.StatusCode >= 500 {
				if b.Alive.Load() {
					log.Printf("[health] backend %s -> UNHEALTHY", b.URL)
				}
				b.Alive.Store(false)
			} else {
				if !b.Alive.Load() {
					log.Printf("[health] backend %s -> HEALTHY", b.URL)
				}
				b.Alive.Store(true)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		time.Sleep(interval)
	}
}

// ─── Monitoring Handlers ──────────────────────────────

// healthHandler: GET /lb/health
func (lb *LoadBalancer) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// statusHandler: GET /lb/status
func (lb *LoadBalancer) statusHandler(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		URL      string `json:"url"`
		Alive    bool   `json:"alive"`
		InFlight int64  `json:"in_flight"`
	}
	var list []entry
	for _, b := range lb.backends {
		list = append(list, entry{
			URL:      b.URL.String(),
			Alive:    b.Alive.Load(),
			InFlight: b.InFlight.Load(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"backends": list})
}

// metricsHandler: GET /lb/metrics
func (lb *LoadBalancer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	lb.metrics.LatencyMu.Lock()
	cp := make([]time.Duration, len(lb.metrics.Latencies))
	copy(cp, lb.metrics.Latencies)
	lb.metrics.LatencyMu.Unlock()

	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":          lb.metrics.Total.Load(),
		"success":        lb.metrics.Success.Load(),
		"failed":         lb.metrics.Failed.Load(),
		"backend_errors": lb.metrics.BackendErrors.Load(),
		"p50_ms":         percentile(cp, 50).Milliseconds(),
		"p95_ms":         percentile(cp, 95).Milliseconds(),
		"p99_ms":         percentile(cp, 99).Milliseconds(),
	})
}

// ─── Forward Handler ──────────────────────────

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	lb.metrics.Total.Add(1)
	start := time.Now()

	b := lb.nextBackend()
	if b == nil {
		lb.metrics.Failed.Add(1)
		http.Error(w, "no healthy backend available", http.StatusServiceUnavailable)
		return
	}

	b.InFlight.Add(1)
	defer b.InFlight.Add(-1)

	target := b.URL
	proxy := httputil.NewSingleHostReverseProxy(target)

	dialer := &net.Dialer{
		Timeout: lb.timeout, 
	}
	proxy.Transport = &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: lb.timeout, 
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}

	ctx, cancel := context.WithTimeout(r.Context(), lb.timeout)
	defer cancel()
	r = r.WithContext(ctx)

	errored := false
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		errored = true
		b.Alive.Store(false)
		lb.metrics.BackendErrors.Add(1)
		lb.metrics.Failed.Add(1)
		http.Error(rw, "backend unavailable", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)

	if !errored {
		lb.metrics.Success.Add(1)
		elapsed := time.Since(start)
		lb.metrics.LatencyMu.Lock()
		lb.metrics.Latencies = append(lb.metrics.Latencies, elapsed)
		lb.metrics.LatencyMu.Unlock()
	}
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	return sorted[idx]
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	addr := flag.String("addr", ":8080",
		"address the load balancer listens on")

	backendsFlag := flag.String("backends", "http://localhost:8081",
		"comma-separated list of backend URLs")

	healthInterval := flag.Duration("health-interval", 1*time.Second,
		"how often to probe backend /health")

	backendTimeout := flag.Duration("backend-timeout", 800*time.Millisecond,
		"backend request timeout")

	flag.Parse()

	// Build backend list
	lb := &LoadBalancer{timeout: *backendTimeout}
	for _, raw := range strings.Split(*backendsFlag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			log.Fatalf("invalid backend URL %q: %v", raw, err)
		}
		b := &Backend{URL: u}
		b.Alive.Store(true)
		lb.backends = append(lb.backends, b)
		log.Printf("[LB] registered backend: %s", u)
	}

	if len(lb.backends) == 0 {
		log.Fatal("no backends configured")
	}

	// Start health check goroutine
	go lb.healthLoop(*healthInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/lb/health",  lb.healthHandler)
	mux.HandleFunc("/lb/status",  lb.statusHandler)
	mux.HandleFunc("/lb/metrics", lb.metricsHandler)
	mux.Handle("/", lb)

	fmt.Printf("\nLoad Balancer listening on %s\n", *addr)
	fmt.Printf("Backends : %d\n", len(lb.backends))
	fmt.Printf("Health   : every %s\n", *healthInterval)
	fmt.Printf("Timeout  : %s\n\n", *backendTimeout)
	fmt.Printf("  /lb/health   — LB liveness\n")
	fmt.Printf("  /lb/status   — backend states\n")
	fmt.Printf("  /lb/metrics  — counters + latency\n")
	fmt.Printf("  /            — forward to backends\n\n")

	log.Fatal(http.ListenAndServe(*addr, mux))
}
