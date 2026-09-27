package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"

	urlshortener "github.com/Sumit6258/Faultline/systems/url-shortener"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/idgen"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/store"
)

// clientIP returns just the IP portion of r.RemoteAddr, without the
// ephemeral source port. The port changes on every new connection even
// from the same machine, so using the full RemoteAddr as a rate limiting
// key would never actually correlate repeated requests from one client,
// every request would look like a new one.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	cacheSize := flag.Int("cache-size", 1000, "LRU cache capacity")
	rateCapacity := flag.Float64("rate-capacity", 20, "creation rate limiter burst capacity per client")
	rateRefill := flag.Float64("rate-refill", 5, "creation rate limiter refill per second per client")
	flag.Parse()

	svc := urlshortener.New(&idgen.Counter{}, store.NewMemory(), *cacheSize, *rateCapacity, *rateRefill, 10000)
	defer svc.Stop()

	mux := http.NewServeMux()

	mux.HandleFunc("/shorten", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		clientID := r.Header.Get("X-Client-ID")
		if clientID == "" {
			clientID = clientIP(r)
		}

		code, err := svc.Shorten(clientID, body.URL)
		if err == urlshortener.ErrRateLimited {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"code": code})
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := svc.Stats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Path[1:]
		if code == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		longURL, ok := svc.Resolve(code)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Redirect(w, r, longURL, http.StatusFound)
	})

	log.Printf("url-shortener listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
