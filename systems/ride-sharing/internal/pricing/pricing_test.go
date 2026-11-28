package pricing

import (
	"math"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)

func TestEstimateAddsTheTariffAndHoldsToTheMinimum(t *testing.T) {
	r := DefaultRates()
	if got := r.Estimate(100, 1); got != 500 {
		t.Errorf("a 100 m ride is the minimum fare, got %d", got)
	}
	// 5 km at 8.3 m/s is about 10 minutes: 250 + 5*120 + 10.04*30 = 1151.
	if got := r.Estimate(5000, 1); got < 1145 || got > 1160 {
		t.Errorf("5 km costs %d cents", got)
	}
	if r.Estimate(5000, 1.5) != int64(math.Round(float64(r.Estimate(5000, 1))*1.5)) && math.Abs(float64(r.Estimate(5000, 1.5))-1.5*float64(r.Estimate(5000, 1))) > 1 {
		t.Error("the multiplier should scale the fare")
	}
	if r.Estimate(100, 2) != 1000 {
		t.Errorf("surge applies to the minimum fare too: %d", r.Estimate(100, 2))
	}
}

func TestFareNeverFallsAsDistanceOrMultiplierRise(t *testing.T) {
	r := DefaultRates()
	var prev int64
	for m := 0.0; m <= 40000; m += 250 {
		if f := r.Estimate(m, 1.3); f < prev {
			t.Fatalf("fare fell from %d to %d at %.0f m", prev, f, m)
		} else {
			prev = f
		}
	}
	prev = 0
	for mult := 1.0; mult <= 3.0; mult += 0.1 {
		if f := r.Estimate(7000, mult); f < prev {
			t.Fatalf("fare fell at multiplier %.1f", mult)
		} else {
			prev = f
		}
	}
}

func newSurge(t *testing.T) *Surge {
	s, err := NewSurge(DefaultSurge())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// demand adds n requests a minute to a cell for the given number of minutes, and returns the multiplier at the end.
func demand(s *Surge, cell string, from time.Time, perMinute, minutes int) time.Time {
	now := from
	for m := 0; m < minutes; m++ {
		for i := 0; i < perMinute; i++ {
			s.RecordRequest(cell, now.Add(time.Duration(i)*time.Minute/time.Duration(perMinute)))
		}
		now = now.Add(time.Minute)
		s.Multiplier(cell, now)
	}
	return now
}

func TestNoDemandMeansNoSurge(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 10)
	if m := s.Multiplier("a", t0.Add(time.Hour)); m != 1 {
		t.Errorf("%v", m)
	}
	if m := s.Multiplier("never-seen", t0); m != 1 {
		t.Errorf("%v", m)
	}
}

func TestPlentyOfDriversMeansNoSurgeEvenWithDemand(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 50) // can serve 5 trips a minute
	now := demand(s, "a", t0, 3, 20)
	if m := s.Multiplier("a", now); m != 1 {
		t.Errorf("3 requests a minute against 50 drivers should not surge, got %.1f", m)
	}
}

func TestSustainedShortageRaisesThePriceInStepsAndStopsAtTheCap(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 2) // can serve 0.2 trips a minute, and 6 are asked for
	cfg := DefaultSurge()
	now, prev := t0, 1.0
	var reachedCap bool
	for minute := 0; minute < 60; minute++ {
		for i := 0; i < 6; i++ {
			s.RecordRequest("a", now)
		}
		now = now.Add(time.Minute)
		m := s.Multiplier("a", now)
		if m > cfg.Cap+1e-9 {
			t.Fatalf("multiplier %.2f is above the cap", m)
		}
		if m-prev > 2*cfg.Step+1e-9 { // two Intervals pass in each minute
			t.Fatalf("multiplier jumped from %.2f to %.2f in a minute", prev, m)
		}
		if m < prev-1e-9 {
			t.Fatalf("the price fell during a shortage: %.2f to %.2f", prev, m)
		}
		prev = m
		reachedCap = reachedCap || m >= cfg.Cap-1e-9
	}
	if !reachedCap {
		t.Errorf("an hour of heavy shortage should reach the cap, ended at %.2f", prev)
	}
}

func TestThePriceComesBackDownWhenTheDemandStops(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 2)
	now := demand(s, "a", t0, 6, 30)
	peak := s.Multiplier("a", now)
	if peak < 2 {
		t.Fatalf("setup: peak %.1f", peak)
	}
	prev := peak
	for i := 0; i < 120; i++ { // two hours of quiet
		now = now.Add(time.Minute)
		m := s.Multiplier("a", now)
		if m > prev+1e-9 {
			t.Fatalf("the price rose with no demand: %.2f to %.2f", prev, m)
		}
		prev = m
	}
	if prev != 1 {
		t.Errorf("after two quiet hours the multiplier is %.2f, want 1", prev)
	}
}

func TestMultipliersAreRoundedAndBounded(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 3)
	now := t0
	for i := 0; i < 500; i++ {
		for j := 0; j < i%9; j++ {
			s.RecordRequest("a", now)
		}
		now = now.Add(20 * time.Second)
		m := s.Multiplier("a", now)
		if m < 1 || m > 3 {
			t.Fatalf("multiplier %v out of range", m)
		}
		if m != math.Round(m*10)/10 {
			t.Fatalf("multiplier %v is not exactly a multiple of 0.1, it would show up as noise in prices and metrics", m)
		}
	}
}

func TestTheMultiplierNeverMovesMoreThanOneStepPerInterval(t *testing.T) {
	cfg := DefaultSurge()
	s := newSurge(t)
	s.SetSupply("a", 2)
	now, prev := t0, 1.0
	for i := 0; i < 400; i++ {
		// wild swings in demand: a burst every other interval
		if (i/2)%2 == 0 {
			for j := 0; j < 30; j++ {
				s.RecordRequest("a", now)
			}
		}
		now = now.Add(cfg.Interval)
		m := s.Multiplier("a", now)
		if math.Abs(m-prev) > cfg.Step+cfg.Grain+1e-9 {
			t.Fatalf("interval %d: the price moved from %.2f to %.2f", i, prev, m)
		}
		prev = m
	}
}

func TestThePriceHoldsStillWithinAnInterval(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 1)
	for i := 0; i < 40; i++ {
		s.RecordRequest("a", t0)
	}
	first := s.Multiplier("a", t0.Add(31*time.Second))
	for i := 0; i < 20; i++ {
		if m := s.Multiplier("a", t0.Add(31*time.Second+time.Duration(i)*time.Second)); m != first && i < 28 {
			t.Fatalf("the price changed inside an interval: %.2f then %.2f", first, m)
		}
	}
}

func TestNeighbourhoodsAreIndependent(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("busy", 1)
	s.SetSupply("quiet", 1)
	now := demand(s, "busy", t0, 8, 30)
	if s.Multiplier("busy", now) < 2 {
		t.Error("the busy cell should surge")
	}
	if m := s.Multiplier("quiet", now); m != 1 {
		t.Errorf("the quiet cell should not, got %.1f", m)
	}
	if s.Max(now) < 2 {
		t.Error("the maximum should see the busy cell")
	}
}

func TestMoreDriversLowerThePrice(t *testing.T) {
	few, many := newSurge(t), newSurge(t)
	few.SetSupply("a", 2)
	many.SetSupply("a", 20)
	nowFew := demand(few, "a", t0, 5, 30)
	nowMany := demand(many, "a", t0, 5, 30)
	if f, m := few.Multiplier("a", nowFew), many.Multiplier("a", nowMany); !(f > m) {
		t.Errorf("with few drivers %.1f, with many %.1f", f, m)
	}
}

func TestConfigIsValidated(t *testing.T) {
	bad := []SurgeConfig{{}, func() SurgeConfig { c := DefaultSurge(); c.Cap = 0.5; return c }(), func() SurgeConfig { c := DefaultSurge(); c.Step = 0; return c }(),
		func() SurgeConfig { c := DefaultSurge(); c.Interval = 0; return c }()}
	for i, c := range bad {
		if _, err := NewSurge(c); err == nil {
			t.Errorf("config %d should be refused", i)
		}
	}
}

func TestSurgeIsSafeForConcurrentUse(t *testing.T) {
	s := newSurge(t)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := t0.Add(time.Duration(i) * time.Second)
				s.RecordRequest("a", now)
				s.SetSupply("a", w+1)
				s.Multiplier("a", now)
				s.Max(now)
			}
		}(w)
	}
	wg.Wait()
}

// A moderate shortage asks for a multiplier like 1.37, not a round number, so
// this is where rounding is actually exercised. A heavy shortage climbs in
// steps of 0.2 and is round by construction.
func TestAModerateShortageStillGivesRoundMultipliers(t *testing.T) {
	s := newSurge(t)
	s.SetSupply("a", 10) // ten drivers serve one trip a minute between them
	now := t0
	seen := map[float64]bool{}
	for minute := 0; minute < 90; minute++ {
		for i := 0; i < 1+minute%3; i++ { // one to three requests a minute, about 2: pressure about 2, a multiplier near 1.5
			s.RecordRequest("a", now)
		}
		now = now.Add(time.Minute)
		m := s.Multiplier("a", now)
		if m != math.Round(m*10)/10 {
			t.Fatalf("multiplier %v is not exactly a multiple of 0.1", m)
		}
		seen[m] = true
	}
	if len(seen) < 3 {
		t.Errorf("the moderate shortage should have produced several different prices, got %v", seen)
	}
}

// Supply can jump at once, for example when a stadium empties and a hundred
// drivers arrive. The price must come down by a step at a time, as it went up.
func TestThePriceFallsGraduallyWhenSupplyJumps(t *testing.T) {
	cfg := DefaultSurge()
	s := newSurge(t)
	s.SetSupply("a", 2)
	now := demand(s, "a", t0, 6, 40)
	peak := s.Multiplier("a", now)
	if peak < cfg.Cap-1e-9 {
		t.Fatalf("setup: peak %.1f", peak)
	}
	s.SetSupply("a", 500)
	now = now.Add(cfg.Interval)
	first := s.Multiplier("a", now)
	if first < peak-cfg.Step-cfg.Grain-1e-9 || first >= peak {
		t.Errorf("one interval after supply jumps the price should fall by one step from %.1f, got %.1f", peak, first)
	}
	prev := first
	for i := 0; i < 30; i++ {
		now = now.Add(cfg.Interval)
		m := s.Multiplier("a", now)
		if prev-m > cfg.Step+cfg.Grain+1e-9 {
			t.Fatalf("the price fell from %.1f to %.1f in one interval", prev, m)
		}
		prev = m
	}
	if prev != 1 {
		t.Errorf("it should come back to 1, got %.1f", prev)
	}
}
