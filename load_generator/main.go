/*
Load Generator — main.go
=========================
A concurrent HTTP load generator for measuring load-balancer performance.

Modes:
  message  — only POST /message   (write-only benchmark)
  feed     — only GET  /feed      (read-only benchmark)
  mixed    — interleaves both according to -read-ratio (default 0.2 → 20% reads)
  health   — only GET  /health

Results are written to:
  <experiment>.json      — full summary including per-endpoint breakdown
  results.csv            — cumulative comparison table (appended)
  utilization.csv        — per-2s CPU/Valkey RTT for all polled health endpoints

Usage examples:

  # Write-only
  ./load_generator -url https://10.1.75.51:5269 -requests 25000 -concurrency 200 \
                   -experiment writes_lb -path /message -mode message \
                   -users 100 -min-len 10 -max-len 300 -out ./results

  # Read-only
  ./load_generator -url https://10.1.75.51:5269 -requests 5000 -concurrency 50 \
                   -experiment reads_lb -path /feed -mode feed -out ./results

  # Mixed (80% writes, 20% reads) with utilization tracking
  ./load_generator -url https://10.1.75.51:5269 -requests 25000 -concurrency 200 \
                   -experiment mixed_lb -mode mixed -read-ratio 0.2 \
                   -users 100 -min-len 10 -max-len 300 \
                   -health-urls https://10.1.75.51:5270/health,https://10.1.75.51:5271/health,https://10.1.75.51:5272/health \
                   -out ./results
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
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── Result types ──────────────────────────────────────────────────────────────

type requestResult struct {
	latency time.Duration
	success bool
	isRead  bool // true = GET /feed, false = POST /message
}

type ExperimentResult struct {
	// Overall
	Experiment        string  `json:"experiment"`
	TargetURL         string  `json:"target_url"`
	StartTimestampSec int64   `json:"start_timestamp_sec"`
	TotalRequests     int     `json:"requests"`
	Concurrency       int     `json:"concurrency"`
	Successful        int64   `json:"successful"`
	Failed            int64   `json:"failed"`
	ThroughputRPS     float64 `json:"throughput_rps"`
	DropoutPercent    float64 `json:"dropout_percent"`
	P50Ms             float64 `json:"p50_ms"`
	P95Ms             float64 `json:"p95_ms"`
	P99Ms             float64 `json:"p99_ms"`
	ElapsedSec        float64 `json:"elapsed_sec"`

	// Write (POST /message) breakdown — populated in message and mixed modes
	WriteSuccessful  int64   `json:"write_successful"`
	WriteTputRPS     float64 `json:"write_throughput_rps"`
	WriteP50Ms       float64 `json:"write_p50_ms"`
	WriteP95Ms       float64 `json:"write_p95_ms"`
	WriteP99Ms       float64 `json:"write_p99_ms"`

	// Read (GET /feed) breakdown — populated in feed and mixed modes
	ReadSuccessful   int64   `json:"read_successful"`
	ReadTputRPS      float64 `json:"read_throughput_rps"`
	ReadP50Ms        float64 `json:"read_p50_ms"`
	ReadP95Ms        float64 `json:"read_p95_ms"`
	ReadP99Ms        float64 `json:"read_p99_ms"`
}

// WorkerConfig bundles per-worker variability parameters.
type WorkerConfig struct {
	Users     []string
	MinLen    int
	MaxLen    int
	MinDelay  time.Duration
	MaxDelay  time.Duration
	ReadRatio float64 // fraction of requests sent to /feed (0.0 = all writes)
	WritePath string  // e.g. /message
	ReadPath  string  // e.g. /feed
}

// ── Random helpers ─────────────────────────────────────────────────────────────

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func generateUserPool(n int) []string {
	users := make([]string, n)
	for i := range users {
		users[i] = fmt.Sprintf("user-%s", randomString(6))
	}
	return users
}

// ── Workers ───────────────────────────────────────────────────────────────────

func worker(
	id int,
	targetURL string,
	mode string,
	cfg WorkerConfig,
	client *http.Client,
	jobs <-chan int,
	results chan<- requestResult,
	wg *sync.WaitGroup,
	done *atomic.Int64,
	total int,
) {
	defer wg.Done()

	for range jobs {
		// Optional random inter-request delay
		if cfg.MaxDelay > 0 {
			jitter := cfg.MinDelay
			if cfg.MaxDelay > cfg.MinDelay {
				jitter += time.Duration(rand.Int63n(int64(cfg.MaxDelay - cfg.MinDelay)))
			}
			time.Sleep(jitter)
		}

		// Decide write vs read
		doRead := false
		switch mode {
		case "feed":
			doRead = true
		case "mixed":
			doRead = rand.Float64() < cfg.ReadRatio
		}

		start := time.Now()
		var resp *http.Response
		var err error

		if doRead {
			resp, err = client.Get(targetURL + cfg.ReadPath)
		} else if mode == "message" || mode == "mixed" {
			username := cfg.Users[rand.Intn(len(cfg.Users))]
			msgLen := cfg.MinLen
			if cfg.MaxLen > cfg.MinLen {
				msgLen += rand.Intn(cfg.MaxLen - cfg.MinLen + 1)
			}
			msg := randomString(msgLen)
			payload := []byte(fmt.Sprintf(`{"client-name":%q,"msg":%q}`, username, msg))
			resp, err = client.Post(targetURL+cfg.WritePath, "application/json", bytes.NewBuffer(payload))
		} else {
			// health / other GET
			resp, err = client.Get(targetURL + cfg.WritePath)
		}

		latency := time.Since(start)
		success := false
		if err == nil && resp != nil {
			success = resp.StatusCode >= 200 && resp.StatusCode < 300
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		results <- requestResult{latency: latency, success: success, isRead: doRead}

		n := done.Add(1)
		if total >= 100 && n%(int64(total/10)) == 0 {
			log.Printf("[Worker %d] Progress: %d/%d (%.0f%%)", id, n, total,
				float64(n)/float64(total)*100)
		}
	}
}

// ── Percentile helpers ────────────────────────────────────────────────────────

func percentile(sorted []time.Duration, pct float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(float64(n-1) * pct / 100.0)
	return float64(sorted[idx].Milliseconds())
}

// ── Utilization tracking ──────────────────────────────────────────────────────

type utilizationRecord struct {
	TimestampSec int64
	URL          string
	CPUPercent   float64
	LoadAvg1m    float64
	ValkeyRTTms  float64
	WSConns      int
}

func appendUtilizationCSV(path string, records []utilizationRecord) error {
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
			"timestamp_sec", "url",
			"cpu_percent", "load_avg_1m",
			"valkey_rtt_ms", "active_ws_connections",
		})
	}
	for _, r := range records {
		_ = w.Write([]string{
			fmt.Sprintf("%d", r.TimestampSec),
			r.URL,
			fmt.Sprintf("%.2f", r.CPUPercent),
			fmt.Sprintf("%.2f", r.LoadAvg1m),
			fmt.Sprintf("%.2f", r.ValkeyRTTms),
			fmt.Sprintf("%d", r.WSConns),
		})
	}
	return nil
}

func pollUtilization(healthURLs []string, outPath string, client *http.Client, stopCh <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			ts := time.Now().Unix()
			var records []utilizationRecord
			for _, u := range healthURLs {
				resp, err := client.Get(u)
				if err != nil {
					continue
				}
				var h map[string]interface{}
				if err2 := json.NewDecoder(resp.Body).Decode(&h); err2 != nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				rec := utilizationRecord{TimestampSec: ts, URL: u}
				if v, ok := h["cpu_percent"].(float64); ok { rec.CPUPercent = v }
				if v, ok := h["load_avg_1m"].(float64); ok { rec.LoadAvg1m = v }
				if v, ok := h["valkey_rtt_ms"].(float64); ok { rec.ValkeyRTTms = v }
				if v, ok := h["active_ws_connections"].(float64); ok { rec.WSConns = int(v) }
				records = append(records, rec)
				log.Printf("[UTIL] %s → cpu=%.1f%% valkey_rtt=%.1fms ws=%d",
					u, rec.CPUPercent, rec.ValkeyRTTms, rec.WSConns)
			}
			if len(records) > 0 {
				if err := appendUtilizationCSV(outPath, records); err != nil {
					log.Printf("[WARN] utilization CSV: %v", err)
				}
			}
		}
	}
}

// ── CSV helpers ───────────────────────────────────────────────────────────────

func appendCSV(path string, res ExperimentResult) error {
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
			"experiment", "target_url", "start_timestamp_sec", "requests", "concurrency",
			"successful", "failed", "throughput_rps", "dropout_pct",
			"p50_ms", "p95_ms", "p99_ms", "elapsed_sec",
			"write_successful", "write_throughput_rps", "write_p50_ms", "write_p95_ms", "write_p99_ms",
			"read_successful",  "read_throughput_rps",  "read_p50_ms",  "read_p95_ms",  "read_p99_ms",
		})
	}
	return w.Write([]string{
		res.Experiment, res.TargetURL,
		fmt.Sprintf("%d", res.StartTimestampSec),
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
		fmt.Sprintf("%d",   res.WriteSuccessful),
		fmt.Sprintf("%.2f", res.WriteTputRPS),
		fmt.Sprintf("%.2f", res.WriteP50Ms),
		fmt.Sprintf("%.2f", res.WriteP95Ms),
		fmt.Sprintf("%.2f", res.WriteP99Ms),
		fmt.Sprintf("%d",   res.ReadSuccessful),
		fmt.Sprintf("%.2f", res.ReadTputRPS),
		fmt.Sprintf("%.2f", res.ReadP50Ms),
		fmt.Sprintf("%.2f", res.ReadP95Ms),
		fmt.Sprintf("%.2f", res.ReadP99Ms),
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	targetURL   := flag.String("url",         "https://10.1.75.51:5269", "Target URL.")
	requests    := flag.Int("requests",        5000,                       "Total requests to send.")
	concurrency := flag.Int("concurrency",     40,                         "Concurrent workers.")
	timeout     := flag.Duration("timeout",    5*time.Second,              "Per-request timeout.")
	experiment  := flag.String("experiment",   "experiment",               "Name tag (used in filenames).")
	outDir      := flag.String("out",          ".",                        "Output directory.")
	mode        := flag.String("mode",         "health",
		"Mode: 'message' (POST /message only), 'feed' (GET /feed only),\n"+
			"      'mixed' (both, controlled by -read-ratio), 'health' (GET /health)")
	writePath   := flag.String("path",         "/message",                 "Path for writes / non-mixed modes.")
	readPath    := flag.String("feed-path",    "/feed?limit=100",          "Path for reads in feed/mixed mode.")
	pollLB      := flag.Bool("poll-lb",        false,                      "Periodically log /lb/status.")
	readRatio   := flag.Float64("read-ratio",  0.2,                        "Fraction of mixed-mode requests that are GET /feed (0.0–1.0).")

	// Variability
	numUsers  := flag.Int("users",       50,  "Distinct simulated usernames.")
	minLen    := flag.Int("min-len",     10,  "Min message length (chars).")
	maxLen    := flag.Int("max-len",     200, "Max message length (chars).")
	minDelay  := flag.Duration("min-delay", 0, "Min sleep between requests per worker.")
	maxDelay  := flag.Duration("max-delay", 0, "Max sleep between requests per worker.")

	healthURLsRaw := flag.String("health-urls", "",
		"Comma-separated /health URLs to poll every 2s → utilization.csv")

	flag.Parse()

	if *requests <= 0 || *concurrency <= 0 {
		log.Fatal("requests and concurrency must be > 0")
	}
	if *minLen < 1 { *minLen = 1 }
	if *maxLen < *minLen { *maxLen = *minLen }
	if *numUsers < 1 { *numUsers = 1 }
	if *readRatio < 0 { *readRatio = 0 }
	if *readRatio > 1 { *readRatio = 1 }

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // #nosec G402
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
		},
	}
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("Cannot create output dir %q: %v", *outDir, err)
	}

	cfg := WorkerConfig{
		Users:     generateUserPool(*numUsers),
		MinLen:    *minLen,
		MaxLen:    *maxLen,
		MinDelay:  *minDelay,
		MaxDelay:  *maxDelay,
		ReadRatio: *readRatio,
		WritePath: *writePath,
		ReadPath:  *readPath,
	}

	divider := "────────────────────────────────────────────────────"
	fmt.Println(divider)
	fmt.Println("  Go Load Generator")
	fmt.Println(divider)
	fmt.Printf("  Experiment   : %s\n", *experiment)
	fmt.Printf("  Target URL   : %s\n", *targetURL)
	fmt.Printf("  Mode         : %s\n", *mode)
	if *mode == "mixed" {
		fmt.Printf("  Write path   : %s  (%.0f%%)\n", *writePath, (1-*readRatio)*100)
		fmt.Printf("  Read  path   : %s   (%.0f%%)\n", *readPath, *readRatio*100)
	}
	fmt.Printf("  Requests     : %d\n", *requests)
	fmt.Printf("  Concurrency  : %d workers\n", *concurrency)
	fmt.Printf("  Users pool   : %d distinct users\n", *numUsers)
	fmt.Printf("  Msg length   : %d–%d chars\n", *minLen, *maxLen)
	fmt.Printf("  Request delay: %s–%s\n", *minDelay, *maxDelay)
	fmt.Println(divider)

	jobs    := make(chan int, *requests)
	results := make(chan requestResult, *requests)
	var wg   sync.WaitGroup
	var done atomic.Int64

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go worker(i+1, *targetURL, *mode, cfg, client, jobs, results, &wg, &done, *requests)
	}

	if *pollLB {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			for range ticker.C {
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

	var utilStopCh chan struct{}
	if *healthURLsRaw != "" {
		urls := strings.Split(*healthURLsRaw, ",")
		for i, u := range urls { urls[i] = strings.TrimSpace(u) }
		utilCSVPath := filepath.Join(*outDir, "utilization.csv")
		utilStopCh = make(chan struct{})
		go pollUtilization(urls, utilCSVPath, client, utilStopCh)
		log.Printf("[UTIL] Polling %d health endpoints → %s", len(urls), utilCSVPath)
	}

	experimentStart := time.Now()
	for i := 0; i < *requests; i++ { jobs <- i }
	close(jobs)
	go func() { wg.Wait(); close(results) }()

	// ── Collect results ────────────────────────────────────────────────────
	var (
		successful, failed                     int64
		writeLatencies, readLatencies          []time.Duration
	)
	for r := range results {
		if r.success {
			successful++
			if r.isRead {
				readLatencies = append(readLatencies, r.latency)
			} else {
				writeLatencies = append(writeLatencies, r.latency)
			}
		} else {
			failed++
		}
	}

	elapsed := time.Since(experimentStart)
	if utilStopCh != nil { close(utilStopCh) }

	allLatencies := append(append([]time.Duration{}, writeLatencies...), readLatencies...)
	sort.Slice(allLatencies,   func(i, j int) bool { return allLatencies[i]   < allLatencies[j] })
	sort.Slice(writeLatencies, func(i, j int) bool { return writeLatencies[i] < writeLatencies[j] })
	sort.Slice(readLatencies,  func(i, j int) bool { return readLatencies[i]  < readLatencies[j] })

	throughput := float64(successful) / elapsed.Seconds()
	total := successful + failed
	dropoutPct := 0.0
	if total > 0 { dropoutPct = float64(failed) / float64(total) * 100.0 }

	writeTput := float64(len(writeLatencies)) / elapsed.Seconds()
	readTput  := float64(len(readLatencies))  / elapsed.Seconds()

	res := ExperimentResult{
		Experiment:        *experiment,
		TargetURL:         *targetURL,
		StartTimestampSec: experimentStart.Unix(),
		TotalRequests:     *requests,
		Concurrency:       *concurrency,
		Successful:        successful,
		Failed:         failed,
		ThroughputRPS:  throughput,
		DropoutPercent: dropoutPct,
		P50Ms:          percentile(allLatencies, 50),
		P95Ms:          percentile(allLatencies, 95),
		P99Ms:          percentile(allLatencies, 99),
		ElapsedSec:     elapsed.Seconds(),
		WriteSuccessful: int64(len(writeLatencies)),
		WriteTputRPS:    writeTput,
		WriteP50Ms:      percentile(writeLatencies, 50),
		WriteP95Ms:      percentile(writeLatencies, 95),
		WriteP99Ms:      percentile(writeLatencies, 99),
		ReadSuccessful:  int64(len(readLatencies)),
		ReadTputRPS:     readTput,
		ReadP50Ms:       percentile(readLatencies, 50),
		ReadP95Ms:       percentile(readLatencies, 95),
		ReadP99Ms:       percentile(readLatencies, 99),
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
	fmt.Println("  Overall latency percentiles:")
	fmt.Printf("  p50 : %.2f ms\n", res.P50Ms)
	fmt.Printf("  p95 : %.2f ms\n", res.P95Ms)
	fmt.Printf("  p99 : %.2f ms\n", res.P99Ms)
	if len(writeLatencies) > 0 {
		fmt.Println(divider)
		fmt.Printf("  Writes (%d req, %.2f req/s):\n", len(writeLatencies), writeTput)
		fmt.Printf("  p50 : %.2f ms  p95 : %.2f ms  p99 : %.2f ms\n",
			res.WriteP50Ms, res.WriteP95Ms, res.WriteP99Ms)
	}
	if len(readLatencies) > 0 {
		fmt.Println(divider)
		fmt.Printf("  Reads  (%d req, %.2f req/s):\n", len(readLatencies), readTput)
		fmt.Printf("  p50 : %.2f ms  p95 : %.2f ms  p99 : %.2f ms\n",
			res.ReadP50Ms, res.ReadP95Ms, res.ReadP99Ms)
	}
	fmt.Println(divider)

	jsonPath := filepath.Join(*outDir, *experiment+".json")
	jsonData, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		log.Printf("[WARN] JSON: %v", err)
	} else {
		fmt.Printf("  JSON saved  → %s\n", jsonPath)
	}

	csvPath := filepath.Join(*outDir, "results.csv")
	if err := appendCSV(csvPath, res); err != nil {
		log.Printf("[WARN] CSV: %v", err)
	} else {
		fmt.Printf("  CSV appended → %s\n", csvPath)
	}
	fmt.Println(divider)
}
