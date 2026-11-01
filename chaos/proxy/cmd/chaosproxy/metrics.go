package main

import (
	"net/http"

	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

// observe adds a /metrics endpoint and request metrics around the control API.
// The proxy's own numbers, what it did to which requests and how long clients
// waited, are registered on reg by the proxy itself.
func observe(app http.Handler, reg *obs.Registry) http.Handler {
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
