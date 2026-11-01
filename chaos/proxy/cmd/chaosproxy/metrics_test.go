package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Sumit6258/Faultline/chaos/proxy"
	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

func TestControlPortServesMetricsFromTheSameRegistryTheProxyWritesTo(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)

	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "chaos-proxy")
	p := proxy.New(u, reg)
	control := observe(p.ControlHandler(), reg)

	p.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/anything", nil)) // one proxied request
	control.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/fault", strings.NewReader(`{"mode":"error"}`)))

	rec := httptest.NewRecorder()
	control.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	out := string(b)
	for _, want := range []string{
		`chaosproxy_requests_total{fault="none",outcome="forwarded"} 1` + "\n",
		`http_requests_total{method="PUT",route="PUT /fault",status="200"} 1` + "\n",
		"go_goroutines ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
