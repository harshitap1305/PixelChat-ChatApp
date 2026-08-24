package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"
)

type Response struct {
	Backend   string `json:"backend"`
	RequestID uint64 `json:"request_id"`
	DelayMs   int64  `json:"delay_ms"`
	Message   string `json:"message"`
}

type ServerState struct {
	name        string
	requestID   atomic.Uint64
	total       atomic.Uint64
	failures    atomic.Uint64
	failureRate float64
	defaultDelay time.Duration
}

func main() {
	name := flag.String("name", "backend-1", "Name identifier for this backend instance")
	port := flag.Int("port", 8081, "Port to listen on")
	failureRate := flag.Float64("failure-rate", 0.0, "Synthetic random failure rate (0.0 - 1.0)")
	defaultDelay := flag.Duration("delay", 0, "Default artificial delay for requests")

	flag.Parse()

	state := &ServerState{
		name:         *name,
		failureRate:  *failureRate,
		defaultDelay: *defaultDelay,
	}

	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Status endpoint
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"backend":      state.name,
			"total":        state.total.Load(),
			"failures":     state.failures.Load(),
			"failure_rate": state.failureRate,
		})
	})

	// Workload processing endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reqID := state.requestID.Add(1)
		state.total.Add(1)

		// Check for manual failure simulation: ?fail=true
		if r.URL.Query().Get("fail") == "true" {
			state.failures.Add(1)
			http.Error(w, `{"error": "manual synthetic failure"}`, http.StatusInternalServerError)
			return
		}

		// Check for random synthetic failure
		if state.failureRate > 0 && rand.Float64() < state.failureRate {
			state.failures.Add(1)
			http.Error(w, `{"error": "random synthetic failure"}`, http.StatusServiceUnavailable)
			return
		}

		// Calculate processing delay
		delay := state.defaultDelay
		if rawDelay := r.URL.Query().Get("delay"); rawDelay != "" {
			if parsed, err := time.ParseDuration(rawDelay); err == nil {
				delay = parsed
			}
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := Response{
			Backend:   state.name,
			RequestID: reqID,
			DelayMs:   delay.Milliseconds(),
			Message:   "ok",
		}
		json.NewEncoder(w).Encode(resp)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	log.Printf("=========================================================")
	log.Printf(" 🖥️  Backend [%s] listening on %s", *name, addr)
	log.Printf(" ⚙️  Failure Rate: %.2f | Default Delay: %v", *failureRate, *defaultDelay)
	log.Printf("=========================================================")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Backend %s failed: %v", *name, err)
	}
}
