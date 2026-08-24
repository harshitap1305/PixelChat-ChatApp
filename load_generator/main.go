package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type RequestResult struct {
	Success    bool
	Latency    time.Duration
	StatusCode int
	Backend    string
	Err        error
}

type ExperimentResult struct {
	Experiment     string             `json:"experiment"`
	TargetURL      string             `json:"target_url"`
	Requests       int                `json:"requests"`
	Concurrency    int                `json:"concurrency"`
	Successful     int                `json:"successful"`
	Failed         int                `json:"failed"`
	ElapsedTimeSec float64            `json:"elapsed_time_sec"`
	ThroughputRPS  float64            `json:"throughput_rps"`
	DropoutPercent float64            `json:"dropout_percent"`
	P50Ms          float64            `json:"p50_ms"`
	P95Ms          float64            `json:"p95_ms"`
	P99Ms          float64            `json:"p99_ms"`
	MinMs          float64            `json:"min_ms"`
	MaxMs          float64            `json:"max_ms"`
	AvgMs          float64            `json:"avg_ms"`
	BackendCounts  map[string]int     `json:"backend_distribution"`
	StatusCodeMap  map[int]int        `json:"status_codes"`
	Timestamp      string             `json:"timestamp"`
}

type BackendJSONResponse struct {
	Backend   string `json:"backend"`
	RequestID uint64 `json:"request_id"`
	Message   string `json:"message"`
}

func main() {
	targetURL := flag.String("url", "http://127.0.0.1:8080", "Target Load Balancer URL")
	numRequests := flag.Int("requests", 5000, "Total number of HTTP requests to execute")
	concurrency := flag.Int("concurrency", 40, "Number of concurrent worker goroutines")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP request timeout per request")
	experiment := flag.String("experiment", "baseline", "Experiment identifier name")
	outFile := flag.String("out", "", "Output JSON path to save experiment metrics")
	csvFile := flag.String("csv", "", "Output CSV path to append metrics row")
	delayParam := flag.String("delay", "", "Optional query delay param (e.g. 50ms, 200ms)")
	failParam := flag.Bool("fail", false, "Simulate synthetic fail query param")

	flag.Parse()

	// Build full request URL with query parameters if specified
	parsedTarget, err := url.Parse(*targetURL)
	if err != nil {
		log.Fatalf("Invalid target URL: %v", err)
	}

	q := parsedTarget.Query()
	if *delayParam != "" {
		q.Set("delay", *delayParam)
	}
	if *failParam {
		q.Set("fail", "true")
	}
	parsedTarget.RawQuery = q.Encode()
	fullURL := parsedTarget.String()

	log.Printf("=========================================================")
	log.Printf(" ⚡ STARTING LOAD GENERATOR")
	log.Printf(" 🎯 Target: %s", fullURL)
	log.Printf(" 🔢 Requests: %d | 🧵 Concurrency: %d workers", *numRequests, *concurrency)
	log.Printf(" 🧪 Experiment: %s", *experiment)
	log.Printf("=========================================================")

	// Optimized HTTP client with high-capacity connection pooling
	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:   false,
			MaxIdleConns:        2000,
			MaxIdleConnsPerHost: 500,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression: true,
		},
	}

	jobs := make(chan int, *numRequests)
	results := make(chan RequestResult, *numRequests)

	var wg sync.WaitGroup

	startTime := time.Now()

	// Launch worker pool
	for w := 1; w <= *concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for range jobs {
				reqStart := time.Now()
				req, err := http.NewRequest(http.MethodGet, fullURL, nil)
				if err != nil {
					results <- RequestResult{
						Success: false,
						Latency: time.Since(reqStart),
						Err:     err,
					}
					continue
				}
				req.Header.Set("User-Agent", fmt.Sprintf("LoadGen-Worker-%d", workerID))

				resp, err := client.Do(req)
				lat := time.Since(reqStart)

				if err != nil {
					results <- RequestResult{
						Success: false,
						Latency: lat,
						Err:     err,
					}
					continue
				}

				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				backendName := "unknown"
				var bResp BackendJSONResponse
				if err := json.Unmarshal(body, &bResp); err == nil && bResp.Backend != "" {
					backendName = bResp.Backend
				}

				success := resp.StatusCode >= 200 && resp.StatusCode < 400
				results <- RequestResult{
					Success:    success,
					Latency:    lat,
					StatusCode: resp.StatusCode,
					Backend:    backendName,
				}
			}
		}(w)
	}

	// Enqueue all jobs
	for i := 1; i <= *numRequests; i++ {
		jobs <- i
	}
	close(jobs)

	// Wait for workers to finish
	wg.Wait()
	close(results)

	totalElapsed := time.Since(startTime)

	// Process and aggregate metrics
	var successCount, failCount int
	var latencies []time.Duration
	var totalLatency time.Duration
	backendCounts := make(map[string]int)
	statusCodes := make(map[int]int)

	for res := range results {
		latencies = append(latencies, res.Latency)
		totalLatency += res.Latency
		if res.StatusCode > 0 {
			statusCodes[res.StatusCode]++
		}
		if res.Success {
			successCount++
			if res.Backend != "" {
				backendCounts[res.Backend]++
			}
		} else {
			failCount++
		}
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	totalReq := len(latencies)
	var p50, p95, p99, minMs, maxMs, avgMs float64
	if totalReq > 0 {
		minMs = float64(latencies[0].Microseconds()) / 1000.0
		maxMs = float64(latencies[totalReq-1].Microseconds()) / 1000.0
		avgMs = float64(totalLatency.Milliseconds()) / float64(totalReq)
		p50 = float64(latencies[int(float64(totalReq)*0.50)].Microseconds()) / 1000.0
		p95 = float64(latencies[int(float64(totalReq)*0.95)].Microseconds()) / 1000.0
		p99 = float64(latencies[int(float64(totalReq)*0.99)].Microseconds()) / 1000.0
	}

	throughput := float64(successCount) / totalElapsed.Seconds()
	dropoutPct := (float64(failCount) / float64(totalReq)) * 100.0

	expResult := ExperimentResult{
		Experiment:     *experiment,
		TargetURL:      fullURL,
		Requests:       *numRequests,
		Concurrency:    *concurrency,
		Successful:     successCount,
		Failed:         failCount,
		ElapsedTimeSec: totalElapsed.Seconds(),
		ThroughputRPS:  throughput,
		DropoutPercent: dropoutPct,
		P50Ms:          p50,
		P95Ms:          p95,
		P99Ms:          p99,
		MinMs:          minMs,
		MaxMs:          maxMs,
		AvgMs:          avgMs,
		BackendCounts:  backendCounts,
		StatusCodeMap:  statusCodes,
		Timestamp:      time.Now().Format(time.RFC3339),
	}

	// Print Summary Table to Terminal
	fmt.Println("\n" + strings.Repeat("=", 68))
	fmt.Printf("                   EXPERIMENT SUMMARY: %s\n", *experiment)
	fmt.Println(strings.Repeat("=", 68))
	fmt.Printf(" Total Requests     : %d\n", *numRequests)
	fmt.Printf(" Concurrency        : %d workers\n", *concurrency)
	fmt.Printf(" Elapsed Time       : %.3f s\n", totalElapsed.Seconds())
	fmt.Printf(" Successful         : %d (%.2f%%)\n", successCount, float64(successCount)/float64(*numRequests)*100.0)
	fmt.Printf(" Failed (Dropout)   : %d (%.2f%%)\n", failCount, dropoutPct)
	fmt.Printf(" Throughput (RPS)   : \033[1;32m%.2f req/sec\033[0m\n", throughput)
	fmt.Printf(" Latency p50 (Med)  : %.2f ms\n", p50)
	fmt.Printf(" Latency p95        : %.2f ms\n", p95)
	fmt.Printf(" Latency p99 (Tail) : %.2f ms\n", p99)
	fmt.Printf(" Latency Avg (Mean) : %.2f ms (Min: %.2f ms, Max: %.2f ms)\n", avgMs, minMs, maxMs)
	if len(backendCounts) > 0 {
		fmt.Printf(" Backend Distribution:\n")
		for b, count := range backendCounts {
			fmt.Printf("   • %-16s : %d requests (%.1f%%)\n", b, count, float64(count)/float64(successCount)*100.0)
		}
	}
	fmt.Println(strings.Repeat("=", 68) + "\n")

	// Save JSON Output if specified
	if *outFile != "" {
		jsonData, err := json.MarshalIndent(expResult, "", "  ")
		if err != nil {
			log.Printf("Failed to marshal JSON: %v", err)
		} else {
			if err := os.WriteFile(*outFile, jsonData, 0644); err != nil {
				log.Printf("Failed to write output JSON to %s: %v", *outFile, err)
			} else {
				log.Printf("📁 Saved JSON report: %s", *outFile)
			}
		}
	}

	// Append to CSV table if specified
	if *csvFile != "" {
		writeCSV(*csvFile, expResult)
	}
}

func writeCSV(filePath string, res ExperimentResult) {
	fileExists := false
	if _, err := os.Stat(filePath); err == nil {
		fileExists = true
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open CSV file %s: %v", filePath, err)
		return
	}
	defer f.Close()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	if !fileExists {
		header := []string{
			"Experiment", "Requests", "Concurrency", "Success", "Failed",
			"RPS", "DropoutPercent", "p50_ms", "p95_ms", "p99_ms", "avg_ms", "ElapsedSec",
		}
		writer.Write(header)
	}

	row := []string{
		res.Experiment,
		fmt.Sprintf("%d", res.Requests),
		fmt.Sprintf("%d", res.Concurrency),
		fmt.Sprintf("%d", res.Successful),
		fmt.Sprintf("%d", res.Failed),
		fmt.Sprintf("%.2f", res.ThroughputRPS),
		fmt.Sprintf("%.2f%%", res.DropoutPercent),
		fmt.Sprintf("%.2fms", res.P50Ms),
		fmt.Sprintf("%.2fms", res.P95Ms),
		fmt.Sprintf("%.2fms", res.P99Ms),
		fmt.Sprintf("%.2fms", res.AvgMs),
		fmt.Sprintf("%.3f", res.ElapsedTimeSec),
	}
	writer.Write(row)
	log.Printf("📊 Appended row to CSV table: %s", filePath)
}
