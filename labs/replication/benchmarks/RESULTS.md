# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=replication` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

A note before the numbers: this lab simulates replication rather than running real Postgres streaming replication. A real primary/standby setup was attempted first. Postgres itself installs and runs fine in this sandbox, but a running server does not reliably survive between separate tool calls here, which made a real multi-step replication demo too unreliable to ship. The simulation below models the same thing a real system does, an ordered write log a follower asynchronously applies, on a configurable delay, and every number below comes from actually running that simulation.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-24

## Write and sync cost

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| Leader.Write | 373.5 | 376 | 0 |
| Follower.Sync, no new entries | 97.79 | 0 | 0 |

Write costs more than a plain map insert because it appends to a growing slice and records a timestamp. Sync with nothing new to apply is cheap, a lock and a length check. Neither number is the point of this lab, see below.

## Replication lag and read-your-own-write

This is the result that matters, and it comes from `go run ./cmd/demo`, not a benchmark. A client writes, then reads its own write back from a follower after a random, realistic delay, 0 to 30ms:

```
follower lag=0s      read-your-own-write succeeded 200/200 times (100%)
follower lag=5ms     read-your-own-write succeeded 163/200 times (82%)
follower lag=15ms    read-your-own-write succeeded 94/200 times (47%)
follower lag=50ms    read-your-own-write succeeded 0/200 times (0%)
```

Same 200 writes, same random read timing, only the follower's lag changes. At 0 lag the write is always visible immediately. At 5ms lag, which is smaller than most of the simulated read delays, it's usually visible, about 82% of the time, close to the 83% predicted by the delay distribution. At 15ms lag it's a coin flip, 47%, close to the predicted 50%. At 50ms lag, longer than any simulated read delay, it's never visible in this run: the client always loses the race. This is exactly why "it worked in testing" and "it's flaky in production" can both be true for the same code, if the read timing in a test happens to consistently exceed the real lag, and production traffic patterns sometimes don't.
