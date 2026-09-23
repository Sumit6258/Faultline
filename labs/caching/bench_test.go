package cache

import (
	"testing"
	"time"
)

func BenchmarkLRU_Get_Hit(b *testing.B) {
	c := NewLRU(1000, time.Hour)
	c.Set("key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("key")
	}
}

func BenchmarkLRU_Set(b *testing.B) {
	c := NewLRU(10000, time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Set("key", "value")
	}
}
