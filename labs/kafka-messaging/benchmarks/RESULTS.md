# Benchmark results

Measured, not estimated. Reproduce with `make bench LAB=kafka-messaging` from the repository root, or `go test -bench=. -benchmem ./...` from this directory.

A note before the numbers: this lab simulates Kafka's core semantics, partitions, offsets, consumer groups, lag, rather than running a real broker. Neither a Kafka broker nor a Go client library for one is reachable from this sandbox: there's no Kafka package in Ubuntu's apt repositories the way there is for Postgres or Redis, and the Go module proxy needed to fetch a real client library like segmentio/kafka-go isn't reachable either. Every number below comes from actually running the simulation in kafkasim, not from a real cluster.

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.10GHz, 1 vCPU (a shared sandbox, not a dedicated benchmarking host, treat these as directional)
- Date: 2026-09-24

## Produce and commit cost

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| Topic.Produce | 261.0 | 259 | 1 |
| ConsumerGroup.Commit | 16.66 | 0 | 0 |

Produce costs more because it hashes the key and appends to a growing slice. Commit is nearly free, a lock and an integer comparison.

## Consumer lag versus consumer count

This is the result that matters, and it comes from `go run ./cmd/demo`, not a benchmark. A topic with 4 partitions, 40 messages produced per tick, each consumer instance able to process up to 10 per tick:

```
1 consumers (1 active, 0 idle, only 4 partitions exist): lag after 10 ticks = 300
2 consumers (2 active, 0 idle, only 4 partitions exist): lag after 10 ticks = 200
4 consumers (4 active, 0 idle, only 4 partitions exist): lag after 10 ticks = 2
8 consumers (4 active, 4 idle, only 4 partitions exist): lag after 10 ticks = 2
```

1 consumer has 10/tick of capacity against 40/tick of production, a 30/tick deficit, times 10 ticks is 300, exactly what was measured. 2 consumers, 20/tick capacity, 20/tick deficit, 200 lag, exact again. 4 consumers gives 40/tick capacity, matching production exactly on paper, but the measured lag is 2, not 0: messages don't split perfectly evenly across partitions every single tick, since partition assignment is a hash of the key, so a partition that happens to get slightly more than its even share in a given tick leaves a small residual for that consumer, even though aggregate supply equals aggregate demand. 8 consumers produces the identical result to 4, because only 4 partitions exist to assign, the other 4 consumers are provably idle the entire run, confirming that adding consumers beyond the partition count buys nothing.

## Idempotency and the dead letter queue

Also from `go run ./cmd/demo`:

```
message delivered twice, underlying charge ran 1 time(s)

order-1: processed successfully
order-2-poison: failed all 3 attempts, sent to DLQ
order-3: processed successfully
DLQ now holds 1 message(s), 2 processed normally
```

The idempotent consumer collapsed 2 deliveries of the same key into 1 real side effect. The dead letter queue let 2 good messages process normally while isolating the one that can never succeed, instead of that one message blocking its partition forever.
