package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	ratelimit "github.com/Sumit6258/Faultline/labs/rate-limiting"
)

func main() {
	algo := flag.String("algo", "token-bucket", "fixed-window, sliding-window, token-bucket, or leaky-bucket")
	limit := flag.Int("limit", 10, "requests allowed per second, or per window for the window algorithms")
	windowMs := flag.Int("window-ms", 1000, "window size in milliseconds, used by fixed-window and sliding-window")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	var limiter ratelimit.Limiter
	switch *algo {
	case "fixed-window":
		limiter = ratelimit.NewFixedWindow(*limit, time.Duration(*windowMs)*time.Millisecond)
	case "sliding-window":
		limiter = ratelimit.NewSlidingWindowCounter(*limit, time.Duration(*windowMs)*time.Millisecond)
	case "token-bucket":
		limiter = ratelimit.NewTokenBucket(float64(*limit), float64(*limit))
	case "leaky-bucket":
		limiter = ratelimit.NewLeakyBucket(float64(*limit), float64(*limit))
	default:
		log.Fatalf("unknown algorithm %q", *algo)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Client-ID")
		if key == "" {
			key = r.RemoteAddr
		}
		if !limiter.Allow(key) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintln(w, `{"error":"rate limit exceeded"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	log.Printf("rate limiting demo server, algorithm=%s limit=%d listening on %s", *algo, *limit, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
