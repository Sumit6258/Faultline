# Circuit Breakers

## Problem

When a dependency is slow or failing, the naive response, keep calling it and waiting for each call to time out, makes things worse, not better. Every caller piles up waiting on a service that's already struggling, threads or goroutines get tied up on doomed calls, and the failure spreads to everything that depends on the caller too. A circuit breaker stops that spread: after enough failures, it stops calling the dependency at all for a while and fails fast instead, then cautiously checks whether the dependency has recovered before resuming normal traffic.

## Production context

The closed, open, half-open circuit breaker is a documented pattern, popularized by Michael Nygard's writing on stability patterns and implemented in widely used libraries like Netflix's Hystrix and Go's own gobreaker. The specific threshold and cooldown values in this lab's demo, 3 failures and 200ms, are chosen to be fast to watch run, not a recommendation for any real deployment.

## Requirements

Functional: track consecutive failures against a dependency, stop calling it once failures cross a threshold, and periodically allow a trial call to check for recovery.

Non-functional: the fast-fail path, the whole point of an open circuit, has to be cheap, cheaper than the timeout it's replacing. Exactly one trial call should run at a time while half-open, not a flood of them the moment the cooldown elapses.

## Capacity estimation

A Breaker is a handful of fields, current state, a fail counter, two timestamps, independent of how many calls flow through it. The number that actually matters for capacity planning is the cooldown itself: a service handling 1000 requests per second that opens its circuit for 200ms sheds roughly 200 requests worth of load onto whatever fallback or error path exists, however briefly, which is the trade a circuit breaker deliberately makes.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the full state diagram and what happens inside a single call.

## Data model

No external storage. State, failure count, and timestamps live in the Breaker struct itself, guarded by a mutex. A production system running many instances of a service typically wants breaker state to be per-instance, not shared, since the point is protecting each instance's own outbound calls, not coordinating across the fleet.

## API design

A Go library wrapping any function that can fail:

```go
b := circuitbreaker.NewBreaker(5, 30*time.Second)
err := b.Call(func() error {
    return callTheRealDependency()
})
if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
    // fail fast, or fall back to something else
}
```

## Implementation

`Breaker` holds the state machine. `Call` is the only operation: it checks the current state, decides whether to run the wrapped function at all, runs it if so, and updates state based on the result. The trickiest part isn't the state transitions themselves, it's making sure only one trial call runs during half-open even under concurrent callers, handled by the halfOpenInFlight flag and tested directly in breaker_test.go. Run the full cycle:

```bash
go run ./cmd/demo
```

## Scaling

A circuit breaker's cost is nearly free at any request volume, see Benchmark below, so it scales fine as traffic grows. The harder scaling question is choosing the threshold and cooldown for a specific dependency: too sensitive, and normal blips trip the circuit constantly, denying traffic to a dependency that was actually fine. Too lax, and the breaker doesn't protect anything before real damage is done. That tuning is dependency specific and usually comes from watching real failure patterns, not a formula.

## Failure modes

This lab's failure mode is really a demonstration of the fix, not a bug being shown off. A dependency that fails on calls 3 through 5, then recovers:

```
call 5: state=open      err=dependency unavailable
call 6: state=open      err=circuit breaker is open
call 7: state=open      err=circuit breaker is open (dependency actually called: false)
...
call 8: state=closed    err=<nil>
```

Calls 6 and 7 never reach the dependency at all, confirmed directly, not assumed. Full output, including the lazy cooldown check, is in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metric that matters most is a state transition counter, `circuit_breaker_state{state="closed|open|half_open"}` as a gauge, or a transitions counter, since a circuit that's open is actively degrading the system on purpose and should be visible on a dashboard, not just inferred from a spike in error rates elsewhere.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Path | ns/op | allocs/op |
|---|---|---|
| Call, closed, success | 34.3 | 0 |
| Call, open, fast reject | 68.0 | 0 |

Full numbers and the state cycle demo output are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Fail fast when open | Protects the caller and the dependency from pointless load during an outage | Every request during the outage gets an immediate error instead of a chance to succeed, even the ones that might have worked |
| One trial call in half-open, not a gradual ramp | Simple to implement and reason about | Recovery is all-or-nothing, one lucky or unlucky trial call decides the state for everyone, a gradual ramp is smoother but meaningfully more complex |
| Consecutive failure count, not a rolling error rate | Cheap, one integer | Doesn't distinguish "3 failures out of 3 calls" from "3 failures out of 3000," a rolling window would, at the cost of more bookkeeping |

Circuit breakers and retries solve different problems and usually sit together: retries help a single caller survive a transient blip, a circuit breaker stops the whole system from hammering a dependency that's actually down. Using only retries against a truly failing dependency just multiplies the load on it. That pairing is why Retries, Backoff, and Jitter is next on this repository's roadmap.
