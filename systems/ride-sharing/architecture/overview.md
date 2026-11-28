# Ride sharing: architecture

## One process, one lock, no goroutines

`Service` holds all state in memory behind a single mutex and starts no goroutines. Time enters through two doors only: the `Clock` it was given, and `Tick`, which the server calls a few times a second. An offer lapsing, a silent driver being dropped, a waiting trip being given up on, and a batch being decided are all things `Tick` does when it finds that enough time has passed.

That choice is what makes the rest testable. A test advances a fake clock and calls `Tick`, and the simulation runs two hours of city life in seconds. It is also why races are decided one way: every operation that changes state takes the lock, so any two of them happen in some order, and the invariants only have to hold between operations.

## Components

```
handler.go ──► Service ──┬─► geo.Index        where the drivers are
 (HTTP, JSON)  (service,  ├─► trip.Trip.Apply  the state machine, one function for every change
                driver,   ├─► dispatch.Strategy  who should be offered what (advice only)
                engine)   │      └─► match.Greedy / match.Optimal
                          ├─► pricing.Surge, Rates  multipliers and fares
                          └─► Observer, OnOffer     metrics, and a hook to push offers
```

Lock order is Service, then the index. The index never calls back into the Service, so there is no cycle. The `Observer` and `OnOffer` callbacks run under the Service's lock, which is stated in their documentation: they must be quick and must not call back in.

## A trip's life

1. **Quote.** `POST /quotes` prices the route with the current surge multiplier of the pickup's neighbourhood and holds that price for two minutes, once, for that rider and route.
2. **Request.** `POST /trips` creates the trip. An `Idempotency-Key` makes retries return the same trip. A rider can have one active trip. The trip joins the queue of waiting trips.
3. **Match.** With greedy matching, the service runs a dispatch round immediately and again on every tick. With batched matching, it runs one when the batch window has passed since the last. A round asks the strategy to propose pairs from the waiting trips, oldest first, and the available drivers.
4. **Reserve.** For each proposed pair the service checks the driver is still available and the trip still waiting, then marks the driver reserved, applies the offer event, and starts a ten second clock. This is the only place a driver goes from available to reserved.
5. **Answer.** The driver accepts, declines, or says nothing. Accepting after the deadline fails, and the trip has already gone back to waiting.
6. **Ride.** Arrive, start, complete. A rider can cancel until the ride starts, and pays a fee once a driver has committed. A driver who backs out sends the trip back to waiting, ahead of newer ones.

## The trip state machine

`trip.Apply(event)` returns a new trip. It never modifies the one it was called on. The transition table is the code, and a test walks every pair of state and event and checks it is either allowed or refused, so a transition added by mistake is noticed.

Three rules do most of the work:

- An event that repeats the one that produced the current state changes nothing and succeeds. Retries are harmless.
- Only the driver a trip is assigned to can act on it. A late answer from a driver who was passed over is refused.
- A driver who turned a trip down, let an offer lapse, or backed out is never offered that trip again.

A trip cancelled after a driver was assigned still names that driver, which is how the service knows whom to free.

## The index

`Grid` cuts the service area into cells of about 500 m and keeps drivers in them. A search starts in the rider's cell and scans outward in square rings. After each ring it can bound how close anything unscanned could be, and stops when the best candidates found are already closer than that bound.

The bound is where the care went. Anything outside the rings scanned so far is at least `r` whole cells from the rider, `r` times the width of the narrowest cell. Cells get narrower east to west towards the poles, so the width used is that of the highest latitude in the service area. The great circle between two points at the same latitude is slightly shorter than the straight east west distance the cell is measured in, so the bound is shrunk by a thousandth. A driver in a diagonal neighbour of the rider's cell can be nearer than one in the rider's own, which is why stopping after the first ring would be wrong, and there is a test for exactly that.

A query from outside the service area needs no special case: its cell is the nearest one on the edge, and nothing lies beyond that cell on the side the query is on. An earlier version had a fallback that scanned everything, and a mutation test showed it changed no answer, so it was removed.

One `RWMutex` covers the grid. A move between cells then happens in a single step, which a sharded design cannot promise without more machinery: a query scanning shard A then shard B can see a driver twice, or not at all, if it moves between them. Splitting the grid by region is the first change to make if that lock ever shows up in a profile.

## Matching

Both strategies implement `Propose(requests, index, available, options) []Pair`, and only propose.

**Greedy** serves requests in the order they arrived, each taking the nearest driver still free. It never makes anyone wait for a decision.

**Batched** takes all the waiting requests, finds the 8 nearest available drivers for each, and builds a matrix of pickup times with forbidden entries for drivers outside a rider's list. The Hungarian algorithm then pairs riders with drivers so that as many riders as possible get one and, among those pairings, the total pickup time is smallest. Forbidden pairs cost more than every real pairing put together, which is what makes it match as many as possible first.

The solver works on the smaller side of the matrix. With 150 riders waiting and about 1,200 drivers in their lists, padding to a square meant a 1,200 by 1,200 problem and 652 ms per decision. Solving the 150 by 1,200 problem directly takes about 3.5 ms for the whole decision, searches included. The exhaustive search that checks the solver tries every pairing for matrices up to 6 by 6, for squares and for lopsided shapes in both directions.

Limiting each rider to 8 candidates keeps the problem small and costs some quality: a rider can miss a better driver who is not among their 8 nearest. Lyft's description says the same, that not every pair needs to be considered.

## Surge pricing

Each 2 km cell remembers recent demand as a decayed count, with a five minute memory. Once per tick, the number of available drivers in each cell is handed to pricing. Pressure is demand per minute divided by what the cell's drivers can serve, at 0.1 trips a minute each. At or below 1 there is no surge. Above it, the target multiplier is 1 plus half the excess, capped at 3.

The multiplier does not jump to the target. It moves toward it by at most 0.2 every 30 seconds, is rounded to 0.1, and holds still in between. Demand fades gradually, and supply can arrive suddenly, so the price comes down in steps, as it went up. Prices are whole cents, and a quote locks the price for two minutes.

The multiplier does not feed back into demand here. Riders in the simulation do not react to price, so surge changes what is quoted and has no effect on the matching experiments.

## Time, and what lapses

| What | After | Handled by |
|---|---|---|
| An offer | 10 s | the next `Tick`, or lazily when the driver tries to accept |
| A silent driver | 30 s, unless on a trip | `Tick` |
| A waiting trip | 2 min | `Tick`, which ends it as `no_drivers` |
| A quote | 2 min | checked when it is used |
| A finished trip | kept for 10 min, then forgotten | `Tick` |

## What would be needed next

Each of these is a project, and none is built.

- **Durable state.** Every trip event is already an ordered, immutable record. Writing it to a log before acting on it, and rebuilding the service from the log, would survive a crash. The payment platform in this repository does this with its outbox.
- **More than one city.** Run one service per city or region. Trips near a boundary are the hard case.
- **Roads.** Replace straight line distance divided by a constant with a routing engine's estimate. The solver does not care where the costs come from.
- **Push to drivers.** `OnOffer` is the hook, and polling can go away.
- **Repositioning idle drivers.** Surge says where drivers are needed. Nothing here moves them there.
- **Pooled rides.** A driver can carry more than one rider, which changes matching from pairs to routes.
