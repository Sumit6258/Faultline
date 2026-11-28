package sim

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
)

func short(p Params) Params {
	if p.Drivers == 0 {
		p.Drivers = 60
	}
	if p.RidersPerMinute == 0 {
		p.RidersPerMinute = 6
	}
	p.Duration, p.Warmup = 25*time.Minute, 5*time.Minute
	return p
}

func run(t *testing.T, p Params) Result {
	t.Helper()
	r, err := Run(short(p))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// strip removes what legitimately differs between two identical runs.
func strip(r Result) Result { r.Wall = 0; return r }

func TestTwoRunsWithTheSameParametersAreIdentical(t *testing.T) {
	for _, strategy := range []ridesharing.StrategyKind{ridesharing.StrategyGreedy, ridesharing.StrategyBatched} {
		a, b := run(t, Params{Seed: 5, Strategy: strategy}), run(t, Params{Seed: 5, Strategy: strategy})
		if !reflect.DeepEqual(strip(a), strip(b)) {
			t.Errorf("%s: two runs of the same simulation differ:\n%+v\n%+v", strategy, strip(a).MatchWait, strip(b).MatchWait)
		}
	}
	if a, b := run(t, Params{Seed: 5}), run(t, Params{Seed: 6}); reflect.DeepEqual(strip(a), strip(b)) {
		t.Error("different seeds should give different cities")
	}
}

func TestTheGridAndBruteForceIndexesGiveIdenticalResults(t *testing.T) {
	for _, strategy := range []ridesharing.StrategyKind{ridesharing.StrategyGreedy, ridesharing.StrategyBatched} {
		g := run(t, Params{Seed: 3, Strategy: strategy, Index: ridesharing.IndexGrid})
		b := run(t, Params{Seed: 3, Strategy: strategy, Index: ridesharing.IndexBrute})
		if !reflect.DeepEqual(strip(g), strip(b)) {
			// the Params differ in the index, so compare what was measured
			if !reflect.DeepEqual(g.TimeToPickupSamples, b.TimeToPickupSamples) || g.Completed != b.Completed || g.NoDrivers != b.NoDrivers {
				t.Errorf("%s: the index changed the outcome: grid completed %d, brute completed %d", strategy, g.Completed, b.Completed)
			}
		}
	}
}

func TestEveryCountedTripEndsSomewhereAndNothingIsLeftHanging(t *testing.T) {
	for _, rate := range []float64{3, 12, 40} {
		r := run(t, Params{Seed: 2, RidersPerMinute: rate, Drivers: 40})
		if r.Requested == 0 {
			t.Fatalf("rate %v: nothing was requested", rate)
		}
		if r.Completed+r.NoDrivers+r.Unfinished != r.Requested {
			t.Errorf("rate %v: %d completed + %d no drivers + %d unfinished != %d requested", rate, r.Completed, r.NoDrivers, r.Unfinished, r.Requested)
		}
		if r.Unfinished != 0 {
			t.Errorf("rate %v: %d trips were still going after the drain period", rate, r.Unfinished)
		}
		if r.Utilization < 0 || r.Utilization > 1 {
			t.Errorf("rate %v: utilization %.2f", rate, r.Utilization)
		}
		for _, v := range r.TimeToPickupSamples {
			if v < 0 || math.IsNaN(v) {
				t.Fatalf("rate %v: a rider waited %v seconds", rate, v)
			}
		}
	}
}

func TestStrategiesSeeTheSameRiders(t *testing.T) {
	g := run(t, Params{Seed: 4, Strategy: ridesharing.StrategyGreedy})
	b := run(t, Params{Seed: 4, Strategy: ridesharing.StrategyBatched})
	if g.Requested != b.Requested || g.Requested == 0 {
		t.Errorf("greedy faced %d riders and batched %d: they must face the same ones to be comparable", g.Requested, b.Requested)
	}
}

func TestAWarmUpIsNotCounted(t *testing.T) {
	a := run(t, Params{Seed: 1})
	p := short(Params{Seed: 1})
	p.Warmup = p.Duration - 5*time.Minute
	b, err := Run(p)
	if err != nil {
		t.Fatal(err)
	}
	if !(b.Requested < a.Requested) {
		t.Errorf("a longer warm up should leave fewer trips counted: %d vs %d", b.Requested, a.Requested)
	}
}

func TestMoreDemandMeansLongerWaitsAndBusierDrivers(t *testing.T) {
	light := run(t, Params{Seed: 7, RidersPerMinute: 4, Drivers: 60})
	heavy := run(t, Params{Seed: 7, RidersPerMinute: 20, Drivers: 60})
	if !(heavy.Utilization > light.Utilization) {
		t.Errorf("utilization %.2f at 20 a minute vs %.2f at 4", heavy.Utilization, light.Utilization)
	}
	if !(heavy.TimeToPickup.Mean >= light.TimeToPickup.Mean) {
		t.Errorf("riders should wait at least as long when it is busy: %.0f vs %.0f s", heavy.TimeToPickup.Mean, light.TimeToPickup.Mean)
	}
}

func TestADriverWhoNeverAnswersStillLetsTripsThroughViaTheNextDriver(t *testing.T) {
	r := run(t, Params{Seed: 9, IgnoreProb: 0.4, AcceptProb: 0.9, Drivers: 60, RidersPerMinute: 5})
	if r.OffersPerTrip < 1.3 {
		t.Errorf("with 40%% of offers ignored there should be well over one offer per trip, got %.2f", r.OffersPerTrip)
	}
	if r.Completed == 0 {
		t.Error("trips should still get through")
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{1, 2, 3, 4, 100})
	if s.N != 5 || s.Mean != 22 || s.P50 != 3 || math.Abs(s.P95-80.8) > 1e-9 {
		t.Errorf("%+v", s)
	}
	if z := Summarize(nil); z.N != 0 || z.Mean != 0 {
		t.Errorf("%+v", z)
	}
}

func TestParamsAreCheckedForNonsense(t *testing.T) {
	_, err := Run(Params{Strategy: "random"})
	if err == nil {
		t.Error("an unknown strategy should be an error")
	}
	_ = fmt.Sprint
}
