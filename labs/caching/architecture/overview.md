# Architecture

## Cache-aside read path

```mermaid
sequenceDiagram
    participant C as Caller
    participant Cache as LRU cache
    participant Coal as Coalescer
    participant DB as Slow source

    C->>Cache: Get(key)
    alt cache hit
        Cache-->>C: value
    else cache miss
        C->>Coal: Do(key, fetch)
        alt another call for key already in flight
            Coal-->>C: wait, then return its result
        else first caller for key
            Coal->>DB: fetch()
            DB-->>Coal: value
            Coal->>Cache: Set(key, value)
            Coal-->>C: value
        end
    end
```

## The stampede, without coalescing

```mermaid
flowchart LR
    R1[Request 1] --> M{Cache miss}
    R2[Request 2] --> M
    R3[Request 3] --> M
    Rn[Request N] --> M
    M --> DB[(Database)]
```

A hot key expires. N requests arrive before any of them finishes repopulating the cache. Every one of them sees a miss and goes straight to the database, N times, at the exact moment the database is least prepared for a spike.

## The fix

```mermaid
flowchart LR
    R1[Request 1] --> Coal{Coalescer}
    R2[Request 2] --> Coal
    R3[Request 3] --> Coal
    Rn[Request N] --> Coal
    Coal -->|one call| DB[(Database)]
    Coal -->|N minus one waiters get the same result| R1
```

The coalescer lets the first request through and makes every other concurrent request for the same key wait on that one result instead of starting its own. Measured effect is in benchmarks/RESULTS.md.
