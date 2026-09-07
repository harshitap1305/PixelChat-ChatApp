/*
Load Generator — main.go
=========================
A concurrent HTTP load generator for measuring load-balancer performance.

Spawns N worker goroutines that pull jobs from a shared channel and send
HTTP GET requests to the target URL. Records per-request latency and
computes throughput, dropout percentage and latency percentiles.

Results are written to:
  <experiment>.json    — single experiment summary
  results.csv          — cumulative comparison table (appended)

Usage examples:

  # Experiment 1 — single backend (bypass LB, hit Sys2 directly)
  go run . -url https://10.1.75.51:5270 -requests 1000 -concurrency 20 \
           -experiment single_backend -path /health

  # Experiment 2 — three backends via Load Balancer
  go run . -url https://10.1.75.51:5269 -requests 5000 -concurrency 40 \
           -experiment three_backends -path /health

  # With simulated delay (tests LB timeout behaviour)
  go run . -url https://10.1.75.51:5269 -requests 1000 -concurrency 20 \
           -experiment delay_100ms -path '/health?delay=100ms'
*/

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ── Result types ──────────────────────────────────────────────────────────────

// requestResult holds the outcome of a single HTTP request.
type requestResult struct {
	latency time.Duration
	success bool
}

// ExperimentResult is the final summary written to JSON/CSV.
type ExperimentResult struct {
	Experiment     string  `json:"experiment"`
	TargetURL      string  `json:"target_url"`
	TotalRequests  int     `json:"requests"`
	Concurrency    int     `json:"concurrency"`
	Successful     int64   `json:"successful"`
	Failed         int64   `json:"failed"`
	ThroughputRPS  float64 `json:"throughput_rps"`
	DropoutPercent float64 `json:"dropout_percent"`
	P50Ms          float64 `json:"p50_ms"`
	P95Ms          float64 `json:"p95_ms"`
	P99Ms          float64 `json:"p99_ms"`
	ElapsedSec     float64 `json:"elapsed_sec"`
}

// ── Workers ───────────────────────────────────────────────────────────────────

// worker pulls job indices from jobs channel and sends HTTP requests.
func worker(
	id int,
	targetURL string,
	path string,
	mode string,
	client *http.Client,
	jobs <-chan int,
	results chan<- requestResult,
	wg *sync.WaitGroup,
	done *atomic.Int64,
	total int,
) {
	defer wg.Done()
	fullURL := targetURL + path

	for i := range jobs {
		start := time.Now()
		var resp *http.Response
		var err error

		if mode == "message" {
			payload := []byte(fmt.Sprintf(`{"client-name": "load-tester-%d", "msg": "Load test message %d"}`, id, i))
			resp, err = client.Post(fullURL, "application/json", bytes.NewBuffer(payload))
		} else {
			resp, err = client.Get(fullURL)
		}

		latency := time.Since(start)

		success := false
		if err == nil && resp != nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				success = true
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		results <- requestResult{latency: latency, success: success}

		// Progress logging every 10%
		n := done.Add(1)
		if total >= 100 && n%(int64(total/10)) == 0 {
			pct := float64(n) / float64(total) * 100
			log.Printf("[Worker %d] Progress: %d/%d (%.0f%%)", id, n, total, pct)
		}
	}
}

// ── Percentile helpers ────────────────────────────────────────────────────────

func percentile(sorted []time.Duration, pct float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	i := int(float64(n)*pct/100) - 1
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return float64(sorted[i].Microseconds()) / 1000.0
}

// ── CSV helpers ───────────────────────────────────────────────────────────────

func appendCSV(path string, res ExperimentResult) error {
	// Write header if file is new
	needHeader := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		needHeader = true
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	if needHeader {
		_ = w.Write([]string{
			"experiment", "target_url", "requests", "concurrency",
			"successful", "failed", "throughput_rps",
			"dropout_pct", "p50_ms", "p95_ms", "p99_ms", "elapsed_sec",
		})
	}

	return w.Write([]string{
		res.Experiment,
		res.TargetURL,
		fmt.Sprintf("%d", res.TotalRequests),
		fmt.Sprintf("%d", res.Concurrency),
		fmt.Sprintf("%d", res.Successful),
		fmt.Sprintf("%d", res.Failed),
		fmt.Sprintf("%.2f", res.ThroughputRPS),
		fmt.Sprintf("%.2f", res.DropoutPercent),
		fmt.Sprintf("%.2f", res.P50Ms),
		fmt.Sprintf("%.2f", res.P95Ms),
		fmt.Sprintf("%.2f", res.P99Ms),
		fmt.Sprintf("%.3f", res.ElapsedSec),
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	targetURL := flag.String("url", "https://10.1.75.51:5269",
		"Target URL (Load Balancer or backend).")
	requests := flag.Int("requests", 5000,
		"Total number of HTTP requests to send.")
	concurrency := flag.Int("concurrency", 40,
		"Number of concurrent worker goroutines.")
	timeout := flag.Duration("timeout", 5*time.Second,
		"Per-request HTTP timeout.")
	experiment := flag.String("experiment", "experiment",
		"Name tag for this run (used in output filenames).")
	outDir := flag.String("out", ".",
		"Directory for output JSON and CSV files.")
	path := flag.String("path", "/health",
		"HTTP path to request on the target (e.g. /health or /message).")
	mode := flag.String("mode", "health",
		"Mode: 'health' (GET) or 'message' (POST)")
	pollLB := flag.Bool("poll-lb", false,
		"If true, query LB status/metrics periodically")
	flag.Parse()

	// Validate
	if *requests <= 0 || *concurrency <= 0 {
		log.Fatal("requests and concurrency must be > 0")
	}

	// HTTP client — skip TLS verify for self-signed certs
	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // #nosec G402
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
		},
	}

	// Ensure output directory exists
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("Cannot create output dir %q: %v", *outDir, err)
	}

	divider := "────────────────────────────────────────────────────"
	fmt.Println(divider)
	fmt.Println("  Go Load Generator")
	fmt.Println(divider)
	fmt.Printf("  Experiment  : %s\n", *experiment)
	fmt.Printf("  Target URL  : %s%s\n", *targetURL, *path)
	fmt.Printf("  Requests    : %d\n", *requests)
	fmt.Printf("  Concurrency : %d workers\n", *concurrency)
	fmt.Printf("  Timeout     : %s\n", *timeout)
	fmt.Println(divider)

	// ── Worker pool ────────────────────────────────────────────────────────
	jobs := make(chan int, *requests)
	results := make(chan requestResult, *requests)

	var wg sync.WaitGroup
	var done atomic.Int64

	// Launch workers
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go worker(i+1, *targetURL, *path, *mode, client, jobs, results, &wg, &done, *requests)
	}

	if *pollLB {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			for {
				<-ticker.C
				resp, err := client.Get(*targetURL + "/lb/status")
				if err == nil {
					var s map[string]interface{}
					json.NewDecoder(resp.Body).Decode(&s)
					log.Printf("[POLL] LB Status: %v", s)
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			}
		}()
	}

	// Send jobs
	experimentStart := time.Now()
	for i := 0; i < *requests; i++ {
		jobs <- i
	}
	close(jobs)

	// Wait for all workers to finish then close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// ── Collect results ────────────────────────────────────────────────────
	var (
		successful int64
		failed     int64
		latencies  []time.Duration
	)

	for r := range results {
		if r.success {
			successful++
			latencies = append(latencies, r.latency)
		} else {
			failed++
		}
	}

	elapsed := time.Since(experimentStart)

	// ── Compute metrics ────────────────────────────────────────────────────
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	throughput := 0.0
	if elapsed.Seconds() > 0 {
		throughput = float64(successful) / elapsed.Seconds()
	}

	total := successful + failed
	dropoutPct := 0.0
	if total > 0 {
		dropoutPct = float64(failed) / float64(total) * 100.0
	}

	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)
	p99 := percentile(latencies, 99)

	res := ExperimentResult{
		Experiment:     *experiment,
		TargetURL:      *targetURL + *path,
		TotalRequests:  *requests,
		Concurrency:    *concurrency,
		Successful:     successful,
		Failed:         failed,
		ThroughputRPS:  throughput,
		DropoutPercent: dropoutPct,
		P50Ms:          p50,
		P95Ms:          p95,
		P99Ms:          p99,
		ElapsedSec:     elapsed.Seconds(),
	}

	// ── Print summary ──────────────────────────────────────────────────────
	fmt.Println(divider)
	fmt.Println("  Results")
	fmt.Println(divider)
	fmt.Printf("  Total sent   : %d\n", *requests)
	fmt.Printf("  Successful   : %d\n", successful)
	fmt.Printf("  Failed       : %d\n", failed)
	fmt.Printf("  Dropout      : %.2f%%\n", dropoutPct)
	fmt.Printf("  Elapsed      : %.3fs\n", elapsed.Seconds())
	fmt.Printf("  Throughput   : %.2f req/s\n", throughput)
	fmt.Println(divider)
	fmt.Println("  Latency percentiles (successful requests only):")
	fmt.Printf("  p50 : %.2f ms\n", p50)
	fmt.Printf("  p95 : %.2f ms\n", p95)
	fmt.Printf("  p99 : %.2f ms\n", p99)
	fmt.Println(divider)

	// ── Save JSON ──────────────────────────────────────────────────────────
	jsonPath := filepath.Join(*outDir, *experiment+".json")
	jsonData, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		log.Printf("[WARN] Could not write JSON: %v", err)
	} else {
		fmt.Printf("  JSON saved  → %s\n", jsonPath)
	}

	// ── Append CSV ─────────────────────────────────────────────────────────
	csvPath := filepath.Join(*outDir, "results.csv")
	if err := appendCSV(csvPath, res); err != nil {
		log.Printf("[WARN] Could not write CSV: %v", err)
	} else {
		fmt.Printf("  CSV appended → %s\n", csvPath)
	}

	fmt.Println(divider)
}
