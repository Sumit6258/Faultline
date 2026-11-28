package geo

import (
	"sync"
	"sync/atomic"
)

// BruteForce looks at every point on every query. It is the version that is
// obviously correct, which is what makes it the oracle for the grid, and it is
// the baseline the grid has to beat.
type BruteForce struct {
	mu               sync.RWMutex
	pts              map[string]Point
	queries, scanned atomic.Uint64
}

func NewBruteForce() *BruteForce { return &BruteForce{pts: map[string]Point{}} }

func (b *BruteForce) Upsert(id string, p Point) error {
	if !p.Valid() {
		return ErrInvalidPoint
	}
	b.mu.Lock()
	b.pts[id] = p
	b.mu.Unlock()
	return nil
}

func (b *BruteForce) Remove(id string) {
	b.mu.Lock()
	delete(b.pts, id)
	b.mu.Unlock()
}

func (b *BruteForce) Get(id string) (Point, bool) {
	b.mu.RLock()
	p, ok := b.pts[id]
	b.mu.RUnlock()
	return p, ok
}

func (b *BruteForce) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.pts)
}

func (b *BruteForce) Stats() Stats { return Stats{b.queries.Load(), b.scanned.Load()} }

func (b *BruteForce) Nearest(q Point, k int, maxMeters float64, accept func(string) bool) []Result {
	b.queries.Add(1)
	if k <= 0 {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	b.scanned.Add(uint64(len(b.pts)))
	var out []Result
	for id, p := range b.pts {
		d := Distance(q, p)
		if maxMeters > 0 && d > maxMeters {
			continue
		}
		if accept != nil && !accept(id) {
			continue
		}
		out = append(out, Result{id, p, d})
	}
	sortResults(out)
	if len(out) > k {
		out = out[:k]
	}
	return out
}
