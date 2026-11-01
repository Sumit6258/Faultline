# Chaos experiment results

Run on 2026-10-31 with Python 3.12.3 on Linux x86_64.
These are one run of each experiment, on a shared one core sandbox. Counts are exact. Timings are not.

## lost-response: passed

**Hypothesis.** When a payment is executed but its response is lost, a client that retries with the same idempotency key is charged once, and a client that retries with a fresh key is charged twice.

- ok: the fault dropped every first response (40/40)
- ok: every retry with the same key was recognised as a repeat (40/40), none created a second payment
- ok: each payment was charged exactly once
- ok: without a shared key, retrying after a lost response double charged every payment (40/40), so the experiment does reproduce the problem
- ok: a request lost before the service leaves nothing behind, and the retry creates the payment
- ok: database invariants hold: no violations

```text
Same key: 40 of 40 first attempts got no answer, yet 40 retries were told created=false, so the work had been done all along. The database holds 40 charges for 40 payments.
Fresh key (the control): 40 of 40 payments were charged twice, the failure idempotency keys prevent.
Lost request: 0 of 20 had reached the service, and 20 retries had to create the payment themselves.
Final state: 140 payments, 140 events, 40 executed without the client ever seeing an answer.
```

_The fault is applied at the HTTP layer by a proxy. It models a lost response, not a machine that loses power._

## crash-under-load: passed

**Hypothesis.** Killing the API with SIGKILL, repeatedly, in the middle of a burst of payments loses no acknowledged payment, charges none twice, and leaves none unannounced, provided clients retry with the same key.

- ok: the crash was visible to clients, so the fault really landed
- ok: every payment was eventually acknowledged (2956 of 2956)
- ok: database invariants hold: no violations
- ok: the restarted relay announced everything that was waiting (2956 of 2956 dispatched)

```text
2956 payments acknowledged, 623 of them before the first crash. The API was killed 5 times and was gone for 2.3s in total, during which clients saw {'conn_error': 168} and kept retrying.
7 payments had committed but their acknowledgement was lost to the crash. Their retries were recognised and answered created=false instead of charging again.
```

_SIGKILL ends the process but not the machine: the operating system still has everything the process wrote. This does not test a power cut._

## relay-stall: passed

**Hypothesis.** When the relay stalls, payments keep succeeding, the backlog and its age are visible in the metrics, and everything is announced exactly once when the relay resumes.

- ok: steady state: with the relay running, every event is announced within moments
- ok: steady state: PaymentOutboxStuck is not firing
- ok: payments kept succeeding while the relay was stalled (100/100), the API does not depend on it
- ok: the backlog is visible in the metrics (payments_outbox_pending = 100.0)
- ok: the age of the oldest event grew as the stall went on (36s)
- ok: PaymentOutboxStuck fired for a real stalled relay, 36s into the stall
- ok: after the relay resumed, the whole backlog was announced
- ok: each event was published exactly once (120 lines for 120 payments)
- ok: the alert resolved once the backlog drained
- ok: database invariants hold: no violations

```text
During 36s of stall: 100 of 100 payments accepted (p99 7ms), backlog 100, oldest event 36s old.
```

_A paused process is not a crashed one. A pause cannot cause duplicates, which is why this one publishes each event exactly once._

## relay-crash-duplicates: passed

**Hypothesis.** Crashing the relay with SIGKILL, over and over, never loses an event, but does publish some twice. Publishing is at least once, so a consumer has to cope with seeing an event again.

- ok: the backlog was fully drained in the end
- ok: no event was lost (0 missing)
- ok: the crashes did cause duplicates (14), so at least once publishing is real, not theoretical
- ok: a crash can duplicate at most the one event in flight (14 duplicates from 26 kills)
- ok: database invariants hold: no violations

```text
The relay was killed 26 times while draining 400 events. 414 lines were published for 400 distinct events, so 14 were published twice (one per kill that landed between publishing an event and marking it).
```

_A 15ms simulated broker acknowledgement widens the window in which a kill causes a duplicate. With no delay the window is real but far narrower, and duplicates are rarer, never impossible._

## db-write-lock: passed

**Hypothesis.** While something else holds the database's write lock, requests either wait and succeed or fail cleanly after the busy timeout, leave no partial state, metrics keep working, and the service recovers the moment the lock is released.

- ok: every request got an answer, none hung or lost its connection
- ok: failures came only after the 10 second busy timeout, not instantly
- ok: a failed request left no payment behind: it failed cleanly, with no partial state
- ok: the lock was held long enough to see both outcomes (8 succeeded, 4 failed)
- ok: /metrics kept answering during the lock (5ms), so observability survives a write lock
- ok: the failures show up in http_requests_total as 500s (4), which is what the error alert watches
- ok: after the lock was released, retrying the failed keys succeeded immediately
- ok: database invariants hold: no violations

```text
With the lock held for 14s and one request per second: 8 waited and succeeded, 4 failed with [500] after about 10s.
  request  0 started at + 0.0s: http_error 500 after  10.1s
  request  1 started at + 1.0s: http_error 500 after  10.0s
  request  2 started at + 2.0s: http_error 500 after  10.0s
  request  3 started at + 3.0s: http_error 500 after  10.0s
  request  4 started at + 4.0s: ok 200 after  10.0s
  request  5 started at + 5.0s: ok 200 after   9.1s
  request  6 started at + 6.0s: ok 200 after   8.0s
  request  7 started at + 7.0s: ok 200 after   7.1s
  request  8 started at + 8.0s: ok 200 after   6.0s
  request  9 started at + 9.0s: ok 200 after   5.0s
  request 10 started at +10.0s: ok 200 after   4.2s
  request 11 started at +11.0s: ok 200 after   3.1s
```

_The 10 second busy timeout is a decision in app/db.py. A shorter one would fail faster and free the request threads sooner, a longer one would ride out longer locks._

## latency-invisible-to-the-service: passed

**Hypothesis.** Delay added on the network moves what users experience but not the service's own latency histogram. Only a measurement taken at the edge shows it.

- ok: users waited 228ms longer
- ok: the service's histogram barely moved (1.1ms), it cannot see delay added before the request arrives
- ok: a histogram at the edge does show the delay

```text
Client mean latency went from 4ms to 232ms.
The service's own histogram for POST /payments went from 2.2ms to 3.2ms.
The proxy's histogram, measured at the edge where clients are, reports 230ms.
```

_This is why alerts on a service's own latency are not enough on their own: a slow network, load balancer or queue in front of it is invisible to it._
