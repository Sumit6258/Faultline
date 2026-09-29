# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=consistent-hashing` from the repository root, or `go test -bench=. -benchmem ./...` from this directory. The distribution and remapping numbers below come from `go run ./cmd/demo`, not from a Go benchmark, since they're about correctness properties, not speed.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-29

## Get() throughput

20 nodes, keys drawn from a pool of 10000:

| Implementation | ns/op | B/op | allocs/op |
|---|---|---|---|
| Ring, 150 virtual nodes per real node | 172.6 | 8 | 1 |
| NaiveModN | 30.9 | 8 | 1 |

The allocation in both cases comes from converting the key to a byte slice for crc32.ChecksumIEEE, not from anything specific to the ring. Ring costs about 5.6x more per lookup than naive mod-N, because Get does a binary search over 3000 sorted points (20 nodes times 150 replicas), where naive mod-N does one hash and one modulo. That extra cost buys the remapping behavior below.

A first version of this benchmark built each key string inside the timed loop for both functions. That added the same roughly 140ns of string-building work to both sides, which is why it originally reported 208.8ns and 66.91ns, a 3x gap instead of the real 5.6x: a fixed cost added to two different base numbers shrinks their ratio. Moving key construction before b.ResetTimer() for both benchmarks, shown in bench_test.go, is what fixed it.

## Distribution evenness

5 nodes, 10000 keys, run via `go run ./cmd/demo`:

```
naive mod-N                                      min=1922 max=2048 avg=2000 spread=6.3%
consistent hash, 1 virtual node per real node     min=12 max=9810 avg=2000 spread=489.9%
consistent hash, 150 virtual nodes per real node  min=1452 max=2604 avg=2000 spread=57.6%
```

This is the honest, slightly uncomfortable result: naive mod-N distributes more evenly than consistent hashing does, even with 150 virtual nodes per real node. With only 1 virtual node per real node, the ring is badly skewed, one node got 12 keys and another got 9810, because 5 random points on a ring don't divide it anywhere close to evenly. 150 virtual points per node smooths that out considerably but still doesn't match naive mod-N's near-perfect split. Consistent hashing isn't chosen for steady-state evenness. It's chosen for what happens next.

## Remapping cost when the node count changes

Same 10000 keys, removing 1 of the 5 nodes:

```
naive mod-N:        7945 of 10000 keys moved (79.5%)
consistent hashing: 1796 of 10000 keys moved (18.0%)
theoretical minimum for consistent hashing removing 1 of 5 nodes: about 20.0%
```

This is the number that actually matters. Removing one node should, ideally, only remap that node's own share of the keys, about 1 in 5, 20%. Naive mod-N remaps nearly 8 in 10 keys, because changing the node count changes the modulus for every key, not just the ones that were on the removed node. Consistent hashing landed within 2 percentage points of the theoretical minimum.
