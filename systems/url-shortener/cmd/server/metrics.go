package main

import (
	"net/http"

	urlshortener "github.com/Sumit6258/Faultline/systems/url-shortener"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/obs"
)

// observe adds a /metrics endpoint and per request metrics around app. The
// numbers specific to this system are read from the service's own counters
// whenever Prometheus scrapes, so the service code is untouched.
//
// Rate limited creations are visible without any extra code: they are the
// requests to /shorten that ended in a 429.
func observe(app http.Handler, svc *urlshortener.Service) http.Handler {
	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "url-shortener")
	reg.NewCounterFunc("urlshortener_cache_hits_total", "Resolves answered from the cache.",
		func() float64 { return float64(svc.Stats().CacheHits) })
	reg.NewCounterFunc("urlshortener_cache_misses_total", "Resolves that had to read the store.",
		func() float64 { return float64(svc.Stats().CacheMisses) })
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
