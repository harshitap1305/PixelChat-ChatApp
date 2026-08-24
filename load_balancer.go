package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"crypto/tls"
)

type Backend struct {
	URL      *url.URL
	Alive    atomic.Bool
	InFlight atomic.Int64
}

type Metrics struct {
	Total         atomic.Uint64
	Success       atomic.Uint64
	Failed        atomic.Uint64
	BackendErrors atomic.Uint64
	LatencyMu     sync.Mutex
	Latencies     []time.Duration
}

type LoadBalancer struct {
	backends  []*Backend
	next      atomic.Uint64
	metrics   *Metrics
	transport http.RoundTripper
}

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

func (lb *LoadBalancer) healthLoop(interval time.Duration) {
	for {
		for _, backend := range lb.backends {
			healthURL := backend.URL.String() + "/health"
			client := http.Client{
				Timeout: 2 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			}
			resp, err := client.Get(healthURL)
			if err == nil && resp.StatusCode == http.StatusOK {
				backend.Alive.Store(true)
			} else {
				backend.Alive.Store(false)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		time.Sleep(interval)
	}
}

func main() {
	var backendsFlag string
	var port int
	var healthInterval time.Duration
	var backendTimeout time.Duration

	flag.StringVar(&backendsFlag, "backends", "", "Comma-separated list of backend URLs")
	flag.IntVar(&port, "port", 8080, "Port to run the load balancer on")
	flag.DurationVar(&healthInterval, "health-interval", 1*time.Second, "Health check interval")
	flag.DurationVar(&backendTimeout, "backend-timeout", 3*time.Second, "Backend request timeout")
	flag.Parse()

	if backendsFlag == "" {
		log.Fatal("Please provide backend URLs using -backends")
	}

	parts := strings.Split(backendsFlag, ",")
	var backends []*Backend
	for _, part := range parts {
		u, err := url.Parse(strings.TrimSpace(part))
		if err != nil {
			log.Fatalf("Invalid backend URL: %s", part)
		}
		b := &Backend{URL: u}
		b.Alive.Store(true)
		backends = append(backends, b)
	}

	// Shared transport with DialContext + ResponseHeader timeouts
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout:   backendTimeout,
		TLSHandshakeTimeout:     2 * time.Second,
		MaxIdleConns:            200,
		MaxIdleConnsPerHost:     100,
		IdleConnTimeout:         90 * time.Second,
		TLSClientConfig:         &tls.Config{InsecureSkipVerify: true},
	}

	lb := &LoadBalancer{
		backends:  backends,
		metrics:   &Metrics{},
		transport: transport,
	}

	go lb.healthLoop(healthInterval)

	mux := http.NewServeMux()

	mux.HandleFunc("/lb/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "LB is alive")
	})

	mux.HandleFunc("/lb/status", func(w http.ResponseWriter, r *http.Request) {
		type backendStatus struct {
			URL   string `json:"url"`
			Alive bool   `json:"alive"`
		}
		var statuses []backendStatus
		for _, b := range lb.backends {
			statuses = append(statuses, backendStatus{
				URL:   b.URL.String(),
				Alive: b.Alive.Load(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"backends": statuses})
	})

	mux.HandleFunc("/lb/metrics", func(w http.ResponseWriter, r *http.Request) {
		lb.metrics.LatencyMu.Lock()
		latencyCount := len(lb.metrics.Latencies)
		lb.metrics.LatencyMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total":          lb.metrics.Total.Load(),
			"success":        lb.metrics.Success.Load(),
			"failed":         lb.metrics.Failed.Load(),
			"backend_errors": lb.metrics.BackendErrors.Load(),
			"latency_count":  latencyCount,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lb.metrics.Total.Add(1)

		backend := lb.nextBackend()
		if backend == nil {
			lb.metrics.Failed.Add(1)
			http.Error(w, "No healthy backends available", http.StatusServiceUnavailable)
			return
		}

		backend.InFlight.Add(1)
		defer backend.InFlight.Add(-1)

		proxy := httputil.NewSingleHostReverseProxy(backend.URL)

		// Use the shared transport (with DialContext + ResponseHeader timeouts)
		proxy.Transport = lb.transport

		// Track whether the error handler fired to prevent double-counting
		proxyFailed := false

		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			backend.Alive.Store(false)
			lb.metrics.BackendErrors.Add(1)
			lb.metrics.Failed.Add(1)
			proxyFailed = true
			http.Error(rw, "backend unavailable", http.StatusBadGateway)
		}

		// Use a custom response writer to capture the status code
		cw := &customResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		proxy.ServeHTTP(cw, r)

		duration := time.Since(start)
		lb.metrics.LatencyMu.Lock()
		lb.metrics.Latencies = append(lb.metrics.Latencies, duration)
		lb.metrics.LatencyMu.Unlock()

		// Only count success/fail if the error handler did NOT already handle it
		if !proxyFailed {
			if cw.statusCode >= 500 {
				lb.metrics.Failed.Add(1)
				lb.metrics.BackendErrors.Add(1)
			} else {
				lb.metrics.Success.Add(1)
			}
		}
	})

	fmt.Printf("Load Balancer running on http://localhost:%d\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), mux))
}

type customResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (cw *customResponseWriter) WriteHeader(code int) {
	cw.statusCode = code
	cw.ResponseWriter.WriteHeader(code)
}
