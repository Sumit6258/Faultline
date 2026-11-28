// Command sim compares matching strategies on the same simulated city.
//
//	go run ./tools/sim -drivers 200 -rates 20,40,60 -strategies greedy,batched:2s,batched:5s -seeds 3
//
// Every strategy at a given rate sees the same riders and the same driver choices, so differences between rows
// are down to the strategy. Several seeds are pooled, because one city can flatter either side.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/sim"
)

type spec struct {
	label    string
	strategy ridesharing.StrategyKind
	window   time.Duration
}

func parseStrategies(s string) ([]spec, error) {
	var out []spec
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "greedy":
			out = append(out, spec{"greedy", ridesharing.StrategyGreedy, 0})
		case part == "batched" || strings.HasPrefix(part, "batched:"):
			w := 2 * time.Second
			if i := strings.Index(part, ":"); i >= 0 {
				d, err := time.ParseDuration(part[i+1:])
				if err != nil || d <= 0 {
					return nil, fmt.Errorf("bad batch window in %q", part)
				}
				w = d
			}
			out = append(out, spec{fmt.Sprintf("batched %s", w), ridesharing.StrategyBatched, w})
		default:
			return nil, fmt.Errorf("unknown strategy %q, want greedy or batched:2s", part)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no strategies given")
	}
	return out, nil
}

func parseRates(s string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("bad rate %q", part)
		}
		out = append(out, v)
	}
	return out, nil
}

// pooled merges the runs of one configuration across seeds into a single result.
func pooled(runs []sim.Result) sim.Result {
	var r sim.Result
	if len(runs) == 0 {
		return r
	}
	r.Params = runs[0].Params
	var offers, deadhead, util float64
	for _, x := range runs {
		r.Requested += x.Requested
		r.Completed += x.Completed
		r.NoDrivers += x.NoDrivers
		r.Unfinished += x.Unfinished
		r.MatchWaitSamples = append(r.MatchWaitSamples, x.MatchWaitSamples...)
		r.PickupSamples = append(r.PickupSamples, x.PickupSamples...)
		r.TimeToPickupSamples = append(r.TimeToPickupSamples, x.TimeToPickupSamples...)
		offers += x.OffersPerTrip
		deadhead += x.DeadheadMeters
		util += x.Utilization
		r.Wall += x.Wall
	}
	n := float64(len(runs))
	r.OffersPerTrip, r.DeadheadMeters, r.Utilization = offers/n, deadhead/n, util/n
	r.MatchWait, r.PickupETA, r.TimeToPickup = sim.Summarize(r.MatchWaitSamples), sim.Summarize(r.PickupSamples), sim.Summarize(r.TimeToPickupSamples)
	return r
}

func main() {
	drivers := flag.Int("drivers", 200, "drivers in the city")
	rates := flag.String("rates", "20,40,60", "riders per minute, as a comma separated list")
	strategies := flag.String("strategies", "greedy,batched:2s,batched:5s", "greedy, or batched with its window")
	seeds := flag.Int("seeds", 3, "how many random cities to pool")
	duration := flag.Duration("duration", 2*time.Hour, "simulated time during which riders arrive")
	warmup := flag.Duration("warmup", 10*time.Minute, "simulated time before counting starts")
	index := flag.String("index", "grid", "driver index: grid or brute")
	parallel := flag.Int("parallel", runtime.NumCPU(), "how many simulations to run at once")
	perSeed := flag.Bool("per-seed", false, "also show the mean time to pickup in each city separately, to see whether a difference holds in all of them")
	flag.Parse()

	specs, err := parseStrategies(*strategies)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	rs, err := parseRates(*rates)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	type job struct {
		rate float64
		spec int
		seed int
	}
	var jobs []job
	for _, r := range rs {
		for i := range specs {
			for s := 1; s <= *seeds; s++ {
				jobs = append(jobs, job{r, i, s})
			}
		}
	}
	results := make([]sim.Result, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, *parallel)
	var failed error
	var mu sync.Mutex
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := sim.Run(sim.Params{Seed: int64(j.seed), Drivers: *drivers, RidersPerMinute: j.rate, Duration: *duration, Warmup: *warmup,
				Strategy: specs[j.spec].strategy, BatchWindow: specs[j.spec].window, Index: ridesharing.IndexKind(*index)})
			if err != nil {
				mu.Lock()
				failed = err
				mu.Unlock()
				return
			}
			results[i] = r
		}(i, j)
	}
	wg.Wait()
	if failed != nil {
		fmt.Fprintln(os.Stderr, failed)
		os.Exit(1)
	}

	fmt.Printf("%d drivers, %s of riders after a %s warm up, %d cities pooled per row. Same riders and same driver choices for every strategy at a given rate.\n\n", *drivers, *duration, *warmup, *seeds)
	fmt.Println("| Riders/min | Strategy | Trips | No driver | Match wait p50 / p95 | Pickup ETA mean | Time to pickup mean / p95 | vs greedy | Deadhead per trip | Driver busy | Offers per trip |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|")
	for _, rate := range rs {
		var base float64
		for si, sp := range specs {
			var runs []sim.Result
			for i, j := range jobs {
				if j.rate == rate && j.spec == si {
					runs = append(runs, results[i])
				}
			}
			r := pooled(runs)
			if si == 0 {
				base = r.TimeToPickup.Mean
			}
			vs := "n/a"
			if si == 0 {
				vs = "baseline"
			} else if base > 0 {
				vs = fmt.Sprintf("%+.1f%%", (r.TimeToPickup.Mean-base)/base*100)
			}
			fmt.Printf("| %.0f | %s | %d | %.1f%% | %.1f s / %.1f s | %.0f s | %.0f s / %.0f s | %s | %.0f m | %.0f%% | %.2f |\n",
				rate, sp.label, r.Requested, float64(r.NoDrivers)/float64(max(r.Requested, 1))*100,
				r.MatchWait.P50, r.MatchWait.P95, r.PickupETA.Mean, r.TimeToPickup.Mean, r.TimeToPickup.P95, vs, r.DeadheadMeters, r.Utilization*100, r.OffersPerTrip)
		}
	}
	if *perSeed {
		fmt.Println()
		fmt.Println("Mean time to pickup in each city, in seconds, and the change from greedy in the same city:")
		fmt.Println()
		header := "| Riders/min | Strategy |"
		rule := "|---|---|"
		for sd := 1; sd <= *seeds; sd++ {
			header += fmt.Sprintf(" City %d |", sd)
			rule += "---|"
		}
		fmt.Println(header)
		fmt.Println(rule)
		for _, rate := range rs {
			base := map[int]float64{}
			for si, sp := range specs {
				row := fmt.Sprintf("| %.0f | %s |", rate, sp.label)
				for sd := 1; sd <= *seeds; sd++ {
					for i, j := range jobs {
						if j.rate == rate && j.spec == si && j.seed == sd {
							m := results[i].TimeToPickup.Mean
							if si == 0 {
								base[sd] = m
								row += fmt.Sprintf(" %.0f |", m)
							} else {
								row += fmt.Sprintf(" %.0f (%+.1f%%) |", m, (m-base[sd])/base[sd]*100)
							}
						}
					}
				}
				fmt.Println(row)
			}
		}
	}
}
