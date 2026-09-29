package consistenthash

import (
	"strconv"
	"testing"
)

// keys is a fixed pool built once, outside every timed loop below, so a
// benchmark measures the lookup itself and not the cost of building the
// string handed to it.
func keys(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "key-" + strconv.Itoa(i)
	}
	return out
}

func BenchmarkRing_Get(b *testing.B) {
	r := NewRing(150)
	for i := 0; i < 20; i++ {
		r.AddNode("node-" + strconv.Itoa(i))
	}
	ks := keys(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Get(ks[i%len(ks)])
	}
}

func BenchmarkNaiveModN_Get(b *testing.B) {
	nodes := make([]string, 20)
	for i := range nodes {
		nodes[i] = "node-" + strconv.Itoa(i)
	}
	n := NewNaiveModN(nodes...)
	ks := keys(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n.Get(ks[i%len(ks)])
	}
}
