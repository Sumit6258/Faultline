# Ride sharing

A ride hailing backend: drivers report where they are, riders ask for a trip, and the service finds a driver, offers the trip, prices it, and follows it to the end. The interesting part is not the happy path. It is what happens when two riders want the same driver, when a driver's phone goes quiet, when a rider's request is retried, and when there are far more riders than drivers.

Go, in memory, no dependencies. `make test`, or `go test -race ./...` from this directory.

## Problem

A rider taps a button. Within a few seconds a nearby driver must be offered the trip, and the rider must be told who is coming. Three things make that harder than a database lookup:

- **Everything moves.** Thousands of drivers report a new position every few seconds. Finding the nearest ones to a rider cannot mean comparing against every driver in the city.
- **Everything happens at once.** Two riders can ask for the same driver in the same millisecond. A driver can accept an offer in the instant it expires. A rider's phone can resend a request it never saw a response to.
- **Nearest is not best.** Giving each rider the nearest driver in the order they ask can leave the next rider with only a distant one. When drivers are scarce this matters a lot.

## Production context

Real ride hailing platforms are much larger than this and do much more. Two public descriptions informed the design, and everything beyond them here is an educational simplification:

- Lyft has published a description of its matching. Every few seconds it gathers the unmatched requests and the available drivers into a batch and decides who goes with whom, names the Hungarian method as an example of how to solve that, and filters which rider and driver pairs are considered to keep the problem tractable. The same paper explains why not to simply give each rider the nearest driver: when few drivers are free, the nearest can be far away, long pickups leave drivers able to serve fewer riders, and that makes the shortage worse, a spiral it calls the wild goose chase ([Lyft, "A Better Match for Drivers and Riders: Reinforcement Learning at Lyft"](https://arxiv.org/pdf/2310.13810)). This system does the same on a small scale, and measures when it is worth doing.
- Uber has published H3, its hexagonal grid, and says it calculates surge pricing by measuring supply and demand in the hexagons of each city. It also explains why hexagons: a square has two kinds of neighbour, one across an edge and one across a corner, and a hexagon has one ([Uber, "H3: Uber's Hexagonal Hierarchical Spatial Index"](https://www.uber.com/us/en/blog/h3/)). This system uses a plain rectangular grid, so it meets the corner case directly, and has a test for it.

Not built: roads and routing, live traffic, shared rides, driver repositioning, payments, authentication, durable storage, and more than one city. The end of `architecture/overview.md` says what each would take, and `architecture/food-delivery-variant.md` describes how food delivery would differ, since this repository documents it as a variant of this system.

## Requirements

- Find the nearest available drivers to a point among tens of thousands, in microseconds.
- Never offer one driver two trips, and never let a trip have two drivers, however requests interleave.
- An offer that is not answered lapses, and the trip goes to the next driver. A driver who accepts after that is told no.
- A retried request must not create a second trip.
- A rider is charged the price they were quoted, even if the surge multiplier rises before they confirm.
- Surge pricing must move slowly: capped, rounded, and limited in how fast it changes.
- Report what the service is doing: how long riders wait, how many are given up on, whether drivers answer.

## Capacity estimation

Assumptions, for a busy city at peak: 20,000 drivers online, each reporting a position every 4 seconds, and 300 trip requests a minute.

| Load | Rate | Cost, measured on the sandbox this was built on | Share of one core |
|---|---|---|---|
| Driver location updates | 5,000 a second | 4.4 µs through the HTTP handler | about 2% |
| Trip requests | 5 a second | 11 µs to search, reserve and offer a driver, including the cancel that frees them | under 0.1% |
| Dispatch tick | 4 a second | 0.41 ms measured with 5,000 drivers, so about 1.6 ms with 20,000 if it scales with drivers, which it does | under 1% |
| A batched decision, 150 riders waiting | one per 2 s window | 3.5 ms | under 0.2% |
| Memory, 20,000 drivers | | 287 bytes each, 5.7 MB | |

Throughput is not what limits a single city. One process has room to spare, and the real constraints are correctness under concurrency, and matching quality when drivers run out. The sandbox is a shared single CPU virtual machine, and 21 of the 34 benchmark rows are marked noisy, so read these as orders of magnitude. `benchmarks/RESULTS.md` has the full tables.

## Architecture

```
 rider app ──► POST /quotes, /trips ─┐                         ┌─► geo index ──► nearest drivers
 driver app ─► POST /drivers/.../location ─► HTTP handler ─► Service ─┼─► trip state machine
               GET  /drivers/.../offer        (handler.go)    (one lock) ├─► dispatch strategy ──► assignment solver
               POST /drivers/.../trips/...                              └─► surge pricing
                                                    ▲
                                       Tick, a few times a second: offers lapse, drivers
                                       go quiet, trips are given up on, matching runs
```

The service has no goroutines of its own. Everything that happens because time passed happens when `Tick` is called, which is what lets tests and the simulation run it on a clock they control. `architecture/overview.md` has the flows and the reasoning.

## Data model

Three kinds of record, all in memory.

**Trip.** Rider, pickup, dropoff, price, state, assigned driver, drivers who already turned it down, and its full event history. Every change goes through one function, `Apply`, which checks the transition table below.

| State | Meaning | Can go to |
|---|---|---|
| requested | waiting for a driver | offered, cancelled, no_drivers |
| offered | a driver has been asked | accepted, requested (declined or lapsed), cancelled |
| accepted | a driver is on the way | arrived, cancelled (the rider pays a fee), requested (the driver backed out) |
| arrived | the driver is at the pickup | in_progress, cancelled (fee), requested (backed out) |
| in_progress | the rider is in the car | completed |
| completed, cancelled, no_drivers | over | nothing |

A repeated event is a no-op, so a retried request is harmless. A trip that goes back to `requested` remembers who turned it down and does not offer it to them again.

**Driver.** Available, reserved (an offer is out), busy (on a trip), or offline, with a last-seen time. A driver who stops reporting is dropped after 30 seconds, unless they are on a trip.

**Quote.** A price held for two minutes, usable once, for one rider and one route.

## API design

| Request | Does | Notable responses |
|---|---|---|
| `POST /quotes` | prices a trip and holds the price | 400 invalid, 200 |
| `POST /trips` with an `Idempotency-Key` header | asks for a trip | 201 created, 200 a replay of an earlier request, 409 rider already has a trip or the quote was used, 410 quote expired, 422 key reused for a different request |
| `GET /trips/{id}` | the trip, its history, and where its driver is | 404 |
| `POST /trips/{id}/cancel` | cancels as the rider | 409 once the ride is in progress |
| `POST /drivers/{id}/location` | the driver's heartbeat, which also brings them online | 204, 400 outside the service area |
| `GET /drivers/{id}/offer` | the offer waiting for the driver | 200, or 204 when there is none |
| `POST /drivers/{id}/trips/{trip}/accept`, `decline`, `arrive`, `start`, `complete`, `cancel` | the driver's steps | 409 if not allowed now, including accepting a lapsed offer |
| `GET /stats` | what the service is doing | |

A 409 means retrying will not help until something else changes. A 410 means a new quote is needed. Drivers poll for offers. A real service would push them to the phone, and `Config.OnOffer` is where that would hook in.

## Implementation

| Package | What it is |
|---|---|
| `internal/geo` | The driver index. A grid that searches outward from the rider in rings and stops once nothing farther can be closer, and a brute force version it is tested against |
| `internal/trip` | The trip state machine |
| `internal/pricing` | Fare estimates in whole cents, and the surge multiplier |
| `internal/match` | Two ways to pair riders with drivers: greedy, and the Hungarian algorithm |
| `internal/dispatch` | Two strategies built on those: greedy first come first served, and batched |
| `internal/sim`, `tools/sim` | The simulation that compares them, and the command that runs it |
| `service.go`, `driver.go`, `engine.go` | The service that ties these together, under one lock |
| `handler.go`, `cmd/server` | HTTP, metrics, and the tick loop |

The decisions worth knowing:

- **A strategy only advises.** Dispatch proposes who should get which trip, but the service reserves the driver itself, under the lock, and only if they are still free. A test installs a strategy that proposes the same driver twice and one that proposes a busy driver, and checks that nothing is double assigned.
- **The index gives the same answer as brute force.** The grid is compared with brute force on random cities at four latitudes and three cell sizes, with moves, removals, filters, and queries from every side of the service area. The brute force version stays in the code as the oracle and as the baseline to measure against.
- **Moving a driver is one step.** One lock covers the grid, so a driver moving between cells is never seen twice or not at all. Splitting the grid into regions is what to do if that lock ever matters.
- **The assignment solver works on the smaller side.** A first version padded the problem to a square, and the benchmarks showed a batched decision with 150 riders waiting taking 652 ms. Solving the rectangular problem brought it to about 3.5 ms.
- **Money is whole cents, and multipliers are rounded to a thousandth.** The first version printed a multiplier as `1.2000000000000002`, which the live run caught.

## What was tested, and how

Unit tests for each package, then tests that attack the whole service. After every operation in a random sequence of 500, repeated over 12 seeds, both strategies and both indexes, a checker confirms that no driver is on two trips, no trip has two drivers, every waiting trip is queued, and every driver's state matches their trip. The same checker runs continuously while real goroutines hammer the service, including 300 riders racing for 8 drivers. The code was then broken on purpose in about 80 places, one at a time, to see whether a test noticed. Every break that changes behaviour is now caught. The ones that were not at first were either real gaps, which became tests, or redundant code, which was removed.

Beyond that, the real server was run under a Python load generator (`simulation/loadgen.py`) and its metrics checked with `promtool`. It was also added to the repository's end to end observability run, with a live Prometheus, where its dashboard panels return data, its new "riders given up" alert fires for a real condition, and the other two stay quiet.

## Findings: when is batching worth it?

The simulation runs the real service on a virtual clock, with the same riders and the same driver behaviour for every strategy, so differences are down to the strategy. 200 drivers, an hour of riders, five random cities pooled per row, and 40% of requests coming from three hotspots where there are too few drivers. A driver finishes a trip about every 11 minutes, so this city saturates near 17 riders a minute.

| Riders per minute | Drivers busy | Mean time to pickup, greedy | Batched, 2 second window | No driver found, greedy | Batched |
|---|---|---|---|---|---|
| 6 | 28% | 99 s | 100 s (+1.1%) | 0% | 0% |
| 10 | 49% | 137 s | 137 s (-0.1%) | 0% | 0% |
| 13 | 67% | 173 s | 172 s (-0.7%) | 0% | 0% |
| 15 | 80% | 206 s | 202 s (-1.8%) | 0.1% | 0.2% |
| 17 | 93% | 335 s | 246 s (-26.6%) | 6.6% | 3.4% |

- **Below about 80% busy, batching buys nothing.** The differences are within a few percent and change sign from city to city. It also costs a little: the median wait for a match goes from 4.3 s to 5.3 s with a 2 second window, and to 9.4 s with a 10 second window.
- **Near saturation it matters a great deal.** At 93% busy, batching is better in all five cities, by 13% to 35% depending on the window. The 95th percentile time to pickup falls from 638 s to 524 s, riders who are never matched are halved, and drivers spend 22% less distance driving empty (1.9 km against 2.4 km a trip). That last figure fits the wild goose chase the Lyft paper describes: when drivers are scarce, a long empty drive is capacity that is not there for the next rider, so shorter ones are extra capacity. I measured the shorter drives, not the spiral itself.
- **The window barely matters.** At saturation, a 2 second window and a 10 second window are within two points of each other. Below saturation, longer windows only add waiting. A short window is the right default.

What this does not show. Travel is straight line at constant speed, with no roads. Idle drivers stay where they finish. Riders do not react to price. There are 200 drivers and five cities, so differences under about 3% should be read as noise. The result is about the shape of the trade-off, and the exact percentages belong to this simulated city. Do not read them as a measurement of any real one.

## Scaling

One process has room for a city, as the capacity table shows. Beyond that, the natural unit is the city or region: a rider in one never needs a driver in another, so each can be its own service with no coordination between them. Not built, and the hard part: trips near a boundary, where the best driver is in the next region.

Three costs grow with load and are worth knowing. The dispatch tick walks every driver, which is fine at tens of thousands and would need incremental bookkeeping at hundreds of thousands. A batched decision grows with the number of riders waiting (3.5 ms at 150), so a batch should be capped or split by neighbourhood long before it approaches the window. The single lock around the service is the first thing to split by region.

## Failure modes

| What goes wrong | What happens | Tested by |
|---|---|---|
| Two riders want the same driver | The service reserves the driver under its lock, and the second rider gets the next one | 300 riders racing for 8 drivers |
| A driver accepts after their offer lapsed | They get 409, and the trip has moved on to someone else | the accept after lapse tests |
| A driver never answers | The offer lapses after 10 seconds and the next driver is tried | offer expiry tests |
| A driver's phone goes quiet | Dropped from the map after 30 seconds. A driver with an offer out loses it. A driver on a trip stays on it | silent driver tests |
| A rider's request is retried | The same trip comes back, and nothing is double counted | idempotency tests |
| The same key is reused for a different trip | 422 | idempotency tests |
| The surge multiplier rises after a quote | The rider pays the quoted price | quote locking test |
| No driver within 5 km | The trip waits, then is given up on after 2 minutes | no driver tests |
| A driver backs out after accepting | The rider goes back to waiting, ahead of newer requests | driver cancel test |
| The process dies | All state is lost. There is no durable storage. See the roadmap in the overview | not tested, not built |

## Observability

The service exports `rideshare_match_wait_seconds`, `rideshare_trips_ended_total{outcome}`, `rideshare_offers_total{outcome}`, `rideshare_drivers{state}`, pending and active trips, the highest surge multiplier, how many drivers each nearest driver search examines, and which strategy and index are in use.

Three alerts, each with rule tests for both firing and staying quiet. `RideShareRidersGivenUp` fires when more than one rider in ten is given up on. `RideShareMatchSlow` fires when the 95th percentile wait is over 30 seconds. `RideShareOffersMostlyIgnored` fires when more than 30% of offers lapse. A "Ride sharing" section of the systems dashboard shows all of it.

## Benchmarks

Summary in this table, and everything with methodology in `benchmarks/RESULTS.md`.

| What | Brute force | Grid |
|---|---|---|
| 5 nearest drivers within 3 km, 1,000 drivers | 61 µs | 4.9 µs |
| 10,000 drivers | 817 µs | 8.4 µs |
| 100,000 drivers | 6.8 ms | 80 µs |
| A trip request that finds and offers a driver, 5,000 drivers online | 528 µs | 11 µs |

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| In memory, one process | Simple, fast, easy to reason about | A crash loses every trip in flight |
| One lock around the service | Races are decided one way, the same way every time | Writers wait for each other. The first thing to split by region |
| Rectangular grid | Simple to build and test | Corner neighbours need the ring search. Hexagons avoid it |
| Drivers poll for offers | Simple, no connection to keep open | Up to a second of added delay, and wasted requests |
| Hungarian algorithm over nearby candidates | Optimal for the candidates considered | A rider may miss a better driver outside the nearest 8. Cost grows with riders waiting |
| Batching | Large gains at saturation | A second or so of waiting at every other time |
| Surge capped, rounded and stepped | Prices riders can trust | Slower to respond to a real shortage |
| A quote locks the price | The rider pays what they saw | The service takes the risk of a rising multiplier for two minutes |

## Run it

```bash
go run ./cmd/server                                  # greedy matching, grid index, on :8080
go run ./cmd/server -strategy batched -addr :8080
python3 simulation/loadgen.py --seconds 30           # drivers and riders against the running server
go run ./tools/sim -rates 6,13,17 -strategies greedy,batched:2s -seeds 3 -per-seed
go test -race ./...
```
