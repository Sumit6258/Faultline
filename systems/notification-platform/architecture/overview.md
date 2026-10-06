# Architecture

## Submitting and delivering

```mermaid
sequenceDiagram
    participant C as Client
    participant Svc as Service
    participant D as Dedup tracker
    participant Q as Queue (scheduled + ready heaps)
    participant W as Worker
    participant S as Sender (channel provider)

    C->>Svc: Submit(id, channel, priority)
    Svc->>D: Claim(id)
    alt already claimed
        D-->>Svc: false
        Svc-->>C: accepted=false, no-op
    else first time
        D-->>Svc: true
        Svc->>Q: Push(notification)
        Svc-->>C: accepted=true
        Note over W: workers continuously poll Q
        W->>Q: Pop(now)
        Q-->>W: highest priority due notification
        W->>S: Send(recipient, body)
        alt success
            S-->>W: ok
        else failure, attempts remain
            S-->>W: error
            W->>Q: Push back, ScheduledFor = now + backoff
        else failure, attempts exhausted
            S-->>W: error
            W->>W: DLQ.Add
        end
    end
```

## Why two heaps

```mermaid
flowchart LR
    New[New notification] --> Sched[Scheduled heap, ordered by time]
    Sched -->|ScheduledFor has passed| Ready[Ready heap, ordered by priority]
    Ready --> Pop[Pop returns the ready heap's top]
```

A single heap can only be ordered one way. This queue genuinely needs both orderings at once: a notification scheduled for an hour from now has to wait regardless of its priority, and among everything actually due right now, priority has to win regardless of submission order. See TestQueue_ReadyLowerPriorityBeatsNotYetDueHigherPriority for the case that only passes with two heaps.

## Retry, backoff, and the DLQ

```mermaid
flowchart TD
    Send[Send attempt] --> Result{Result}
    Result -->|success| Done[Delivered]
    Result -->|failure| Count{attempts >= MaxAttempts?}
    Count -->|no| Reschedule[Push back with full jitter backoff]
    Count -->|yes| DLQ[(Dead letter queue)]
    Reschedule --> Send
```

Full jitter, the same formula as labs/retries-backoff, a fresh implementation here rather than a shared import. Every lab and system in this repository stays independently buildable on its own; see that lab's README for why fixed delays synchronize into a retry storm and jitter doesn't.
