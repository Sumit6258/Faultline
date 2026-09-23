package consistenthash

import (
	"strconv"
	"testing"
)

func BenchmarkRing_Get(b *testing.B) {
	r := NewRing(150)
	for i := 0; i < 20; i++ {
		r.AddNode("node-" + strconv.Itoa(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Get("key-" + strconv.Itoa(i%10000))
	}
}

func BenchmarkNaiveModN_Get(b *testing.B) {
	nodes := make([]string, 20)
	for i := range nodes {
		nodes[i] = "node-" + strconv.Itoa(i)
	}
	n := NewNaiveModN(nodes...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n.Get("key-" + strconv.Itoa(i%10000))
	}
}
