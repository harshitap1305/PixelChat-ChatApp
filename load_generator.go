package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var url string
	var totalRequests int
	var concurrency int
	var timeout time.Duration
	var experiment string
	var outFile string
	var csvFile string

	flag.StringVar(&url, "url", "", "Target URL (e.g., http://SYS1:8080/?delay=10ms)")
	flag.IntVar(&totalRequests, "requests", 1000, "Total number of requests")
	flag.IntVar(&concurrency, "concurrency", 10, "Number of concurrent workers")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "Request timeout")
	flag.StringVar(&experiment, "experiment", "baseline", "Experiment name")
	flag.StringVar(&outFile, "out", "", "Output JSON file (default: <experiment>.json)")
	flag.StringVar(&csvFile, "csv", "results.csv", "CSV file to append results")
	flag.Parse()

	if url == "" {
		log.Fatal("Please provide a target URL using -url")
	}

	jobs := make(chan struct{}, totalRequests)
	for i := 0; i < totalRequests; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	var successCount atomic.Int64
	var failCount atomic.Int64
	
	latencies := make(chan time.Duration, totalRequests)
	
	// Shared transport with connection pooling for all workers
	sharedTransport := &http.Transport{
		MaxIdleConns:          300,
		MaxIdleConnsPerHost:   300,
		IdleConnTimeout:       90 * time.Second,
	}

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{
				Timeout:   timeout,
				Transport: sharedTransport,
			}
			for range jobs {
				reqStart := time.Now()
				
				req, err := http.NewRequest("GET", url, nil)
				if err != nil {
					failCount.Add(1)
					continue
				}
				// Set header to trigger test mode in the python backend
				req.Header.Set("User-Agent", "Go-http-client/Load-Generator")
				
				resp, err := client.Do(req)
				if err != nil {
					failCount.Add(1)
					continue
				}
				
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if resp.StatusCode == http.StatusOK {
					successCount.Add(1)
					latencies <- time.Since(reqStart)
				} else {
					failCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	close(latencies)
	elapsed := time.Since(start)

	var lats []time.Duration
	for l := range latencies {
		lats = append(lats, l)
	}

	sort.Slice(lats, func(i, j int) bool {
		return lats[i] < lats[j]
	})

	var p50, p95, p99 float64
	if len(lats) > 0 {
		p50 = float64(lats[int(float64(len(lats))*0.50)].Milliseconds())
		p95 = float64(lats[int(float64(len(lats))*0.95)].Milliseconds())
		p99 = float64(lats[int(float64(len(lats))*0.99)].Milliseconds())
	}

	successful := successCount.Load()
	failed := failCount.Load()
	throughputRPS := float64(successful) / elapsed.Seconds()
	dropoutPercent := (float64(failed) / float64(totalRequests)) * 100

	result := map[string]interface{}{
		"experiment":      experiment,
		"requests":        totalRequests,
		"concurrency":     concurrency,
		"successful":      successful,
		"failed":          failed,
		"throughput_rps":  throughputRPS,
		"dropout_percent": dropoutPercent,
		"p50_ms":          p50,
		"p95_ms":          p95,
		"p99_ms":          p99,
	}

	// Determine JSON output filename
	jsonFile := outFile
	if jsonFile == "" {
		jsonFile = fmt.Sprintf("%s.json", experiment)
	}
	file, err := os.Create(jsonFile)
	if err != nil {
		log.Printf("Warning: could not create %s: %v", jsonFile, err)
	} else {
		json.NewEncoder(file).Encode(result)
		file.Close()
	}

	// Append to CSV
	csvExists := false
	if _, err := os.Stat(csvFile); err == nil {
		csvExists = true
	}
	f, err := os.OpenFile(csvFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Warning: could not open %s: %v", csvFile, err)
	} else {
		if !csvExists {
			f.WriteString("Experiment,Success,Failed,RPS,Dropout,p50,p95,p99\n")
		}
		f.WriteString(fmt.Sprintf("%s,%d,%d,%.1f,%.1f%%,%.1fms,%.1fms,%.1fms\n",
			experiment, successful, failed, throughputRPS, dropoutPercent, p50, p95, p99))
		f.Close()
	}

	fmt.Printf("Experiment %s finished in %v\n", experiment, elapsed)
}
