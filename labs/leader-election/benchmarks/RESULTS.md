# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=leader-election` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-23

## Heartbeat cost

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| Cluster.Heartbeat, same leader renewing | 71.78 | 0 | 0 |

A renewal heartbeat is a mutex lock, a time comparison, and a timestamp update, nothing allocates. At this cost, a cluster could handle heartbeats from a very large number of nodes without the heartbeat mechanism itself becoming a bottleneck. The real constraint on heartbeat frequency in a production system is almost always the timeout tuning, not raw throughput: a short timeout detects a dead leader fast but risks a false failover during a brief network hiccup, a long timeout is more tolerant of noise but leaves the cluster leaderless for longer after a real crash.

## The election itself

This is the result that matters, and it comes from `go run ./cmd/demo`, not a benchmark:

```
3 nodes start heartbeating. node-1 heartbeats first.
node-1 heartbeat: term=1 leader=true
node-2 heartbeat: leader=false (refused, node-1 already leads this term)
node-3 heartbeat: leader=false (refused, node-1 already leads this term)

node-1 keeps heartbeating normally for a while...
node-1 heartbeat: term=1 leader=true
node-1 heartbeat: term=1 leader=true
node-1 heartbeat: term=1 leader=true

node-1 crashes. it stops heartbeating.
waiting past the 200ms timeout...

node-2 and node-3 both notice the silence and heartbeat.
node-2 heartbeat: term=2 leader=true
node-3 heartbeat: term=2 leader=false

final leader: node-2, term 2
```

node-2 happened to heartbeat first in this run and won term 2. node-3's heartbeat, sent immediately after, found node-2 already installed as leader for that term and was refused, which is correct: exactly one node should win a contested election, not zero and not two.
