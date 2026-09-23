package main

import (
	"flag"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080", "target URL")
	clients := flag.Int("clients", 20, "concurrent goroutines")
	requestsPerClient := flag.Int("n", 50, "requests per goroutine")
	idPrefix := flag.String("client-id-prefix", "client", "prefix for the X-Client-ID header")
	sharedID := flag.Bool("shared-client", false, "if true, every goroutine uses the same X-Client-ID, simulating one client hitting the service from many concurrent connections")
	flag.Parse()

	var allowed, rejected, errored int64
	var wg sync.WaitGroup

	start := time.Now()
	for c := 0; c < *clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := &http.Client{Timeout: 2 * time.Second}
			clientID := fmt.Sprintf("%s-%d", *idPrefix, id)
			if *sharedID {
				clientID = *idPrefix
			}
			for i := 0; i < *requestsPerClient; i++ {
				req, _ := http.NewRequest("GET", *url, nil)
				req.Header.Set("X-Client-ID", clientID)
				resp, err := client.Do(req)
				if err != nil {
					atomic.AddInt64(&errored, 1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					atomic.AddInt64(&allowed, 1)
				} else {
					atomic.AddInt64(&rejected, 1)
				}
			}
		}(c)
	}
	wg.Wait()
	elapsed := time.Since(start)

	total := allowed + rejected + errored
	fmt.Printf("clients=%d requests_per_client=%d total_requests=%d shared_client=%v\n", *clients, *requestsPerClient, total, *sharedID)
	fmt.Printf("allowed=%d rejected=%d errored=%d\n", allowed, rejected, errored)
	fmt.Printf("elapsed=%s throughput=%.0f req/s\n", elapsed, float64(total)/elapsed.Seconds())
}
