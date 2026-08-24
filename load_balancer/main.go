package main

import (
	"bufio"
	"context"
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

// Backend holds state for an individual backend server instance.
type Backend struct {
	URL          *url.URL
	Alive        atomic.Bool
	InFlight     atomic.Int64
	ReverseProxy *httputil.ReverseProxy
}

// Metrics tracks runtime operational performance counters.
type Metrics struct {
	Total         atomic.Uint64
	Success       atomic.Uint64
	Failed        atomic.Uint64
	BackendErrors atomic.Uint64
	LatencyMu     sync.Mutex
	Latencies     []time.Duration
}

// LoadBalancer manages backend routing, health monitoring, and metrics tracking.
type LoadBalancer struct {
	backends       []*Backend
	next           atomic.Uint64
	metrics        Metrics
	healthInterval time.Duration
	backendTimeout time.Duration
	httpClient     *http.Client
}

// NewLoadBalancer initializes the LoadBalancer with parsed backends and configurations.
func NewLoadBalancer(backendURLs []string, healthInterval, backendTimeout time.Duration) *LoadBalancer {
	lb := &LoadBalancer{
		healthInterval: healthInterval,
		backendTimeout: backendTimeout,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
	}

	for _, rawURL := range backendURLs {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			log.Fatalf("Invalid backend URL %q: %v", rawURL, err)
		}

		b := &Backend{
			URL: u,
		}
		b.Alive.Store(true)

		// Create reverse proxy with customized transport and timeout settings
		proxy := httputil.NewSingleHostReverseProxy(u)

		// Configure custom transport for strict timeouts and WebSocket/HTTP connection pooling
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   2 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   200,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: backendTimeout,
		}
		proxy.Transport = transport

		// Error handler for backend connection issues
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			b.Alive.Store(false)
			lb.metrics.BackendErrors.Add(1)
			lb.metrics.Failed.Add(1)
			log.Printf("[LB WARN] Backend %s failure: %v", b.URL.String(), err)
			http.Error(rw, `{"error": "backend unavailable"}`, http.StatusBadGateway)
		}

		b.ReverseProxy = proxy
		lb.backends = append(lb.backends, b)
	}

	return lb
}

// nextBackend selects the next alive backend using thread-safe Round-Robin scheduling.
func (lb *LoadBalancer) nextBackend() *Backend {
	n := len(lb.backends)
	if n == 0 {
		return nil
	}

	for i := 0; i < n; i++ {
		index := lb.next.Add(1) % uint64(n)
		b := lb.backends[index]
		if b.Alive.Load() {
			return b
		}
	}
	return nil
}

// healthCheck checks an individual backend's health via GET /health.
func (lb *LoadBalancer) healthCheck(b *Backend) {
	healthURL := fmt.Sprintf("%s://%s/health", b.URL.Scheme, b.URL.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		b.Alive.Store(false)
		return
	}

	resp, err := lb.httpClient.Do(req)
	if err != nil {
		if b.Alive.Load() {
			log.Printf("[HEALTH] Backend %s became UNHEALTHY (error: %v)", b.URL.String(), err)
		}
		b.Alive.Store(false)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if !b.Alive.Load() {
			log.Printf("[HEALTH] Backend %s RECOVERED -> HEALTHY", b.URL.String())
		}
		b.Alive.Store(true)
	} else {
		if b.Alive.Load() {
			log.Printf("[HEALTH] Backend %s returned status %d -> UNHEALTHY", b.URL.String(), resp.StatusCode)
		}
		b.Alive.Store(false)
	}
}

// healthLoop runs periodic background health checks across all configured backends.
func (lb *LoadBalancer) healthLoop() {
	ticker := time.NewTicker(lb.healthInterval)
	defer ticker.Stop()

	for range ticker.C {
		var wg sync.WaitGroup
		for _, b := range lb.backends {
			wg.Add(1)
			go func(backend *Backend) {
				defer wg.Done()
				lb.healthCheck(backend)
			}(b)
		}
		wg.Wait()
	}
}

// recordLatency stores duration into metrics slice with mutex lock.
func (lb *LoadBalancer) recordLatency(d time.Duration) {
	lb.metrics.LatencyMu.Lock()
	lb.metrics.Latencies = append(lb.metrics.Latencies, d)
	lb.metrics.LatencyMu.Unlock()
}

// ServeHTTP handles incoming requests, dispatching to LB management endpoints or forwarding to backends.
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Management endpoints
	switch r.URL.Path {
	case "/lb/health":
		lb.handleLBHealth(w, r)
		return
	case "/lb/status":
		lb.handleLBStatus(w, r)
		return
	case "/lb/metrics":
		lb.handleLBMetrics(w, r)
		return
	case "/lb/reset":
		lb.handleLBReset(w, r)
		return
	}

	// Normal workload proxying
	lb.metrics.Total.Add(1)
	start := time.Now()

	backend := lb.nextBackend()
	if backend == nil {
		lb.metrics.Failed.Add(1)
		http.Error(w, `{"error": "no healthy backends available"}`, http.StatusServiceUnavailable)
		return
	}

	backend.InFlight.Add(1)
	defer backend.InFlight.Add(-1)

	// Custom response recorder to capture status code
	recorder := &responseStatusRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}

	backend.ReverseProxy.ServeHTTP(recorder, r)

	elapsed := time.Since(start)
	lb.recordLatency(elapsed)

	if recorder.statusCode >= 200 && recorder.statusCode < 400 {
		lb.metrics.Success.Add(1)
	} else if recorder.statusCode >= 500 {
		lb.metrics.Failed.Add(1)
	} else {
		// Client error or redirection - still considered serviced
		lb.metrics.Success.Add(1)
	}
}

type responseStatusRecorder struct {
	http.ResponseWriter
	statusCode int
	wroteHead  bool
}

func (r *responseStatusRecorder) WriteHeader(code int) {
	if !r.wroteHead {
		r.statusCode = code
		r.wroteHead = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *responseStatusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHead {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// Hijack supports WebSocket upgrading for full bi-directional communication
func (r *responseStatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support Hijack")
}

func (lb *LoadBalancer) handleLBHealth(w http.ResponseWriter, r *http.Request) {
	healthyCount := 0
	for _, b := range lb.backends {
		if b.Alive.Load() {
			healthyCount++
		}
	}

	status := "healthy"
	if healthyCount == 0 && len(lb.backends) > 0 {
		status = "all_backends_down"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":           status,
		"total_backends":   len(lb.backends),
		"healthy_backends": healthyCount,
	})
}

func (lb *LoadBalancer) handleLBStatus(w http.ResponseWriter, r *http.Request) {
	type BackendStatus struct {
		URL      string `json:"url"`
		Alive    bool   `json:"alive"`
		InFlight int64  `json:"in_flight"`
	}

	var list []BackendStatus
	for _, b := range lb.backends {
		list = append(list, BackendStatus{
			URL:      b.URL.String(),
			Alive:    b.Alive.Load(),
			InFlight: b.InFlight.Load(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"backends": list,
	})
}

func (lb *LoadBalancer) handleLBMetrics(w http.ResponseWriter, r *http.Request) {
	lb.metrics.LatencyMu.Lock()
	count := len(lb.metrics.Latencies)
	latenciesCopy := make([]time.Duration, count)
	copy(latenciesCopy, lb.metrics.Latencies)
	lb.metrics.LatencyMu.Unlock()

	var p50, p95, p99, avgMs float64
	if count > 0 {
		sort.Slice(latenciesCopy, func(i, j int) bool {
			return latenciesCopy[i] < latenciesCopy[j]
		})

		var totalDur time.Duration
		for _, d := range latenciesCopy {
			totalDur += d
		}
		avgMs = float64(totalDur.Milliseconds()) / float64(count)

		p50 = float64(latenciesCopy[int(float64(count)*0.50)].Microseconds()) / 1000.0
		p95 = float64(latenciesCopy[int(float64(count)*0.95)].Microseconds()) / 1000.0
		p99 = float64(latenciesCopy[int(float64(count)*0.99)].Microseconds()) / 1000.0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total":          lb.metrics.Total.Load(),
		"success":        lb.metrics.Success.Load(),
		"failed":         lb.metrics.Failed.Load(),
		"backend_errors": lb.metrics.BackendErrors.Load(),
		"samples":        count,
		"avg_ms":         avgMs,
		"p50_ms":         p50,
		"p95_ms":         p95,
		"p99_ms":         p99,
	})
}

func (lb *LoadBalancer) handleLBReset(w http.ResponseWriter, r *http.Request) {
	lb.metrics.Total.Store(0)
	lb.metrics.Success.Store(0)
	lb.metrics.Failed.Store(0)
	lb.metrics.BackendErrors.Store(0)
	lb.metrics.LatencyMu.Lock()
	lb.metrics.Latencies = nil
	lb.metrics.LatencyMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "metrics reset successfully",
	})
}

func main() {
	port := flag.Int("port", 8080, "Port for Load Balancer to listen on (Sys1)")
	backendsArg := flag.String("backends", "http://127.0.0.1:8081,http://127.0.0.1:8082,http://127.0.0.1:8083", "Comma-separated list of backend URLs")
	healthInterval := flag.Duration("health-interval", 1*time.Second, "Interval between background health checks")
	backendTimeout := flag.Duration("backend-timeout", 800*time.Millisecond, "Backend request timeout")

	flag.Parse()

	rawParts := strings.Split(*backendsArg, ",")
	var backendList []string
	for _, p := range rawParts {
		p = strings.TrimSpace(p)
		if p != "" {
			backendList = append(backendList, p)
		}
	}

	if len(backendList) == 0 {
		log.Fatal("No valid backend URLs provided via -backends flag")
	}

	lb := NewLoadBalancer(backendList, *healthInterval, *backendTimeout)

	// Start background health checking goroutine
	go lb.healthLoop()

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	log.Printf("=========================================================")
	log.Printf(" 🚀 Load Balancer running on %s", addr)
	log.Printf(" 🎯 Configured Backends (%d total): %v", len(backendList), backendList)
	log.Printf(" ⏱️  Health Check Interval: %v | Backend Timeout: %v", *healthInterval, *backendTimeout)
	log.Printf(" 📊 Endpoints: /lb/health, /lb/status, /lb/metrics, /lb/reset")
	log.Printf("=========================================================")

	server := &http.Server{
		Addr:    addr,
		Handler: lb,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Load Balancer HTTP server failed: %v", err)
	}
}
