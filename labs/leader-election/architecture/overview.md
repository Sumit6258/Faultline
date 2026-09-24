# Architecture

## Normal operation

```mermaid
sequenceDiagram
    participant N1 as node-1 (leader)
    participant C as Cluster
    participant N2 as node-2
    participant N3 as node-3

    N1->>C: Heartbeat(node-1)
    C-->>N1: term=1, leader=true
    N2->>C: Heartbeat(node-2)
    C-->>N2: leader=false, node-1 still fresh
    N3->>C: Heartbeat(node-3)
    C-->>N3: leader=false, node-1 still fresh
```

## Leader goes silent, election happens

```mermaid
sequenceDiagram
    participant N1 as node-1 (crashed)
    participant C as Cluster
    participant N2 as node-2
    participant N3 as node-3

    Note over N1: stops heartbeating
    Note over C: timeout elapses with no heartbeat from node-1
    N2->>C: Heartbeat(node-2)
    C-->>N2: term=2, leader=true
    N3->>C: Heartbeat(node-3)
    C-->>N3: term=2, leader=false, node-2 already claimed this term
```

Whichever node's heartbeat reaches the cluster first after the timeout wins. This lab's Cluster is a single, shared piece of state, which is exactly what makes the race resolve cleanly to one winner. See the README's Scaling section for what changes once there's no longer one obviously correct copy of that state to check against.
