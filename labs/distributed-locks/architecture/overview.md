# Architecture

## The failure this lab is about

```mermaid
sequenceDiagram
    participant A as worker-A
    participant L as Lock service
    participant B as worker-B
    participant R as Protected resource

    A->>L: TryAcquire(job-1, ttl=200ms)
    L-->>A: token=1
    Note over A: A stalls for 300ms, GC pause, slow network, anything
    B->>L: TryAcquire(job-1, ttl=200ms)
    Note over L: A's lease already expired
    L-->>B: token=2
    B->>R: Write(token=2, "written by B")
    R-->>B: accepted
    Note over A: A wakes up, unaware it lost the lease
    A->>R: Write(token=1, "written by A, but stale")
    R-->>A: rejected, 1 is lower than the highest token seen
```

## Without fencing

```mermaid
sequenceDiagram
    participant A as worker-A
    participant B as worker-B
    participant R as Unfenced resource

    Note over A: A stalls past its lease, same as above
    B->>R: Write("written by B")
    Note over A: A wakes up, unaware it lost the lease
    A->>R: Write("written by A, but stale")
    Note over R: no token to check, the write just happens
    Note over R: resource now silently holds A's stale value
```

The lock service correctly told B it now owns the lease. The problem was never the lock service, it was assuming that holding a lease and being allowed to write are the same guarantee. They're only the same guarantee if the resource itself checks a token.
