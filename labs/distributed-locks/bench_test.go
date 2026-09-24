package distlock

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkService_TryAcquire_NewResourceEachTime(b *testing.B) {
	s := NewService()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.TryAcquire("resource-"+strconv.Itoa(i), "worker", time.Minute)
	}
}

func BenchmarkFencedResource_Write(b *testing.B) {
	r := NewFencedResource()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Write(int64(i+1), "value")
	}
}
