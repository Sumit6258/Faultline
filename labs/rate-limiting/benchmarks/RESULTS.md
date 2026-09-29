# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=rate-limiting` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional, not a hardware comparison baseline)
- Command: go test -bench=. -benchmem -run=^$ ./...
- Date: 2026-09-28

## Results

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| FixedWindow.Allow | 87.64 | 0 | 0 |
| SlidingWindowCounter.Allow | 118.0 | 0 | 0 |
| TokenBucket.Allow | 108.6 | 0 | 0 |
| LeakyBucket.Allow (meter) | 106.7 | 0 | 0 |
| LeakyBucketQueue.Admit | 155.0 | 24 | 1 |
| TokenBucket.Allow, 10000 distinct keys | 190.5 | 16 | 1 |

These are a second run, taken after LeakyBucketQueue was added. The first run, before it existed, measured the same four algorithms 10 to 15 percent apart in both directions (87.9, 101.4, 95.4, 103.0 and 167.5 ns/op), which is ordinary run to run variance on a shared single vCPU sandbox. Read the ordering and the order of magnitude, not the last digit.

## Reading these numbers

All four algorithms cost under 110ns per call and allocate nothing on the hot path when a key already has an entry, since each one is a map lookup plus a bit of arithmetic under a mutex. FixedWindow is the cheapest, which tracks with it doing the least math per call. SlidingWindowCounter and LeakyBucket cost a little more for the extra state and division.

The many keys variant matters because a single hot key isn't realistic. A limiter keyed by user ID or IP address will have thousands of live entries, and every new key is a map insert. That one allocation per new key is why a production version needs the TTL sweep or bounded cache mentioned in fixed_window.go, or the map grows without bound.

These numbers say nothing about lock contention under real concurrent load across many goroutines competing for the same mutex. The load test results in ../load-tests/RESULTS.md are closer to that picture, though still on one CPU.
