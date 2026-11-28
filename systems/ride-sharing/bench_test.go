package ridesharing

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/clock"
	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
)

// The paths that run all the time. A driver app pings every few seconds, so location updates outnumber
// everything else by a wide margin. Requests are rarer but each does a search and a state change. Tick runs
// a few times a second whatever the load.

func benchService(b *testing.B, drivers int, cfg Config) (*Service, *clock.Fake) {
	b.Helper()
	clk := clock.NewFake(start)
	cfg.Clock = clk
	cfg.DriverTTL = 1000 * time.Hour
	cfg.Retention = time.Nanosecond // finished trips are pruned on the next tick, so a long run does not grow without bound
	s, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < drivers; i++ {
		s.UpdateLocation(fmt.Sprintf("d%05d", i), geo.Point{Lat: 12.86 + float64(i%100)*0.0024, Lon: 77.46 + float64(i/100%100)*0.0029})
	}
	return s, clk
}

func BenchmarkUpdateLocation(b *testing.B) {
	s, _ := benchService(b, 10000, Config{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.UpdateLocation(fmt.Sprintf("d%05d", i%10000), geo.Point{Lat: 12.86 + float64(i%97)*0.0025, Lon: 77.46 + float64(i%89)*0.0032})
	}
}

func BenchmarkUpdateLocationParallel(b *testing.B) {
	s, _ := benchService(b, 10000, Config{})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			s.UpdateLocation(fmt.Sprintf("d%05d", i%10000), geo.Point{Lat: 12.86 + float64(i%97)*0.0025, Lon: 77.46 + float64(i%89)*0.0032})
			i++
		}
	})
}

// A rider asks, a driver is found and offered the trip, and the rider cancels so the driver is free for the next one.
func BenchmarkRequestMatchAndCancel(b *testing.B) {
	for _, strategy := range []StrategyKind{StrategyGreedy} {
		for _, idx := range []IndexKind{IndexGrid, IndexBrute} {
			b.Run(fmt.Sprintf("%s/%s/drivers=5000", strategy, idx), func(b *testing.B) {
				s, clk := benchService(b, 5000, Config{Strategy: strategy, Index: idx})
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					tr, _, err := s.RequestTrip(TripRequest{Rider: fmt.Sprintf("r%d", i), From: geo.Point{Lat: 12.97, Lon: 77.59}, To: geo.Point{Lat: 12.99, Lon: 77.61}})
					if err != nil {
						b.Fatal(err)
					}
					s.CancelTrip(tr.ID, "rider")
					if i%1024 == 1023 {
						clk.Advance(time.Second)
						s.Tick()
					}
				}
			})
		}
	}
}

// A tick with a busy city: thousands of drivers online, a queue of trips nobody can take yet.
func BenchmarkTick(b *testing.B) {
	for _, waiting := range []int{0, 50, 200} {
		for _, strategy := range []StrategyKind{StrategyGreedy, StrategyBatched} {
			b.Run(fmt.Sprintf("%s/drivers=5000/waiting=%d", strategy, waiting), func(b *testing.B) {
				s, clk := benchService(b, 5000, Config{Strategy: strategy, BatchWindow: time.Nanosecond})
				// Trips in a corner with no driver in reach, so they stay waiting however often matching runs.
				for i := 0; i < waiting; i++ {
					if _, _, err := s.RequestTrip(TripRequest{Rider: fmt.Sprintf("w%d", i), From: geo.Point{Lat: 13.095, Lon: 77.745}, To: geo.Point{Lat: 13.09, Lon: 77.74}}); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					clk.Advance(time.Millisecond)
					s.Tick()
				}
			})
		}
	}
}

// The driver ping through the whole HTTP layer, which is what the phones actually call.
func BenchmarkHTTPDriverPing(b *testing.B) {
	s, _ := benchService(b, 1000, Config{})
	h := NewHandler(s)
	bodies := make([]string, 64)
	for i := range bodies {
		bodies[i] = fmt.Sprintf(`{"lat":%.5f,"lon":%.5f}`, 12.9+float64(i)*0.002, 77.5+float64(i)*0.003)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", fmt.Sprintf("/drivers/d%05d/location", i%1000), strings.NewReader(bodies[i%64]))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			b.Fatal(rec.Code)
		}
	}
}
