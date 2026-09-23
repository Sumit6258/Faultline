package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkFixedWindow_Allow(b *testing.B) {
	fw := NewFixedWindow(1000000, time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fw.Allow("bench-key")
	}
}

func BenchmarkSlidingWindow_Allow(b *testing.B) {
	sw := NewSlidingWindowCounter(1000000, time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sw.Allow("bench-key")
	}
}

func BenchmarkTokenBucket_Allow(b *testing.B) {
	tb := NewTokenBucket(1000000, 1000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow("bench-key")
	}
}

func BenchmarkLeakyBucket_Allow(b *testing.B) {
	lb := NewLeakyBucket(1000000, 1000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lb.Allow("bench-key")
	}
}

func BenchmarkTokenBucket_Allow_ManyKeys(b *testing.B) {
	tb := NewTokenBucket(1000000, 1000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow("key-" + strconv.Itoa(i%10000))
	}
}
