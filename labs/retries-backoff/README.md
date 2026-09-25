# Retries, Backoff, and Jitter

## Problem

Retrying a failed request seems like an obviously good idea, and it is, for a single client recovering from a single transient blip. The problem shows up at scale: if a shared dependency has a brief outage, every client that was calling it fails at roughly the same moment. If they all retry using the same fixed delay, they all retry at roughly the same moment too, hitting the dependency with a second synchronized spike right as it's trying to recover. That spike can look just like the original outage, and cause another one.

## Production context

Exponential backoff with jitter is a documented pattern, described in AWS's own Architecture Blog post on the subject, which is where the specific "full jitter" formula used in this lab comes from. It's a standard part of most production HTTP client libraries and RPC frameworks. The 200 client, 200ms numbers in this lab's demo are chosen to make the effect easy to see, not a measurement of any real outage.

## Requirements

Functional: retry a failed operation up to a configured number of times, waiting between attempts according to a pluggable strategy, and stop retrying once it succeeds or attempts run out.

Non-functional: the retry mechanism's own overhead has to be negligible next to whatever real operation it wraps. Under a shared failure, the retries from many independent clients should not re-synchronize into a second spike.

## Capacity estimation

The cost of computing a delay is nanoseconds, see Benchmark below, completely irrelevant next to a network call. The number that actually matters here isn't a per-call cost, it's the shape of aggregate retry traffic: 200 clients hitting a dependency in one synchronized burst is a very different load event than the same 200 clients spread over a window, even though the total number of requests is identical either way.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the storm and the fix, with diagrams.

## Data model

No state beyond the strategy's own configuration, Base, Max, and for FullJitter, a random source. Do itself is stateless between calls, all of a single retry sequence's state lives on the stack of the goroutine running it.

## API design

A Go library:

```go
err := retry.Do(func() error {
    return callTheRealDependency()
}, retry.NewFullJitter(100*time.Millisecond, 5*time.Second, time.Now().UnixNano()), 5, time.Sleep)
```

`Do` takes the operation, a `Strategy`, a max attempt count, and a sleep function, production code passes `time.Sleep`, tests pass something that doesn't actually wait.

## Implementation

Three strategies, one interface. `Fixed` always waits the same duration. `ExponentialBackoff` doubles the wait each attempt, up to a cap, which helps a single client back off but does nothing about many clients synchronizing. `FullJitter` draws the actual wait uniformly at random between 0 and the exponential cap, which is what breaks the synchronization. `Do` is the executor: it calls the operation, and on failure asks the strategy for the next delay via the injected sleep function rather than calling time.Sleep directly, which is what makes the tests run in milliseconds instead of actually waiting. Run the storm comparison directly:

```bash
go run ./cmd/demo
```

## Scaling

None of this changes with scale in the way a database or cache does, there's no capacity limit to hit. What changes is the blast radius of getting it wrong: a fixed delay retry policy is harmless with 5 clients and a genuine problem with 5,000, since the synchronized spike scales linearly with client count. The fix, full jitter, costs nothing extra at any scale, which is why there's little reason not to default to it.

## Failure modes

Demonstrated, not just described. 200 clients fail at the same instant and each computes its own retry delay:

```
fixed delay, no jitter:
  busiest bucket has 200 simultaneous retries

exponential backoff with full jitter:
  busiest bucket has 29 simultaneous retries
```

Same 200 clients, same overall time budget, an 85% reduction in the worst moment just from spreading the same total retries across the window instead of stacking them at one instant. Full output is in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metric that matters most is `retry_attempts_total{outcome="success|exhausted"}`, plus watching for correlated spikes in request rate at intervals matching your fixed delay, if you see periodic spikes exactly Wait apart, that's a retry storm signature.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Strategy | ns/op | allocs/op |
|---|---|---|
| Fixed.Delay | 0.33 | 0 |
| FullJitter.Delay | 6.13 | 0 |

Full numbers and the storm demonstration are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Fixed delay | Simplest possible, completely predictable per-client wait | Synchronizes every client that failed together into a second spike |
| Exponential backoff, no jitter | A single misbehaving client backs off over time | Still synchronizes with every other client on the same attempt number, doesn't fix the storm |
| Full jitter | Breaks synchronization across clients at no extra cost | Individual wait times are less predictable, a specific client might get a much shorter or longer wait than another on the same attempt |

The generally recommended default is full jitter, for exactly the reason measured above. Plain exponential backoff without jitter is a common mistake precisely because it looks like the fix, it does change behavior for a single client retrying repeatedly, while doing nothing for the actual failure mode that matters at scale: many clients failing together.
