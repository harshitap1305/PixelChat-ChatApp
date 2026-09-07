/*
Load Balancer — main.go
========================
A reverse-proxy load balancer written in Go.

Features:
  - Round-robin scheduling (skips unhealthy backends)
  - Periodic background health checks
  - Per-request in-flight tracking (atomic)
  - Aggregated metrics (total / success / failed / p50 / p95 / p99)
  - HTTPS support (uses same cert/key as the Python backends)
  - Monitoring endpoints: /lb/health  /lb/status  /lb/metrics

Usage:
  go run . \
    -port 5000 \
    -backends https://10.1.75.51:5270,https://10.1.75.51:5271,https://10.1.75.51:5272 \
    -cert ../cert.pem \
    -key  ../key.pem

Monitoring (from any machine):
  curl -k https://10.1.75.51:5269/lb/health
  curl -k https://10.1.75.51:5269/lb/status
  curl -k https://10.1.75.51:5269/lb/metrics
*/

package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// ── LoadBalancer ──────────────────────────────────────────────────────────────

// LoadBalancer holds the backend pool, a round-robin counter and metrics.
type LoadBalancer struct {
	backends []*Backend
	next     atomic.Uint64
	metrics  Metrics
}

// overloadThreshold is the max in-flight requests per backend before it is
// considered overloaded and deprioritised by P2C. Using real-time in_flight
// instead of stale health-check scores prevents the Thundering Herd problem.
const overloadThreshold int64 = 200

// nextBackend returns the next alive backend using Power of Two Choices (P2C)
func (lb *LoadBalancer) nextBackend() *Backend {
	n := len(lb.backends)
	if n == 0 {
		return nil
	}
	if n == 1 {
		b := lb.backends[0]
		if b.IsAlive() && !b.IsOverloaded() {
			return b
		}
		if b.IsAlive() { // fallback if all overloaded
			return b
		}
		return nil
	}

	// Pick two distinct random indices
	i := rand.Intn(n)
	j := rand.Intn(n - 1)
	if j >= i {
		j++
	}

	b1 := lb.backends[i]
	b2 := lb.backends[j]

	var candidate *Backend
	if b1.IsAlive() && !b1.IsOverloaded() && b2.IsAlive() && !b2.IsOverloaded() {
		if b1.InFlightCount() <= b2.InFlightCount() {
			candidate = b1
		} else {
			candidate = b2
		}
	} else if b1.IsAlive() && !b1.IsOverloaded() {
		candidate = b1
	} else if b2.IsAlive() && !b2.IsOverloaded() {
		candidate = b2
	}

	if candidate != nil {
		return candidate
	}

	// Fallback 1: Try round-robin skipping overloaded
	for k := 0; k < n; k++ {
		idx := lb.next.Add(1) % uint64(n)
		b := lb.backends[idx]
		if b.IsAlive() && !b.IsOverloaded() {
			return b
		}
	}

	// Fallback 2: Any alive backend
	for k := 0; k < n; k++ {
		idx := lb.next.Add(1) % uint64(n)
		b := lb.backends[idx]
		if b.IsAlive() {
			return b
		}
	}

	return nil
}

// healthLoop runs forever, checking /health on every backend every interval.
func (lb *LoadBalancer) healthLoop(interval time.Duration) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
	}

	for {
		for _, b := range lb.backends {
			healthURL := b.URL.String() + "/health"
			resp, err := client.Get(healthURL)

			wasAlive := b.IsAlive()
			if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
				b.SetAlive(false)
				b.SetLoadScore(9999.0) // Penalize dead backend
				if wasAlive {
					log.Printf("[HEALTH] ⬇  Backend DOWN: %s", b.URL)
				}
			} else {
				var h BackendHealth
				if err := json.NewDecoder(resp.Body).Decode(&h); err == nil {
					score := b.computeLoadScore(h)
					b.SetLoadScore(score)
					// NOTE: overloaded flag is now computed from live in_flight in IsOverloaded().
					// We keep the score for monitoring/status endpoints only.
				}
				resp.Body.Close()

				b.SetAlive(true)
				if !wasAlive {
					log.Printf("[HEALTH] ⬆  Backend UP:   %s", b.URL)
				}
			}
		}
		time.Sleep(interval)
	}
}

// ── Request Handler ───────────────────────────────────────────────────────────

// makeTransport creates an HTTP transport that skips TLS verification.
// This is needed because the Python backends use self-signed certificates.
var globalTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // #nosec G402
	ResponseHeaderTimeout: 10 * time.Second,
	MaxIdleConns:          500,
	MaxIdleConnsPerHost:   500,
	IdleConnTimeout:       4 * time.Second, // Must be lower than Uvicorn's 5s timeout!
}

// serveRequest is the main HTTP handler — picks a backend and proxies the request.
func (lb *LoadBalancer) serveRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lb.metrics.Total.Add(1)

	b := lb.nextBackend()
	if b == nil {
		lb.metrics.Failed.Add(1)
		http.Error(w, `{"error":"no healthy backends available"}`, http.StatusServiceUnavailable)
		log.Printf("[LB] No healthy backends for %s %s", r.Method, r.URL.Path)
		return
	}

	b.IncrementInFlight()
	defer b.DecrementInFlight()

	b.proxy.ServeHTTP(w, r)

	if r.Header.Get("X-Proxy-Failed") != "true" {
		elapsed := time.Since(start)
		lb.metrics.Success.Add(1)
		lb.metrics.RecordLatency(elapsed)
	}
}

// ── Monitoring Endpoints ──────────────────────────────────────────────────────

func (lb *LoadBalancer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (lb *LoadBalancer) handleStatus(w http.ResponseWriter, r *http.Request) {
	type backendStatus struct {
		URL        string  `json:"url"`
		Alive      bool    `json:"alive"`
		InFlight   int64   `json:"in_flight"`
		LoadScore  float64 `json:"load_score"`
		Overloaded bool    `json:"overloaded"`
	}
	statuses := make([]backendStatus, 0, len(lb.backends))
	for _, b := range lb.backends {
		statuses = append(statuses, backendStatus{
			URL:        b.URL.String(),
			Alive:      b.IsAlive(),
			InFlight:   b.InFlightCount(),
			LoadScore:  b.LoadScore(),
			Overloaded: b.IsOverloaded(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"backends": statuses,
	})
}

func (lb *LoadBalancer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	total := lb.metrics.Total.Load()
	success := lb.metrics.Success.Load()
	failed := lb.metrics.Failed.Load()
	beErrors := lb.metrics.BackendErrors.Load()

	var dropoutPct float64
	if total > 0 {
		dropoutPct = float64(failed) / float64(total) * 100.0
	}

	p50, p95, p99 := lb.metrics.Percentiles()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":          total,
		"success":        success,
		"failed":         failed,
		"backend_errors": beErrors,
		"dropout_pct":    fmt.Sprintf("%.2f%%", dropoutPct),
		"p50_ms":         p50,
		"p95_ms":         p95,
		"p99_ms":         p99,
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// ── CLI flags ──────────────────────────────────────────────────────────
	port := flag.Int("port", 5000,
		"Port the load balancer listens on (internal).")

	backendsFlag := flag.String("backends",
		"https://10.1.75.51:5270,https://10.1.75.51:5271,https://10.1.75.51:5272",
		"Comma-separated list of backend HTTPS URLs.")

	healthInterval := flag.Duration("health-interval", 2*time.Second,
		"How often to health-check each backend.")

	certFile := flag.String("cert", "../cert.pem",
		"Path to TLS certificate (PEM). Pass empty string to run plain HTTP.")

	keyFile := flag.String("key", "../key.pem",
		"Path to TLS private key (PEM).")

	flag.Parse()

	// ── Parse backends ─────────────────────────────────────────────────────
	var backends []*Backend
	for _, raw := range strings.Split(*backendsFlag, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			log.Fatalf("[INIT] Invalid backend URL %q: %v", raw, err)
		}
		b := &Backend{URL: u}
		b.SetAlive(true) // assume alive until first health check
		backends = append(backends, b)
	}
	if len(backends) == 0 {
		log.Fatal("[INIT] No backends configured. Use -backends flag.")
	}

	lb := &LoadBalancer{backends: backends}

	for _, b := range lb.backends {
		p := httputil.NewSingleHostReverseProxy(b.URL)
		p.Transport = globalTransport
		bBackend := b
		p.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			req.Header.Set("X-Proxy-Failed", "true")
			log.Printf("[ERROR] Backend %s error: %v", bBackend.URL, err)
			lb.metrics.BackendErrors.Add(1)
			lb.metrics.Failed.Add(1)
			http.Error(rw, `{"error":"backend unavailable"}`, http.StatusBadGateway)
		}
		b.proxy = p
	}

	// ── Start health checker ───────────────────────────────────────────────
	go lb.healthLoop(*healthInterval)

	// ── Register routes ────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// Monitoring — must be registered before the catch-all
	mux.HandleFunc("/lb/health", lb.handleHealth)
	mux.HandleFunc("/lb/status", lb.handleStatus)
	mux.HandleFunc("/lb/metrics", lb.handleMetrics)

	// Catch-all → reverse proxy to backends
	mux.HandleFunc("/", lb.serveRequest)

	// ── Banner ─────────────────────────────────────────────────────────────
	divider := strings.Repeat("─", 52)
	fmt.Println(divider)
	fmt.Println("  Go Load Balancer — Chat App")
	fmt.Println(divider)
	fmt.Printf("  Internal port   : %d\n", *port)
	fmt.Printf("  External access : https://10.1.75.51:5269\n")
	fmt.Printf("  Health interval : %s\n", *healthInterval)
	fmt.Printf("  Backends (%d):\n", len(backends))
	for _, b := range backends {
		fmt.Printf("    ➜  %s\n", b.URL)
	}
	fmt.Println(divider)
	fmt.Println("  Monitoring endpoints:")
	fmt.Println("    GET /lb/health   → liveness check")
	fmt.Println("    GET /lb/status   → backend health + in-flight counts")
	fmt.Println("    GET /lb/metrics  → RPS, dropout%, latency percentiles")
	fmt.Println(divider)

	addr := fmt.Sprintf(":%d", *port)

	// ── Start server ───────────────────────────────────────────────────────
	certExists := false
	if *certFile != "" {
		if _, err := os.Stat(*certFile); err == nil {
			certExists = true
		}
	}

	if certExists {
		fmt.Printf("  Mode: HTTPS (cert: %s)\n", *certFile)
		fmt.Println(divider)
		if err := http.ListenAndServeTLS(addr, *certFile, *keyFile, mux); err != nil {
			log.Fatalf("[FATAL] HTTPS server error: %v", err)
		}
	} else {
		fmt.Printf("  Mode: HTTP (no cert found at %q — running plain HTTP)\n", *certFile)
		fmt.Println(divider)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("[FATAL] HTTP server error: %v", err)
		}
	}
}
