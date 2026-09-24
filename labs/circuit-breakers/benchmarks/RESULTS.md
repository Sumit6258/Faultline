# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=circuit-breakers` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-23

## Call() overhead

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| Call, closed, dependency succeeds | 34.28 | 0 | 0 |
| Call, open, fast reject | 68.04 | 0 | 0 |

The closed path is two mutex critical sections and a function call, about as cheap as this gets. The open path costs almost twice as much per call despite doing less real work, calling the clock function to check the cooldown accounts for the difference. Both numbers are noise next to any real dependency call, which is the entire point: the breaker's overhead when things are fine should never be the reason to avoid using one.

## The full state cycle

This is the result that matters, and it comes from `go run ./cmd/demo`, not a benchmark, since it's a correctness and behavior demonstration, not a speed measurement. A dependency fails on calls 3 through 5, then recovers:

```
call 1: state=closed    err=<nil>
call 2: state=closed    err=<nil>
call 3: state=closed    err=dependency unavailable
call 4: state=closed    err=dependency unavailable
call 5: state=open      err=dependency unavailable
call 6: state=open      err=circuit breaker is open

circuit is open, further calls fail fast without touching the dependency
call 7: state=open      err=circuit breaker is open (dependency actually called: false)

waiting for the 200ms cooldown...
state right after the wait, before any call is attempted: open
(the breaker checks the cooldown lazily, on the next call, not on a timer)

cooldown elapsed, next call is a half-open trial against the now-recovered dependency
call 8: state=closed    err=<nil>
call 9: state=closed    err=<nil>
call 10: state=closed    err=<nil>
```

Three consecutive failures opened the circuit at call 5. Call 6 and call 7 never reached the dependency at all, confirmed directly rather than assumed. The state stays open even after the cooldown has technically elapsed, until something actually calls the breaker again, since this implementation checks the cooldown lazily rather than running a background timer. Call 8 is the half-open trial. It succeeds, since the dependency has recovered by then, and the circuit closes.
