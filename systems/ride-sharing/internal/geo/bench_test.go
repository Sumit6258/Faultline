package geo

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// The point of the grid, measured: how long it takes to find the 5 nearest
// drivers within 3 km, as the number of drivers in the city grows. Brute force
// examines every driver, so its time grows with the city. The grid examines
// the cells around the rider, so its time grows with how crowded that
// neighbourhood is.

type benchSet struct {
	brute   *BruteForce
	grid    *Grid
	queries []Point
}

var (
	benchMu    sync.Mutex
	benchCache = map[int]*benchSet{}
)

func benchData(n int) *benchSet {
	benchMu.Lock()
	defer benchMu.Unlock()
	if s, ok := benchCache[n]; ok {
		return s
	}
	r := rand.New(rand.NewSource(1))
	s := &benchSet{brute: NewBruteForce()}
	s.grid, _ = NewGrid(areas[1].b, 500)
	for i := 0; i < n; i++ {
		p := randPoint(r, areas[1].b)
		id := fmt.Sprintf("d%d", i)
		s.brute.Upsert(id, p)
		s.grid.Upsert(id, p)
	}
	for i := 0; i < 1024; i++ {
		s.queries = append(s.queries, randPoint(r, areas[1].b))
	}
	benchCache[n] = s
	return s
}

func BenchmarkNearest(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		s := benchData(n)
		for _, idx := range []struct {
			name string
			ix   Index
		}{{"brute", s.brute}, {"grid", s.grid}} {
			b.Run(fmt.Sprintf("%s/drivers=%d", idx.name, n), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					idx.ix.Nearest(s.queries[i%len(s.queries)], 5, 3000, nil)
				}
			})
		}
	}
}

// A driver app reports its position every few seconds, so updates outnumber
// queries by a wide margin.
func BenchmarkUpsertMove(b *testing.B) {
	s := benchData(10000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.grid.Upsert(fmt.Sprintf("d%d", i%10000), s.queries[i%len(s.queries)])
	}
}

func BenchmarkUpsertParallel(b *testing.B) {
	s := benchData(10000)
	var n int64
	var mu sync.Mutex
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		mu.Lock()
		n++
		id := n
		mu.Unlock()
		i := 0
		for pb.Next() {
			s.grid.Upsert(fmt.Sprintf("p%d-%d", id, i%64), s.queries[i%len(s.queries)])
			i++
		}
	})
}
