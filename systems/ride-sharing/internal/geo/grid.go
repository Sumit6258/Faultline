package geo

import (
	"sync"
	"sync/atomic"
)

type cellKey struct{ x, y int32 }

type entry struct {
	key cellKey
	p   Point
}

// Grid keeps points in cells and searches outward from the query's cell in
// rings, stopping as soon as nothing farther out could be closer than what it
// already has. A query touches the cells near the rider, however many drivers
// the city has.
//
// One lock covers the whole grid. A move between cells then happens as one step,
// so a query never sees a driver twice or not at all. The cost is that writers
// and readers wait for each other, which is cheap at this scale and is the first
// thing to change by splitting the grid into regions if it ever is not.
type Grid struct {
	cg               CellGrid
	mu               sync.RWMutex
	cells            map[cellKey]map[string]Point
	loc              map[string]entry
	queries, scanned atomic.Uint64
}

func NewGrid(b Bounds, cellMeters float64) (*Grid, error) {
	cg, err := NewCellGrid(b, cellMeters)
	if err != nil {
		return nil, err
	}
	return &Grid{cg: cg, cells: map[cellKey]map[string]Point{}, loc: map[string]entry{}}, nil
}

func (g *Grid) CellGrid() CellGrid { return g.cg }

func (g *Grid) Upsert(id string, p Point) error {
	if !p.Valid() {
		return ErrInvalidPoint
	}
	if !g.cg.Bounds.Contains(p) {
		return ErrOutOfBounds
	}
	x, y := g.cg.Cell(p)
	key := cellKey{int32(x), int32(y)}
	g.mu.Lock()
	defer g.mu.Unlock()
	old, had := g.loc[id]
	if had && old.key != key {
		g.removeFromCell(old.key, id)
	}
	m := g.cells[key]
	if m == nil {
		m = map[string]Point{}
		g.cells[key] = m
	}
	m[id] = p
	g.loc[id] = entry{key, p}
	return nil
}

func (g *Grid) removeFromCell(key cellKey, id string) {
	if m := g.cells[key]; m != nil {
		delete(m, id)
		if len(m) == 0 {
			delete(g.cells, key)
		}
	}
}

func (g *Grid) Remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.loc[id]; ok {
		g.removeFromCell(e.key, id)
		delete(g.loc, id)
	}
}

func (g *Grid) Get(id string) (Point, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e, ok := g.loc[id]
	return e.p, ok
}

func (g *Grid) Len() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.loc)
}

func (g *Grid) Stats() Stats { return Stats{g.queries.Load(), g.scanned.Load()} }

func (g *Grid) Nearest(q Point, k int, maxMeters float64, accept func(string) bool) []Result {
	g.queries.Add(1)
	if k <= 0 {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()

	var cand []Result
	scanned := 0
	visit := func(m map[string]Point) {
		for id, p := range m {
			scanned++
			d := Distance(q, p)
			if maxMeters > 0 && d > maxMeters {
				continue
			}
			if accept != nil && !accept(id) {
				continue
			}
			cand = append(cand, Result{id, p, d})
		}
	}

	// A query from outside the service area needs no special case. Its cell is the nearest cell on the
	// edge, nothing lies beyond that cell on the side the query is on, and so every unscanned point is
	// still at least r cells farther from the query than the ring that was just searched.
	cx, cy := g.cg.Cell(q)
	maxRing := g.cg.NX
	if g.cg.NY > maxRing {
		maxRing = g.cg.NY
	}
	for r := 0; r <= maxRing; r++ {
		g.ring(cx, cy, r, visit)
		// Everything not yet scanned is at least r whole cells away. The small factor covers the
		// great circle between two points being a little shorter than the straight east west
		// distance the cell width is measured in.
		bound := float64(r) * g.cg.MinCellMeters * 0.999
		if maxMeters > 0 && bound > maxMeters {
			break // nothing farther out can be within the radius
		}
		if len(cand) >= k {
			sortResults(cand)
			if cand[k-1].Meters <= bound {
				break // nothing farther out can beat the k-th best
			}
		}
	}
	g.scanned.Add(uint64(scanned))
	sortResults(cand)
	if len(cand) > k {
		cand = cand[:k]
	}
	return cand
}

// ring visits the cells at distance exactly r from (cx, cy), measured in whole cells in either direction.
func (g *Grid) ring(cx, cy, r int, visit func(map[string]Point)) {
	at := func(x, y int) {
		if x < 0 || y < 0 || x >= g.cg.NX || y >= g.cg.NY {
			return
		}
		if m := g.cells[cellKey{int32(x), int32(y)}]; m != nil {
			visit(m)
		}
	}
	if r == 0 {
		at(cx, cy)
		return
	}
	for x := cx - r; x <= cx+r; x++ {
		at(x, cy-r)
		at(x, cy+r)
	}
	for y := cy - r + 1; y <= cy+r-1; y++ {
		at(cx-r, y)
		at(cx+r, y)
	}
}
