# Replication and Replication Lag

## Problem

Replicating data to multiple nodes buys read scalability and durability, more copies, more places to serve a read from, no single disk failure losing everything. It also means those copies are not always identical at any given instant. Asynchronous replication, the common case, acknowledges a write as soon as the leader has it, before followers have applied it. Anything that reads from a follower immediately after a related write can see stale, or missing, data. This lab is about measuring exactly how often that happens, not just stating that it can.

## Production context

Leader-follower replication with asynchronous followers is a documented, extremely common pattern, used by PostgreSQL streaming replication, MySQL replication, and most managed database read replica offerings. "Read your own writes" is a documented consistency problem with that setup, commonly solved by reading from the leader for a short window after a write, or by tracking a client's last-written position and requiring a follower to have caught up to at least that point before serving its read. This lab implements neither fix, it exists to measure the problem, not solve it.

## Requirements

Functional: a leader accepts writes and keeps them in order. A follower asynchronously applies those writes, delayed by some lag, and can be read from directly.

Non-functional: measuring lag's effect has to be deterministic and fast to run, not dependent on real wall clock waiting, so it can run in a test suite. See the injectable clock in clock.go, the same pattern used in every other lab in this repository that deals with time.

## Capacity estimation

The leader's log in this lab keeps every entry ever written, in memory, forever, which is fine for a demo and not fine for anything real. A real system would need either a bounded retention window with old entries discarded once every follower has caught up, or a background process compacting the log, neither of which this lab implements. See Scaling.

## Architecture

See [architecture/overview.md](architecture/overview.md) for normal operation, the read-your-own-write failure, and why this lab simulates replication instead of using a real database, which is explained there in full rather than repeated here.

## Data model

The leader holds an ordered slice of log entries, sequence number, key, value, and write timestamp. Each follower holds its own local key-value map, built by applying log entries in order, plus how far into the log it has gotten. No entry is ever mutated once written, a follower catching up just replays history forward.

## API design

A Go library:

```go
leader := replication.NewLeader()
follower := replication.NewFollower(leader, 50*time.Millisecond)

leader.Write("key", "value")
follower.Sync()                 // pulls and applies anything old enough
value, ok := follower.Read("key")
```

## Implementation

`Leader.Write` appends to an in-memory log with an increasing sequence number and the real write timestamp. `Follower.Sync` asks the leader for everything after its own last-applied sequence, then applies only the entries old enough to satisfy its configured lag, stopping at the first one that isn't, since entries are chronological. Run the read-your-own-write measurement directly:

```bash
go run ./cmd/demo
```

## Scaling

This lab's leader keeps every entry forever and every follower polls by asking for everything since its last position, both of which are fine at the scale of a demo and neither of which scale to a real write-heavy system. A real leader needs bounded log retention, and real followers typically get pushed to via a streaming connection rather than polling. The property this lab does capture correctly at any scale is the shape of the trade-off: more followers, or slower followers, do not slow down the leader's writes at all, since followers pull independently. What they cost is staleness risk for anyone reading from them, which is a cost paid by readers, not writers.

## Failure modes

Demonstrated, not just described. A client writes, then reads its own write back after a realistic, randomized delay, 0 to 30ms, simulating normal request handling time:

```
follower lag=0s      read-your-own-write succeeded 200/200 times (100%)
follower lag=5ms     read-your-own-write succeeded 163/200 times (82%)
follower lag=15ms    read-your-own-write succeeded 94/200 times (47%)
follower lag=50ms    read-your-own-write succeeded 0/200 times (0%)
```

Same 200 writes, same read timing, only the lag changes. The failure rate isn't a fixed number, it's a race between how long the follower takes to catch up and how long the client happens to wait before reading. Full numbers are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. The metric that matters most in a real deployment is replication lag itself, in bytes or in seconds behind the leader, per follower, since it directly predicts how often reads from that follower will be stale. A follower whose lag is climbing over time, not just nonzero, usually means it can't keep up with write volume at all, a different and more serious problem than steady state lag.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Leader.Write | 373.5 | 0 |
| Follower.Sync, no new entries | 97.8 | 0 |

Full numbers and the lag demonstration are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Asynchronous replication | Writes to the leader are fast, they don't wait for any follower | Followers can be stale by an unbounded amount if they fall behind, and reads from them are never guaranteed fresh |
| Synchronous replication, not built in this lab | A write isn't acknowledged until at least one follower has it, bounding staleness | Every write now waits on the slowest required follower, directly costing write latency |
| Polling followers, this lab's approach | Simple, the follower controls its own pace | Real systems typically stream instead, since polling adds its own latency on top of replication lag |

Choosing between synchronous and asynchronous replication is choosing which side of the write pays for consistency. Asynchronous makes writers fast and leaves readers exposed to staleness, exactly what this lab measures. Synchronous makes readers safe and makes writers wait, which is a real and sometimes correct trade, just not the one this lab demonstrates.
