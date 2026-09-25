# Architecture

## Producing and consuming

```mermaid
flowchart LR
    P[Producer] -->|hash of key| T{Topic}
    T --> P0[Partition 0]
    T --> P1[Partition 1]
    T --> P2[Partition 2]
    T --> P3[Partition 3]
    P0 --> C0[Consumer 0]
    P1 --> C1[Consumer 1]
    P2 --> C2[Consumer 2]
    P3 --> C3[Consumer 3]
```

Each partition is owned by exactly one consumer in the group at a time. That assignment, not the number of consumers, is what caps how much of the group's work can happen in parallel.

## Why lag grows

```mermaid
flowchart TB
    Tick["one tick"] --> Produced["40 messages produced"]
    Tick --> Consumed["numConsumers x 10 messages consumed, capped at 4 active consumers"]
    Produced --> Delta{Produced > Consumed?}
    Consumed --> Delta
    Delta -->|yes| Grow[Lag grows by the difference, every tick]
    Delta -->|no, and evenly distributed| Flat[Lag stays flat]
```

Measured numbers for four different consumer counts are in benchmarks/RESULTS.md, including the honest residual lag that shows up even when aggregate capacity matches aggregate production, because partition assignment doesn't guarantee a perfectly even split every tick.

## Idempotency and the DLQ

```mermaid
sequenceDiagram
    participant B as Broker (simulated)
    participant C as Consumer
    Note over C: processes message, crashes before committing
    B->>C: redelivers the same message
    Note over C: IdempotentConsumer recognizes the key, skips reprocessing
```

```mermaid
flowchart LR
    M[Message] --> Try1[Attempt 1]
    Try1 -->|fails| Try2[Attempt 2]
    Try2 -->|fails| Try3[Attempt 3]
    Try3 -->|fails| DLQ[(Dead letter queue)]
    Try3 -->|succeeds| Done[Committed normally]
```

## Why this lab is a simulation

Neither a real Kafka broker nor a Go client library for one is reachable from this sandbox. Kafka has no standard Ubuntu package the way Postgres and Redis do, and the Go module proxy needed to fetch a real client library isn't reachable either, see the repository's ARCHITECTURE.md for the full list of what is and isn't reachable here. kafkasim models the parts of Kafka that actually matter for the lessons in this lab, ordered partitions, consumer group assignment, offsets, lag, idempotent consumption, dead lettering, as real, tested Go code, without depending on a broker this sandbox can't run.
