package main

import (
	"testing"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/sim"
)

func TestParseStrategies(t *testing.T) {
	got, err := parseStrategies("greedy, batched:5s,batched")
	if err != nil || len(got) != 3 || got[0].strategy != ridesharing.StrategyGreedy || got[1].window != 5*time.Second || got[2].window != 2*time.Second || got[1].label != "batched 5s" {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "random", "batched:soon", "batched:0s", "batched:-1s"} {
		if _, err := parseStrategies(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestParseRates(t *testing.T) {
	if got, err := parseRates("10, 25.5"); err != nil || len(got) != 2 || got[1] != 25.5 {
		t.Errorf("%v %v", got, err)
	}
	for _, bad := range []string{"", "x", "0", "-5"} {
		if _, err := parseRates(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestPoolingJoinsSamplesBeforeSummarising(t *testing.T) {
	a := sim.Result{Requested: 2, TimeToPickupSamples: []float64{10, 20}, Utilization: 0.4, DeadheadMeters: 100}
	b := sim.Result{Requested: 3, TimeToPickupSamples: []float64{30, 40, 50}, Utilization: 0.6, DeadheadMeters: 300}
	p := pooled([]sim.Result{a, b})
	if p.Requested != 5 || p.TimeToPickup.N != 5 || p.TimeToPickup.Mean != 30 || p.Utilization != 0.5 || p.DeadheadMeters != 200 {
		t.Errorf("%+v", p)
	}
	if z := pooled(nil); z.Requested != 0 {
		t.Errorf("%+v", z)
	}
}
