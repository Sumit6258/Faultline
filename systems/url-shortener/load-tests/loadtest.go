package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

type stats struct {
	CacheHits   int64
	CacheMisses int64
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "server base URL")
	numURLs := flag.Int("urls", 1000, "distinct short URLs to create")
	numRequests := flag.Int("requests", 20000, "redirect requests to send")
	flag.Parse()

	client := &http.Client{Timeout: 2 * time.Second}

	codes := make([]string, *numURLs)
	for i := 0; i < *numURLs; i++ {
		codes[i] = shorten(client, *baseURL, fmt.Sprintf("https://example.com/page/%d", i))
	}
	fmt.Printf("created %d short URLs\n", len(codes))

	before := fetchStats(client, *baseURL)

	// A Zipfian distribution, the standard library's own model for exactly
	// this: a small number of values get most of the draws, which is what
	// real hot-URL traffic looks like, a few popular links dominate total
	// clicks.
	rnd := rand.New(rand.NewSource(1))
	zipf := rand.NewZipf(rnd, 1.07, 1, uint64(*numURLs-1))

	distinct := make(map[uint64]bool)
	for i := 0; i < *numRequests; i++ {
		idx := zipf.Uint64()
		distinct[idx] = true
		resolve(client, *baseURL, codes[idx])
	}

	after := fetchStats(client, *baseURL)
	hits := after.CacheHits - before.CacheHits
	misses := after.CacheMisses - before.CacheMisses
	total := hits + misses

	fmt.Printf("%d redirect requests against %d distinct codes touched (out of %d that exist), Zipfian access pattern\n", *numRequests, len(distinct), *numURLs)
	fmt.Printf("cache hits=%d misses=%d hit ratio=%.1f%%\n", hits, misses, 100*float64(hits)/float64(total))
}

func shorten(client *http.Client, baseURL, longURL string) string {
	body, _ := json.Marshal(map[string]string{"url": longURL})
	resp, err := client.Post(baseURL+"/shorten", "application/json", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("shorten failed with status %d, raise -rate-capacity for bulk setup", resp.StatusCode))
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		panic(err)
	}
	return out.Code
}

func resolve(client *http.Client, baseURL, code string) {
	req, _ := http.NewRequest("GET", baseURL+"/"+code, nil)
	client2 := *client
	client2.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // don't actually follow the redirect, just measure the lookup
	}
	resp, err := client2.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func fetchStats(client *http.Client, baseURL string) stats {
	resp, err := client.Get(baseURL + "/stats")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var s stats
	json.NewDecoder(resp.Body).Decode(&s)
	return s
}
