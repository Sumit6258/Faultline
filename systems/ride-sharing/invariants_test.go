package ridesharing

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/trip"
)

// checkInvariants looks at the service's internal state and fails if anything is inconsistent. It is the
// definition of "the dispatcher is correct" that the random and concurrent tests hold the service to, whatever
// order things happen in.
func checkInvariants(t *testing.T, s *Service, ctx string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	onTrip := map[string]string{}
	activeByRider := map[string]string{}
	pending := map[string]bool{}
	for _, id := range s.pending {
		pending[id] = true
	}
	for id, tr := range s.trips {
		if tr.ID != id {
			t.Errorf("%s: trip stored under %s is %s", ctx, id, tr.ID)
		}
		if tr.State.Terminal() {
			continue
		}
		if prev, dup := activeByRider[tr.Rider]; dup {
			t.Errorf("%s: rider %s has two active trips, %s and %s", ctx, tr.Rider, prev, id)
		}
		activeByRider[tr.Rider] = id
		if s.riderTrip[tr.Rider] != id {
			t.Errorf("%s: rider %s's active trip is %s but the service thinks %q", ctx, tr.Rider, id, s.riderTrip[tr.Rider])
		}
		if tr.State == trip.Requested {
			if tr.Driver != "" {
				t.Errorf("%s: waiting trip %s has driver %s", ctx, id, tr.Driver)
			}
			if !pending[id] {
				t.Errorf("%s: waiting trip %s is not in the queue and would never be matched", ctx, id)
			}
			continue
		}
		d := s.drivers[tr.Driver]
		if d == nil {
			t.Errorf("%s: trip %s is %s with unknown driver %q", ctx, id, tr.State, tr.Driver)
			continue
		}
		want := DriverBusy
		if tr.State == trip.Offered {
			want = DriverReserved
		}
		if d.state != want || d.tripID != id {
			t.Errorf("%s: trip %s is %s but its driver %s is %s on %q", ctx, id, tr.State, d.id, d.state, d.tripID)
		}
		if prev, dup := onTrip[tr.Driver]; dup {
			t.Errorf("%s: driver %s is on two trips, %s and %s", ctx, tr.Driver, prev, id)
		}
		onTrip[tr.Driver] = id
	}
	for id, d := range s.drivers {
		switch d.state {
		case DriverReserved, DriverBusy:
			tr, ok := s.trips[d.tripID]
			if !ok || tr.Driver != id || tr.State.Terminal() || tr.State == trip.Requested {
				t.Errorf("%s: driver %s is %s on trip %q, which is %+v", ctx, id, d.state, d.tripID, tr.State)
			}
		case DriverAvailable, DriverOffline:
			if d.tripID != "" {
				t.Errorf("%s: driver %s is %s but holds trip %s", ctx, id, d.state, d.tripID)
			}
		}
		_, onMap := s.idx.Get(id)
		if (d.state == DriverOffline) == onMap {
			t.Errorf("%s: driver %s is %s but on the map is %v", ctx, id, d.state, onMap)
		}
	}
	for id := range pending {
		if tr, ok := s.trips[id]; ok && tr.State.Terminal() {
			// allowed: the queue is cleaned lazily, and a finished trip in it is skipped
			continue
		}
	}
}

func sortedTripIDs(s *Service) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.trips))
	for id := range s.trips {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestRandomOperationsNeverBreakTheInvariants(t *testing.T) {
	for _, strategy := range []StrategyKind{StrategyGreedy, StrategyBatched} {
		for _, index := range []IndexKind{IndexGrid, IndexBrute} {
			t.Run(fmt.Sprintf("%s/%s", strategy, index), func(t *testing.T) {
				for seed := int64(1); seed <= 12; seed++ {
					r := rand.New(rand.NewSource(seed))
					s, clk, _ := newSvc(t, Config{Strategy: strategy, Index: index})
					drivers := make([]string, 10)
					for i := range drivers {
						drivers[i] = fmt.Sprintf("d%d", i)
					}
					riders := make([]string, 14)
					for i := range riders {
						riders[i] = fmt.Sprintf("r%d", i)
					}
					near := func() (p struct{ lat, lon float64 }) {
						p.lat, p.lon = 12.96+r.Float64()*0.03, 77.58+r.Float64()*0.03
						return
					}
					for step := 0; step < 500; step++ {
						ctx := fmt.Sprintf("seed %d step %d", seed, step)
						ids := sortedTripIDs(s)
						switch op := r.Intn(14); op {
						case 0, 1, 2:
							p := near()
							s.UpdateLocation(drivers[r.Intn(len(drivers))], pt(p.lat, p.lon))
						case 3, 4, 5:
							p := near()
							s.RequestTrip(TripRequest{Rider: riders[r.Intn(len(riders))], From: pt(p.lat, p.lon), To: dropoff, IdempotencyKey: []string{"", "", "k1", "k2"}[r.Intn(4)]})
						case 6, 7:
							d := drivers[r.Intn(len(drivers))]
							if o, ok := s.CurrentOffer(d); ok {
								if r.Intn(10) < 7 {
									s.AcceptOffer(d, o.TripID)
								} else {
									s.DeclineOffer(d, o.TripID)
								}
							}
						case 8, 9:
							if len(ids) > 0 {
								id := ids[r.Intn(len(ids))]
								v, err := s.GetTrip(id)
								if err == nil {
									d := v.Trip.Driver
									if r.Intn(8) == 0 {
										d = drivers[r.Intn(len(drivers))] // sometimes the wrong driver
									}
									switch v.Trip.State {
									case trip.Accepted:
										s.ArriveAtPickup(d, id)
									case trip.Arrived:
										s.StartTrip(d, id)
									case trip.InProgress:
										s.CompleteTrip(d, id)
									}
								}
							}
						case 10:
							if len(ids) > 0 {
								s.CancelTrip(ids[r.Intn(len(ids))], []string{"rider", "system"}[r.Intn(2)])
							}
						case 11:
							if len(ids) > 0 {
								id := ids[r.Intn(len(ids))]
								if v, err := s.GetTrip(id); err == nil {
									s.DriverCancel(v.Trip.Driver, id)
								}
							}
						case 12:
							s.GoOffline(drivers[r.Intn(len(drivers))])
						default:
							clk.Advance(time.Duration(r.Intn(15000)) * time.Millisecond)
							s.Tick()
						}
						checkInvariants(t, s, ctx)
						if t.Failed() {
							return
						}
					}
				}
			})
		}
	}
}

// Real goroutines this time, hammering the service at once. Whatever interleaving the scheduler picks, no driver
// may ever be on two trips and no trip may ever have two drivers, checked continuously while it runs.
func TestManyRidersRacingForFewDriversNeverShareADriver(t *testing.T) {
	s, _, r := newSvc(t, Config{})
	const drivers, riders = 8, 300
	for i := 0; i < drivers; i++ {
		online(t, s, fmt.Sprintf("d%d", i), pt(12.9700+float64(i)*0.0004, 77.5900))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < riders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s.RequestTrip(TripRequest{Rider: fmt.Sprintf("r%03d", i), From: center, To: dropoff})
		}(i)
	}
	stop := make(chan struct{})
	var monitor sync.WaitGroup
	monitor.Add(1)
	go func() {
		defer monitor.Done()
		for {
			select {
			case <-stop:
				return
			default:
				checkInvariants(t, s, "during the race")
			}
		}
	}()
	close(start)
	wg.Wait()
	close(stop)
	monitor.Wait()
	checkInvariants(t, s, "after the race")

	offered := 0
	byDriver := map[string]int{}
	for _, id := range sortedTripIDs(s) {
		tr := get(t, s, id)
		if tr.State == trip.Offered {
			offered++
			byDriver[tr.Driver]++
		}
	}
	if offered != drivers {
		t.Errorf("%d drivers should have been offered exactly %d trips, got %d", drivers, drivers, offered)
	}
	for d, n := range byDriver {
		if n != 1 {
			t.Errorf("driver %s was offered %d trips at once", d, n)
		}
	}
	if r.offered != drivers || r.requested != riders {
		t.Errorf("observer: offered %d, requested %d", r.offered, r.requested)
	}
}

// A longer concurrent run: riders requesting, drivers polling and answering, the clock ticking, all at once.
func TestConcurrentTrafficKeepsEveryInvariant(t *testing.T) {
	for _, strategy := range []StrategyKind{StrategyGreedy, StrategyBatched} {
		t.Run(string(strategy), func(t *testing.T) {
			s, clk, r := newSvc(t, Config{Strategy: strategy})
			const drivers, riders = 12, 120
			for i := 0; i < drivers; i++ {
				online(t, s, fmt.Sprintf("d%02d", i), pt(12.9600+float64(i)*0.002, 77.5900+float64(i%3)*0.002))
			}
			var done atomic.Bool
			var wg sync.WaitGroup
			for i := 0; i < drivers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					id := fmt.Sprintf("d%02d", i)
					rng := rand.New(rand.NewSource(int64(i)))
					for !done.Load() {
						if o, ok := s.CurrentOffer(id); ok {
							if rng.Intn(5) == 0 {
								s.DeclineOffer(id, o.TripID)
								continue
							}
							if _, err := s.AcceptOffer(id, o.TripID); err == nil {
								s.ArriveAtPickup(id, o.TripID)
								s.StartTrip(id, o.TripID)
								s.CompleteTrip(id, o.TripID)
							}
						}
						s.UpdateLocation(id, pt(12.9600+float64(i)*0.002, 77.5900+float64(i%3)*0.002))
					}
				}(i)
			}
			var reqWG sync.WaitGroup
			for i := 0; i < riders; i++ {
				reqWG.Add(1)
				go func(i int) {
					defer reqWG.Done()
					s.RequestTrip(TripRequest{Rider: fmt.Sprintf("r%03d", i), From: pt(12.9650+float64(i%10)*0.001, 77.5900), To: dropoff})
				}(i)
			}
			stop := make(chan struct{})
			var aux sync.WaitGroup
			aux.Add(2)
			go func() { // the clock and the dispatcher's tick
				defer aux.Done()
				for {
					select {
					case <-stop:
						return
					default:
						clk.Advance(500 * time.Millisecond)
						s.Tick()
						time.Sleep(50 * time.Microsecond)
					}
				}
			}()
			go func() {
				defer aux.Done()
				for {
					select {
					case <-stop:
						return
					default:
						checkInvariants(t, s, "during traffic")
					}
				}
			}()
			reqWG.Wait()
			time.Sleep(400 * time.Millisecond)
			done.Store(true)
			wg.Wait()
			close(stop)
			aux.Wait()
			checkInvariants(t, s, "after traffic")
			if r.ended[trip.Completed] == 0 {
				t.Error("nothing completed, so this exercised very little")
			}
			if r.accepted < r.ended[trip.Completed] {
				t.Errorf("accepted %d but completed %d", r.accepted, r.ended[trip.Completed])
			}
			t.Logf("%s: %d requested, %d offered, %d accepted, %d declined, %d expired, ended %v", strategy, r.requested, r.offered, r.accepted, r.declined, r.expired, r.ended)
		})
	}
}
