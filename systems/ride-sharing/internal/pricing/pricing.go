// Package pricing turns a trip into a price, and a shortage of drivers into a
// higher one.
//
// A flat price gives no reason for more drivers to come online when demand
// spikes, and no reason for riders who can wait to wait, so a request in a
// busy neighbourhood sits unmatched while drivers elsewhere sit idle. Surge
// pricing is the lever for that. The cost of the lever is that a price which
// jumps is a price riders distrust, so everything below is built to move
// slowly: a cap, a limit on how fast it can change, rounding so it does not
// flicker, and quotes that lock a price in for a few minutes.
//
// Money is whole cents, never floating point.
package pricing

import (
	"errors"
	"math"
	"sync"
	"time"
)

type Rates struct {
	BaseCents    int64
	PerKmCents   int64
	PerMinCents  int64
	MinFareCents int64
	// SpeedMps is the assumed average speed, which turns a distance into a duration for both the
	// price and the estimated pickup time. A real service uses a routing engine and live traffic.
	SpeedMps float64
}

// DefaultRates are illustrative round numbers, not any city's tariff.
func DefaultRates() Rates {
	return Rates{BaseCents: 250, PerKmCents: 120, PerMinCents: 30, MinFareCents: 500, SpeedMps: 8.3}
}

// Minutes is how long a distance takes at the assumed speed.
func (r Rates) Minutes(meters float64) float64 { return meters / r.SpeedMps / 60 }

// Estimate prices a trip: the tariff first, held up to the minimum fare, then the surge multiplier.
func (r Rates) Estimate(meters, multiplier float64) int64 {
	km := meters / 1000
	fare := float64(r.BaseCents) + float64(r.PerKmCents)*km + float64(r.PerMinCents)*r.Minutes(meters)
	fare = math.Max(fare, float64(r.MinFareCents))
	return int64(math.Round(fare * multiplier))
}

type SurgeConfig struct {
	// Window is how long demand is remembered. Demand decays with this time constant, so a spike
	// fades instead of vanishing the moment it stops.
	Window time.Duration
	// Capacity is how many trips one available driver can serve per minute. Demand beyond what the
	// drivers in a cell can serve is what pushes the price up.
	Capacity float64
	// Slope is how much the multiplier rises for each unit of pressure above 1.
	Slope float64
	Cap   float64
	// Step is the most the multiplier can change in one update, so a price never jumps.
	Step float64
	// Interval is the shortest time between updates. Between them the price holds still.
	Interval time.Duration
	// Grain is what multipliers are rounded to, so riders see 1.4 and not 1.3718.
	Grain float64
}

func DefaultSurge() SurgeConfig {
	return SurgeConfig{Window: 5 * time.Minute, Capacity: 0.1, Slope: 0.5, Cap: 3.0, Step: 0.2, Interval: 30 * time.Second, Grain: 0.1}
}

func (c SurgeConfig) validate() error {
	switch {
	case c.Window <= 0 || c.Interval <= 0:
		return errors.New("pricing: window and interval must be positive")
	case c.Capacity <= 0 || c.Slope <= 0 || c.Step <= 0 || c.Grain <= 0:
		return errors.New("pricing: capacity, slope, step and grain must be positive")
	case c.Cap < 1:
		return errors.New("pricing: the cap cannot be below 1")
	}
	return nil
}

type cell struct {
	demand  float64 // decayed count of recent requests
	at      time.Time
	supply  int
	mult    float64
	updated time.Time
}

// Surge tracks supply and demand per neighbourhood and turns the gap into a multiplier.
type Surge struct {
	cfg   SurgeConfig
	mu    sync.Mutex
	cells map[string]*cell
}

func NewSurge(cfg SurgeConfig) (*Surge, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Surge{cfg: cfg, cells: map[string]*cell{}}, nil
}

func (s *Surge) cellLocked(name string, now time.Time) *cell {
	c := s.cells[name]
	if c == nil {
		c = &cell{mult: 1, at: now, updated: now}
		s.cells[name] = c
	}
	return c
}

func (s *Surge) decayLocked(c *cell, now time.Time) {
	if dt := now.Sub(c.at); dt > 0 {
		c.demand *= math.Exp(-dt.Seconds() / s.cfg.Window.Seconds())
		c.at = now
	}
}

// RecordRequest notes that someone asked for a ride in this cell.
func (s *Surge) RecordRequest(name string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cellLocked(name, now)
	s.decayLocked(c, now)
	c.demand++
}

// SetSupply sets how many drivers are available in this cell.
func (s *Surge) SetSupply(name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cells[name]
	if c == nil {
		if n == 0 {
			return
		}
		c = &cell{mult: 1}
		s.cells[name] = c
	}
	c.supply = n
}

// target is the multiplier the current supply and demand call for, before smoothing and before the cap,
// which is applied once, after the step, in updateLocked.
func (s *Surge) targetLocked(c *cell) float64 {
	rate := c.demand / s.cfg.Window.Minutes() // requests per minute
	supply := float64(c.supply)
	if supply < 1 {
		supply = 1
	}
	pressure := rate / (supply * s.cfg.Capacity)
	if pressure <= 1 {
		return 1
	}
	return 1 + s.cfg.Slope*(pressure-1)
}

// Multiplier is the price multiplier for a cell now. It moves toward what supply and demand call for by at
// most Step per Interval, and holds still in between.
func (s *Surge) Multiplier(name string, now time.Time) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cells[name]
	if c == nil {
		return 1
	}
	s.updateLocked(c, now)
	return c.mult
}

func (s *Surge) updateLocked(c *cell, now time.Time) {
	if c.updated.IsZero() {
		c.updated, c.at = now, now
	}
	s.decayLocked(c, now)
	steps := int(now.Sub(c.updated) / s.cfg.Interval)
	if steps <= 0 {
		return
	}
	if steps > 20 {
		steps = 20
	}
	target := s.targetLocked(c)
	for i := 0; i < steps; i++ {
		switch {
		case target > c.mult:
			c.mult = math.Min(target, c.mult+s.cfg.Step)
		case target < c.mult:
			c.mult = math.Max(target, c.mult-s.cfg.Step)
		}
	}
	// The target is never below 1, so neither is the multiplier. The cap is applied here, once.
	// Rounding to a multiple of 0.1 in floating point gives values like 1.2000000000000002, which would show up
	// in a price and in every metric. Rounding the result to a thousandth removes that noise.
	c.mult = math.Min(s.cfg.Cap, math.Round(math.Round(c.mult/s.cfg.Grain)*s.cfg.Grain*1000)/1000)
	c.updated = c.updated.Add(time.Duration(steps) * s.cfg.Interval)
}

// Max is the highest multiplier in any cell, which is what a dashboard wants to watch.
func (s *Surge) Max(now time.Time) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := 1.0
	for _, c := range s.cells {
		s.updateLocked(c, now)
		max = math.Max(max, c.mult)
	}
	return max
}
