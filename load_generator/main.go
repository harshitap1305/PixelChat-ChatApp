package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Result ───────────────────────────────────────────────────────────────────

type Result struct {
	Experiment     string  `json:"experiment"`
	Requests       int     `json:"requests"`
	Concurrency    int     `json:"concurrency"`
	Successful     int64   `json:"successful"`
	Failed         int64   `json:"failed"`
	ThroughputRPS  float64 `json:"throughput_rps"`
	DropoutPercent float64 `json:"dropout_percent"`
	P50Ms          float64 `json:"p50_ms"`
	P95Ms          float64 `json:"p95_ms"`
	P99Ms          float64 `json:"p99_ms"`
}

// ─── Worker Pool ──────────────────────────────────────────────────────────────

func runWorkers(url string, requests, concurrency int, timeout time.Duration) (successful, failed int64, latencies []time.Duration, elapsed time.Duration) {
	jobs := make(chan struct{}, requests)
	for i := 0; i < requests; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	var (
		succ      atomic.Int64
		fail      atomic.Int64
		mu        sync.Mutex
		latSlice  []time.Duration
	)

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: concurrency * 2,
		},
	}

	start := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				t0 := time.Now()
				resp, err := client.Get(url)
				lat := time.Since(t0)

				if err != nil || resp.StatusCode >= 500 {
					fail.Add(1)
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				resp.Body.Close()
				succ.Add(1)

				mu.Lock()
				latSlice = append(latSlice, lat)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	elapsed = time.Since(start)
	successful = succ.Load()
	failed = fail.Load()
	latencies = latSlice
	return
}

// ─── Metrics ──────────────────────────────────────────────────────────────────

// ThroughputRPS = float64(successful) / elapsed.Seconds()
func throughput(successful int64, elapsed time.Duration) float64 {
	if elapsed.Seconds() == 0 {
		return 0
	}
	return float64(successful) / elapsed.Seconds()
}

// dropout = (failed / total) * 100
func dropout(failed, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(failed) / float64(total) * 100
}

func percentileMs(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	return float64(sorted[idx].Milliseconds())
}

// ─── CSV ──────────────────────────────────────────────────────────────────────

var csvHeaders = []string{
	"experiment", "requests", "concurrency",
	"successful", "failed",
	"throughput_rps", "dropout_percent",
	"p50_ms", "p95_ms", "p99_ms",
}

func appendCSV(path string, r Result) error {
	_, statErr := os.Stat(path)
	writeHeader := os.IsNotExist(statErr)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if writeHeader {
		w.Write(csvHeaders)
	}
	w.Write([]string{
		r.Experiment,
		strconv.Itoa(r.Requests),
		strconv.Itoa(r.Concurrency),
		strconv.FormatInt(r.Successful, 10),
		strconv.FormatInt(r.Failed, 10),
		fmt.Sprintf("%.2f", r.ThroughputRPS),
		fmt.Sprintf("%.2f", r.DropoutPercent),
		fmt.Sprintf("%.1f", r.P50Ms),
		fmt.Sprintf("%.1f", r.P95Ms),
		fmt.Sprintf("%.1f", r.P99Ms),
	})
	w.Flush()
	return w.Error()
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	url        := flag.String("url",         "http://localhost:8080", "load balancer URL")
	requests   := flag.Int("requests",       5000,                    "total requests to send")
	concurrency := flag.Int("concurrency",   40,                      "concurrent workers")
	timeout    := flag.Duration("timeout",   5*time.Second,           "per-request timeout")
	experiment := flag.String("experiment",  "baseline",              "experiment name")
	out        := flag.String("out",         "results",               "output directory for JSON")
	csv        := flag.String("csv",         "results/comparison.csv","cumulative CSV file")
	flag.Parse()

	if err := os.MkdirAll(*out, 0755); err != nil {
		log.Fatalf("cannot create output dir: %v", err)
	}

	fmt.Printf("\nLoad Generator\n")
	fmt.Printf("  url         : %s\n", *url)
	fmt.Printf("  requests    : %d\n", *requests)
	fmt.Printf("  concurrency : %d\n", *concurrency)
	fmt.Printf("  experiment  : %s\n\n", *experiment)

	// Run workers
	succ, fail, latencies, elapsed := runWorkers(*url, *requests, *concurrency, *timeout)

	// Sort latencies for percentile calculation
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	total := succ + fail

	result := Result{
		Experiment:     *experiment,
		Requests:       *requests,
		Concurrency:    *concurrency,
		Successful:     succ,
		Failed:         fail,
		ThroughputRPS:  throughput(succ, elapsed),
		DropoutPercent: dropout(fail, total),
		P50Ms:          percentileMs(latencies, 50),
		P95Ms:          percentileMs(latencies, 95),
		P99Ms:          percentileMs(latencies, 99),
	}

	// Print result
	fmt.Printf("experiment      : %s\n", result.Experiment)
	fmt.Printf("requests        : %d\n", result.Requests)
	fmt.Printf("concurrency     : %d\n", result.Concurrency)
	fmt.Printf("successful      : %d\n", result.Successful)
	fmt.Printf("failed          : %d\n", result.Failed)
	fmt.Printf("throughput_rps  : %.2f\n", result.ThroughputRPS)
	fmt.Printf("dropout_percent : %.2f%%\n", result.DropoutPercent)
	fmt.Printf("p50_ms          : %.1f\n", result.P50Ms)
	fmt.Printf("p95_ms          : %.1f\n", result.P95Ms)
	fmt.Printf("p99_ms          : %.1f\n\n", result.P99Ms)

	// Save JSON
	jsonPath := filepath.Join(*out, *experiment+".json")
	jf, err := os.Create(jsonPath)
	if err != nil {
		log.Printf("warning: could not write JSON: %v", err)
	} else {
		enc := json.NewEncoder(jf)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		jf.Close()
		fmt.Printf("JSON saved -> %s\n", jsonPath)
	}

	// Save CSV
	if err := appendCSV(*csv, result); err != nil {
		log.Printf("warning: could not write CSV: %v", err)
	} else {
		fmt.Printf("CSV  saved -> %s\n", *csv)
	}
}
