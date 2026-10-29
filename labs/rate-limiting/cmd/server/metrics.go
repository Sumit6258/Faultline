package main

import (
	"net/http"

	"github.com/Sumit6258/Faultline/labs/rate-limiting/internal/obs"
)

// observe adds a /metrics endpoint and per request metrics around app. Allowed
// and rejected requests need no code of their own: they are the 200 and 429
// series of http_requests_total, and the algorithm in use is published as an
// info metric so a dashboard can tell runs apart.
func observe(app http.Handler, algorithm string) http.Handler {
	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "rate-limiting")
	reg.NewGaugeFuncVec("ratelimit_algorithm_info", "Always 1. The algorithm label is the limiter this server runs.", []string{"algorithm"},
		func() []obs.Sample { return []obs.Sample{{Labels: []string{algorithm}, Value: 1}} })
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
