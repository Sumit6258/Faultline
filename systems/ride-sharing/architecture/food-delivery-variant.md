# Food delivery as a variant of ride sharing

The repository's architecture notes say food delivery is documented here instead of built separately, because the geo-matching core is the same. This page says what carries over, what changes, and what would be hard. It is design reasoning from the pieces in this directory. None of it is built, and it describes no company's system.

## What carries over unchanged

- **The driver index.** Couriers are points that move and are searched by nearness, exactly as drivers are. `internal/geo` needs no change.
- **Reservation under a lock.** A courier must not be offered two orders at once, and an order must not have two couriers. The service's rule that a strategy only advises, and that reserving happens under the lock, applies as it is.
- **Offers that lapse, declines, and silent couriers.** The same timers, and the same rule that a courier who declined is not asked again.
- **Idempotent requests and quotes that lock the price.** A customer's phone retries just as a rider's does.
- **The simulation.** Compare greedy with batched on the same orders and couriers. That is the way to find out whether a change helps.

## What changes

**Three parties instead of two.** A ride has a rider and a driver. An order has a customer, a restaurant and a courier, and the restaurant is a participant with its own timing: it accepts or refuses the order, and takes a variable time to prepare it. The state machine grows:

| Ride sharing | Food delivery |
|---|---|
| requested | placed, then accepted by the restaurant (or refused, which ends it) |
| offered, accepted | courier offered, courier accepted |
| arrived | courier at the restaurant |
| (nothing) | preparing, then ready |
| in_progress | picked up |
| completed | delivered |

Everything else about `trip.Apply` stays: one function for every change, a table of what may follow what, repeated events are no-ops, and only the assigned courier can act.

**The cost of a pairing is different.** A rider cares about how long until the car arrives. A customer cares when the food arrives, and the food is not ready until the restaurant has finished. A courier who reaches the restaurant early waits there, unpaid and unavailable. A courier who arrives late leaves food getting cold. So the cost of pairing a courier with an order is closer to how far the courier's arrival at the restaurant is from the moment the food is ready, in either direction, plus the delivery leg. `internal/match` does not change. It minimises whatever cost matrix it is given, and only the matrix changes.

**Dispatch timing becomes a decision.** In ride sharing the best time to match is now. In delivery the best time is when a courier can arrive about as the food is ready, which depends on preparation time the restaurant estimates and often gets wrong. Dispatching too early wastes courier time. Dispatching too late makes the customer wait. This needs a "not before" time on each order, and an estimate of preparation time that learns from how restaurants actually do.

**Batching is more natural, and better.** The simulation in this directory found that batching costs a little waiting at ordinary load and pays when couriers are scarce. In delivery the customer's wait is already measured in tens of minutes, and dispatch is deliberately held back until the food is nearly ready. A longer window then costs almost nothing in felt waiting, and it gives the solver more orders to arrange, which is where the saving was in ride sharing. I expect a longer window to be right for delivery, but this is a prediction from the ride sharing result and the simulation would have to be rebuilt for orders to test it.

**A courier can carry more than one order.** Stacking two orders from the same restaurant, or from two nearby ones, into one trip is how delivery uses couriers efficiently, and it breaks the assumption that a driver is on one trip at a time. Matching stops being a pairing of orders to couriers and becomes assigning orders to routes, with the order of stops mattering, which makes it a harder problem than the assignment solver here. Ride sharing has the same difficulty in its pooled form.

**Prices have more parts.** A delivery fee, the food, tips, and a service charge replace one fare. Surge applies to the delivery fee and is driven by the supply of couriers near restaurants, not near customers. Money stays in whole cents, and a quote that locks the price matters more because the order is paid for before it is delivered.

## What would be hard

- Estimating preparation time well enough that dispatch timing is worth doing at all.
- Routes for stacked orders, which is a vehicle routing problem and not an assignment.
- Keeping three parties' views consistent: a restaurant that has started cooking an order the customer has just cancelled is a real case, and the state machine needs a rule for who pays.

## What to build first

If this were built, the order would be: the order state machine with the restaurant as a participant, then the cost matrix with a ready time, then the simulation extended with restaurants and preparation times so the dispatch timing question can be answered with a measurement. Stacking comes last, because it replaces the solver and is a project on its own.
