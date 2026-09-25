# Kafka Messaging and Consumer Lag

## Problem

A message queue decouples producers from consumers: a producer doesn't wait for anything downstream to process what it sends. That decoupling has a sharp edge. If consumers process messages slower than producers create them, the backlog, the lag, grows without bound, and nothing about the producer side will ever tell you that's happening. This lab is about making that backlog visible and measurable, and about the operational realities around it: how many consumers can actually help, what happens when a message is delivered twice, and what happens when one message can never be processed at all.

## Production context

Partitions, consumer groups, offsets, and consumer lag are Kafka's actual vocabulary, documented in Kafka's own architecture documentation. Idempotent consumption and dead letter queues are documented patterns for handling at-least-once delivery and poison messages, used across most production Kafka deployments and message queue systems generally, not unique to Kafka. This lab simulates these mechanics in Go rather than running real Kafka, see architecture/overview.md for exactly why.

## Requirements

Functional: messages with the same key always go to the same partition, in order. A consumer group divides partitions among its members so each partition has exactly one owner. Consumers can commit progress and query how far behind they are.

Non-functional: partition assignment and lag calculation need to stay correct and fast regardless of message volume, since in a real system this bookkeeping runs continuously alongside real traffic, not as an occasional batch job.

## Capacity estimation

This lab's in-memory log keeps every message forever, which is fine for a demo of a few thousand messages and not fine for a real topic, where retention is measured in days and message volume can be enormous. The number that matters more than storage here is throughput balance: if a topic averages 1000 messages per second and a consumer group can process 800 per second aggregate, lag grows by 200 messages every second, indefinitely, until something changes, more consumers up to the partition count, more partitions, or faster processing per message.

## Architecture

See [architecture/overview.md](architecture/overview.md) for how producing, consuming, idempotency, and the dead letter queue fit together, with diagrams.

## Data model

Each partition is an append-only slice of messages with sequential offsets. A consumer group tracks, per partition, which consumer owns it and what offset has been committed. Nothing here persists to disk, a real broker's entire value proposition is durable, replicated storage of this log, which is exactly the part this lab doesn't attempt to model.

## API design

A Go library:

```go
topic := kafkasim.NewTopic(4)
topic.Produce("user-42", "clicked-button")

group := kafkasim.NewConsumerGroup(topic, 2)
messages := group.Poll(0)          // everything consumer 0 owns and hasn't committed
group.Commit(partition, offset)
lag := group.TotalLag()
```

## Implementation

`Partition` is an ordered, append-only log. `Topic` hashes a message's key to pick a partition, so same key always means same partition, and therefore in-order processing for that key. `ConsumerGroup` assigns partitions round robin across a fixed number of consumers and tracks committed offsets, from which lag falls out directly: latest offset minus committed offset, per partition. `IdempotentConsumer` wraps a processing function with deduplication by key. `DLQ` is just a place to put messages that a caller has decided to give up on. Run all three demonstrations, consumer lag, idempotency, and the dead letter queue, directly:

```bash
go run ./cmd/demo
```

## Scaling

Consumer lag has exactly one real fix once you're already using all your partitions: more partitions. Adding consumers beyond the partition count, measured directly in this lab's demo, buys nothing, those consumers sit idle because there's nothing left to assign them. Increasing partition count is not free either: more partitions means more files and more memory overhead on a real broker, and repartitioning an existing topic is disruptive, since it can change which partition a given key maps to, breaking the in-order guarantee for that key across the transition. Getting the partition count right up front matters more than it looks like it should.

## Failure modes

Demonstrated, not just described, three separate ones.

Growing lag when consumers can't keep up:

```
1 consumers (1 active, 0 idle, only 4 partitions exist): lag after 10 ticks = 300
4 consumers (4 active, 0 idle, only 4 partitions exist): lag after 10 ticks = 2
8 consumers (4 active, 4 idle, only 4 partitions exist): lag after 10 ticks = 2
```

Duplicate delivery after a crash before commit:

```
message delivered twice, underlying charge ran 1 time(s)
```

A poison message that can never succeed:

```
order-2-poison: failed all 3 attempts, sent to DLQ
DLQ now holds 1 message(s), 2 processed normally
```

Full output and the reasoning behind each number is in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

Not wired up yet in this lab, that's part of Phase 5 on the roadmap. Consumer lag, per partition and summed per group, is the single most important metric in any real deployment of this pattern: it's the earliest, clearest signal that consumers are falling behind, well before anything downstream notices data is stale.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.10GHz:

| Operation | ns/op | allocs/op |
|---|---|---|
| Topic.Produce | 261.0 | 1 |
| ConsumerGroup.Commit | 16.7 | 0 |

Full numbers and all three failure mode demonstrations are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| More partitions | Higher ceiling on consumer parallelism | More overhead per partition on a real broker, and repartitioning later is disruptive |
| Idempotent consumers | Safe to redeliver a message without double-processing, required for at-least-once delivery to be usable | Extra bookkeeping, a processed-keys record that itself needs bounding or expiry |
| A dead letter queue | One bad message can't block a partition forever | Someone, or something, has to actually look at the DLQ, a DLQ nobody monitors just becomes a place failures go to be forgotten |

At-least-once delivery, what Kafka actually provides, is not the same as exactly-once processing. This lab's IdempotentConsumer is what closes that gap at the application level: Kafka's guarantee plus deduplication by key is what most systems actually mean when they say exactly-once.
