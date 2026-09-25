# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=retries-backoff` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-24

## Delay computation cost

| Benchmark | ns/op | allocs/op |
|---|---|---|
| Fixed.Delay | 0.33 | 0 |
| FullJitter.Delay | 6.13 | 0 |

Fixed is close to free, it returns a struct field. FullJitter costs about 18x more, still under 10ns, for a random draw from the exponential cap. Neither number matters in practice, both are dwarfed by any real network call a retry would wrap.

## The retry storm

This is the result that matters, and it comes from `go run ./cmd/demo`, not a benchmark, since it's about traffic shape, not speed. 200 clients all fail at the same instant, a shared dependency blips, and each independently computes its own retry delay:

```
fixed delay, no jitter:
  200 clients, 1 distinct 20ms buckets used, busiest bucket has 200 simultaneous retries

exponential backoff with full jitter:
  200 clients, 10 distinct 20ms buckets used, busiest bucket has 29 simultaneous retries
```

Both strategies were configured with the same effective budget, up to 200ms before retrying. Fixed delay means every single client computes the exact same 200ms wait and all 200 land in the same 20ms window: the retry storm. Full jitter draws each client's delay uniformly from 0 up to that same 200ms cap, spreading the 200 retries across 10 buckets instead of 1, and dropping the busiest moment from 200 simultaneous retries to 29, an 85% reduction in peak load with nothing else about the system changed.
