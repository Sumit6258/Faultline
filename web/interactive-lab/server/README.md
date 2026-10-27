# Lab server

A small Go server that runs real Faultline lab code and streams what it reports to the interactive lab in the browser. Today it serves one scenario: Kafka consumer lag, from [`labs/kafka-messaging`](../../../labs/kafka-messaging).

## Why it exists

The other pages in the interactive lab run TypeScript ports of the Go labs. A port can drift from the original, and a reader has no way to tell. This server removes the question for the pages that use it: the lab code itself runs here, and the page shows what the lab reports. Nothing in `internal/kafkalag` reimplements Kafka behavior. Topics, partition assignment, committed offsets and lag all come from the lab.

## Run it

```bash
cd web/interactive-lab/server
go run ./cmd/labserver
```

In a second terminal:

```bash
cd web/interactive-lab
npm run dev
```

Open the **Kafka Lag (live)** page. The dev server proxies `/api` to `127.0.0.1:8081`, and the page connects by itself whenever the server is up, including after a restart.

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8081` | Listen address. There is no authentication, so keep it on loopback. |
| `-tick` | `250ms` | Wall clock time per simulated tick. |
| `-partitions` | `4` | Partitions in the topic. |

## API

| Endpoint | Purpose |
|---|---|
| `GET /api/kafka/stream` | Server-sent events. One `history` event with up to 120 recent snapshots, then one `snapshot` event per tick. |
| `GET /api/kafka/state` | The current snapshot and history as plain JSON, handy with `curl`. |
| `POST /api/kafka/config` | Partial update of `consumers` (1 to 12), `produceRate` (0 to 500 per tick), `consumeRate` (0 to 100 per consumer per tick), `hotKeyPercent` (0 to 100). Unknown fields and out of range values are a 400 with a message, and a rejected request changes nothing. |
| `POST /api/kafka/stall` | `{"consumer": 1, "stalled": true}` stops one consumer from processing anything. |
| `POST /api/kafka/reset` | Start over with an empty topic. `{"partitions": 8}` changes the partition count, an empty body keeps it. |
| `GET /api/health` | Liveness, and which scenarios exist. |

There is one shared session. Every open page sees the same run, and a change made in one tab shows up in the others.

## What the numbers mean

Each tick produces messages, then each consumer processes up to its budget, in the same order as the lab's own demo. The defaults match that demo: 4 partitions, 40 messages produced per tick, 10 consumed per consumer per tick. With them the session reports a total lag of 300, 200, 2 and 2 after 10 ticks for 1, 2, 4 and 8 consumers, the same figures `go run ./cmd/demo` prints in the lab. A test asserts this, so the live path and the lab's harness cannot drift apart without a failure.

The hot key setting sends that share of each tick's messages with one fixed key. Every message with that key hashes to one partition, and a partition has exactly one owner, so lag piles up there however many consumers exist.

## Decisions worth knowing

**It imports a lab, on purpose.** Labs never import each other. This server is the one consumer that imports one, through a `replace` directive in `go.mod`, because running the real lab code is its whole job.

**Changing the consumer count rebuilds the group.** The lab's `ConsumerGroup` assigns partitions once, when it is created, and has no rebalance of its own. The server builds a new group and carries the committed offsets across through the lab's public API, which is the property a real rebalance preserves: consumers join or leave and the group's progress does not reset. If the lab gains a rebalance, this adapter should be replaced by it.

**Memory is bounded by stopping the producer.** The lab's partitions are append-only and never trim, because the lab does not model retention. After 200,000 messages the producer halts and every snapshot says so, until the session is reset. A real broker would delete old segments instead.

**The page reconnects itself.** A browser retries an event stream that drops, but if the server answers with an error status it closes the stream for good. The dev proxy answers 502 while this server is down, so the page opens a new stream itself every two seconds until the server is back. Start the server late, or restart it, and the page picks up without a reload.

**A slow browser tab cannot slow the simulation.** Each tab has a small buffer. A tab that stops reading misses ticks, and the others are unaffected.

## Tests

```bash
go test -race ./...
```

They cover the session logic against the lab's own demo numbers, rebalances keeping their progress, consumers beyond the partition count sitting idle, the hot key and stalled consumer behavior, input validation, the message limit, concurrent use, and the HTTP layer including disconnects, a stuck subscriber and shutdown.

`testdata/snapshot.golden.json` is the contract with the page. It is generated from a real session, the Go tests fail if the server's JSON stops matching it, and the page's own tests read the same file and check it against the page's validator. Renaming a field breaks a test on one side or the other. After an intentional change, regenerate it with `UPDATE_GOLDEN=1 go test ./...` and update `src/lib/kafkaLive.ts` to match.

## Not built

Only the Kafka scenario exists. There is one shared session and no persistence. There is no authentication, which is why the default address is loopback. Adding another scenario means a new package under `internal/` with the same shape (a session with `Step`, validated config, immutable snapshots), routes under `/api/<scenario>/`, matching types and a validator in the page, a golden file, and a page.
