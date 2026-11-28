// Package geo answers one question fast: which drivers are near this point.
//
// Without an index the answer costs a distance calculation for every driver in
// the city on every request. With a grid it costs the few cells around the
// rider. Both are here, behind one interface, so the grid can be tested
// against the obviously correct version and measured against it.
package geo

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	EarthRadiusMeters  = 6371008.8
	MetersPerDegreeLat = math.Pi / 180 * EarthRadiusMeters
)

var (
	ErrInvalidPoint = errors.New("geo: invalid coordinates")
	ErrOutOfBounds  = errors.New("geo: point is outside the service area")
)

type Point struct{ Lat, Lon float64 }

func (p Point) Valid() bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lon) && p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

// Distance is the great circle distance in meters between two points.
func Distance(a, b Point) float64 {
	const rad = math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLon := (b.Lon - a.Lon) * rad
	sLat, sLon := math.Sin(dLat/2), math.Sin(dLon/2)
	h := sLat*sLat + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*sLon*sLon
	return 2 * EarthRadiusMeters * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Bounds is the service area, a rectangle of latitude and longitude. A real
// service area is a polygon around a city. A rectangle keeps the grid simple
// and is enough to show what the index does.
type Bounds struct{ MinLat, MaxLat, MinLon, MaxLon float64 }

func (b Bounds) Contains(p Point) bool {
	return p.Lat >= b.MinLat && p.Lat <= b.MaxLat && p.Lon >= b.MinLon && p.Lon <= b.MaxLon
}

func (b Bounds) Center() Point {
	return Point{(b.MinLat + b.MaxLat) / 2, (b.MinLon + b.MaxLon) / 2}
}

func (b Bounds) Validate() error {
	switch {
	case !(Point{b.MinLat, b.MinLon}).Valid() || !(Point{b.MaxLat, b.MaxLon}).Valid():
		return fmt.Errorf("%w in bounds %+v", ErrInvalidPoint, b)
	case b.MinLat >= b.MaxLat || b.MinLon >= b.MaxLon:
		return fmt.Errorf("geo: bounds %+v are empty", b)
	case math.Abs(b.MinLat) > 85 || math.Abs(b.MaxLat) > 85:
		return fmt.Errorf("geo: bounds %+v reach the poles, where longitude cells collapse", b)
	}
	return nil
}

type Result struct {
	ID     string
	Point  Point
	Meters float64
}

// Stats counts the work an index has done. Scanned divided by Queries is how
// many drivers a query looked at, which is the number an index exists to shrink.
type Stats struct{ Queries, Scanned uint64 }

type Index interface {
	// Upsert places or moves id. It fails for coordinates that are invalid or outside the service area.
	Upsert(id string, p Point) error
	Remove(id string)
	Get(id string) (Point, bool)
	Len() int
	// Nearest returns up to k entries within maxMeters of q, nearest first, ties broken by id.
	// maxMeters of zero or less means no limit. accept, if not nil, decides which ids may be returned.
	Nearest(q Point, k int, maxMeters float64, accept func(id string) bool) []Result
	Stats() Stats
}

func sortResults(rs []Result) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Meters != rs[j].Meters {
			return rs[i].Meters < rs[j].Meters
		}
		return rs[i].ID < rs[j].ID
	})
}

// CellGrid cuts a service area into rectangular cells of about the same size in meters.
type CellGrid struct {
	Bounds           Bounds
	LatStep, LonStep float64 // degrees per cell
	NX, NY           int
	// MinCellMeters is the width of the narrowest cell. Cells get narrower east to west towards the
	// poles, and a search that stops early has to assume the narrowest, or it would stop too soon.
	MinCellMeters float64
}

func NewCellGrid(b Bounds, cellMeters float64) (CellGrid, error) {
	if err := b.Validate(); err != nil {
		return CellGrid{}, err
	}
	if cellMeters <= 0 {
		return CellGrid{}, errors.New("geo: cell size must be positive")
	}
	g := CellGrid{Bounds: b}
	g.LatStep = cellMeters / MetersPerDegreeLat
	g.LonStep = cellMeters / (MetersPerDegreeLat * math.Cos(b.Center().Lat*math.Pi/180))
	g.NX = int(math.Ceil((b.MaxLon - b.MinLon) / g.LonStep))
	g.NY = int(math.Ceil((b.MaxLat - b.MinLat) / g.LatStep))
	widest := math.Max(math.Abs(b.MinLat), math.Abs(b.MaxLat))
	eastWest := g.LonStep * MetersPerDegreeLat * math.Cos(widest*math.Pi/180)
	northSouth := g.LatStep * MetersPerDegreeLat
	g.MinCellMeters = math.Min(eastWest, northSouth)
	return g, nil
}

// Cell returns the cell holding p, clamped to the grid so a point exactly on the far edge still has one.
func (g CellGrid) Cell(p Point) (x, y int) {
	x = int((p.Lon - g.Bounds.MinLon) / g.LonStep)
	y = int((p.Lat - g.Bounds.MinLat) / g.LatStep)
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= g.NX {
		x = g.NX - 1
	}
	if y >= g.NY {
		y = g.NY - 1
	}
	return x, y
}

// Label names the cell holding p. Pricing uses it to group riders and drivers by neighbourhood.
func (g CellGrid) Label(p Point) string {
	x, y := g.Cell(p)
	return LabelOf(x, y)
}

// LabelOf names the cell at x, y. Counting drivers per cell needs a name for each occupied cell, not for each driver.
func LabelOf(x, y int) string { return fmt.Sprintf("%d:%d", x, y) }
