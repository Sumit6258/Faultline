package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

// What the proxy adds to a request when no fault is active. The chaos
// experiments measure latency, so they depend on this being small next to the
// delays they inject.

func benchUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
}

func get(b *testing.B, c *http.Client, url string) {
	resp, err := c.Get(url)
	if err != nil {
		b.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func BenchmarkRequestDirect(b *testing.B) {
	up := benchUpstream()
	defer up.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		get(b, up.Client(), up.URL)
	}
}

func BenchmarkRequestThroughProxy(b *testing.B) {
	up := benchUpstream()
	defer up.Close()
	u, _ := url.Parse(up.URL)
	front := httptest.NewServer(New(u, obs.NewRegistry()))
	defer front.Close()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		get(b, front.Client(), front.URL)
	}
}
