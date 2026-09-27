package urlshortener

import (
	"strconv"
	"testing"

	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/idgen"
	"github.com/Sumit6258/Faultline/systems/url-shortener/internal/store"
)

func BenchmarkService_Shorten(b *testing.B) {
	svc := New(&idgen.Counter{}, store.NewMemory(), 10000, 1e9, 1e9, 10000)
	defer svc.Stop()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.Shorten("bench-client", "https://example.com/page")
	}
}

func BenchmarkService_Resolve_CacheHit(b *testing.B) {
	svc := New(&idgen.Counter{}, store.NewMemory(), 10000, 1e9, 1e9, 10000)
	defer svc.Stop()
	code, _ := svc.Shorten("bench-client", "https://example.com/page")
	svc.Resolve(code) // warm the cache
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.Resolve(code)
	}
}

func BenchmarkService_Resolve_CacheMiss(b *testing.B) {
	svc := New(&idgen.Counter{}, store.NewMemory(), 1, 1e9, 1e9, 10000) // cache size 1, everything evicts immediately
	defer svc.Stop()
	codes := make([]string, b.N)
	for i := range codes {
		codes[i], _ = svc.Shorten("bench-client", "https://example.com/"+strconv.Itoa(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.Resolve(codes[i]) // a fresh code each time, guaranteed miss, plus the cache is too small to help anyway
	}
}
