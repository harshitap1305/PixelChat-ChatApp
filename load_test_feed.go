package main
import (
	"fmt"
	"net/http"
	"sync"
	"time"
)
func main() {
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://10.1.75.51:5269/feed?limit=50000")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	fmt.Println("Time:", time.Since(start))
}
