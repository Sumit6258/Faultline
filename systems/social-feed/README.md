# Social Feed

The fourth complete system in this repository, and the one built to be measured against itself: three ways to build a home timeline, written as interchangeable strategies behind one interface, so the cost of each can be compared on the same code instead of argued from diagrams. Go.

## Problem

A social feed shows each person the recent posts of the people they follow. There are two obvious ways to build one, and each puts the cost on one side of the system. Do the work when a post is created, and every post by someone with a million followers becomes a million writes. Do the work when a timeline is read, and every refresh by someone following thousands of accounts becomes thousands of lookups. The engineering is deciding where to pay, and noticing that the answer is different for different accounts.

## Production context

Pushing posts into per-follower timelines, assembling timelines at read time, and a hybrid that treats very large accounts differently are all well documented approaches. Twitter's engineers described a push-based home timeline with high-follower accounts handled separately in public conference talks, and Raffi Krikorian's "Timelines at Scale" is the one usually cited. I did not read that talk for this lab. I am relying on secondary write-ups that cite it, so treat it as a pointer to go and read, and note that nothing here depends on any company's exact numbers. The celebrity threshold used here, 10,000 followers, is an illustrative choice; blog posts and interview write-ups quote very different values for where that line sits, and a real system tunes it.

## Requirements

Functional: follow and unfollow, create a post, and read a timeline of the newest posts from the accounts a user follows, newest first.

Non-functional: all three strategies must show a reader the same timeline, since they differ in where the work happens and not in what is visible. Fanning one post out to many followers must not serialize behind a single lock. A precomputed timeline must be bounded, since an unbounded one is a memory leak that grows with every post.

## Capacity estimation

Fan-out on write does exactly one inbox write per follower per post, and that is a counted number in this system, not an estimate. An account with 1,000,000 followers that posts 5 times a day, an illustrative rate, causes 5,000,000 inbox writes a day. Measured here, one post to 1,000,000 followers took between 0.82 and 1.48 seconds of single core time across three runs, see Benchmark.

An inbox bounded at 800 entries, the default, holds at most 6,400 bytes of post pointers per user, ignoring slice growth slack. Across 1,000,000 users that is 6.4 GB of pointers at the bound, which is the arithmetic that makes the bound necessary and not optional.

Whether paying all those writes up front is worth it depends on how many of those followers actually open their feed before the post is buried under newer ones. That ratio is not something this repository has measured, and it does not assume one.

## Architecture

See [architecture/overview.md](architecture/overview.md) for the three strategies side by side, where each puts its cost, why the hybrid's celebrity status is sticky, and how an inbox is stored.

## Data model

All in memory. A follow graph stored in both directions, because fan-out on write needs an author's followers and fan-out on read needs a reader's followees. A post store keyed by id and by author, newest first. For the write and hybrid strategies, 64 shards of per-user inboxes, each a bounded slice of post pointers.

## API design

```
POST /follow            {"follower": "alice", "followee": "bob"}      204
POST /posts             {"author": "bob", "body": "hello"}            200 {id, author, body, created_at}
GET  /timeline/{user}   ?limit=20, 1 to 200                         200 [{post}, ...] newest first
GET  /stats                                                          200 {"strategy": "...", "inbox_writes": N}
```

## Implementation

`internal/graph` is the follow graph. `internal/post` is the post store. `internal/timeline` holds the three strategies behind one `Strategy` interface, plus the two pieces they share: a k-way merge built on a heap, which costs O(k + limit log k) instead of the O(k limit log(k limit)) of concatenating and sorting, so fan-out on read is not made to look worse than it is, and a sharded inbox store whose push is amortized constant time. `feed.go` and `handler.go` wrap a chosen strategy in a service and an HTTP API. Run it:

```bash
go run ./cmd/server -strategy=hybrid -celebrity-threshold=10000
```

```bash
curl -X POST localhost:8080/follow -d '{"follower":"alice","followee":"bob"}'
curl -X POST localhost:8080/posts  -d '{"author":"bob","body":"hello"}'
curl localhost:8080/timeline/alice
```

`-strategy` takes `write`, `read`, or `hybrid`.

The tests that guard the subtle parts were checked by breaking the code on purpose. Recomputing celebrity status from the current follower count made the sticky-status test fail. Removing duplicate suppression from the merge made both duplicate tests fail. Having the hybrid stop merging celebrity posts at read time made four tests fail, the strategy-equivalence test among them. One of those three attempts first failed to compile, which proved nothing about the tests, and was redone in a form that compiles.

## Scope

Built: the three strategies, a bounded and sharded inbox, a sticky celebrity rule, an HTTP API, and measurements. Not built, and not implied: persistence of any kind, ranking (timelines are strictly chronological), backfilling a new follower's inbox with the author's earlier posts, removing posts from inboxes when someone is unfollowed or a post is deleted, post deletion at all, pagination past the newest N, a per-reader index of followed celebrities, and a networked inbox store. Two of those gaps are pinned by tests, `TestFanoutOnWrite_OnlyPostsMadeAfterFollowingAppear` for the missing backfill and `TestUnfollowTakesEffectImmediatelyOnlyUnderFanoutOnRead` for the missing cleanup, and one, the celebrity scan, is measured under Failure modes.

## Scaling

Everything here is one process. A real deployment would put inboxes in a networked cache or database, which changes the cost model in a way the in-process numbers here cannot show: an inbox write becomes a network call, and the ratio between a write and a read changes with it. The shape carries over, writes proportional to followers, reads proportional to followed accounts, but the absolute times do not.

Two refinements that are not built. The hybrid's read path scans every followed account to find the celebrities among them, which is why its read cost still grows with the number followed, see Failure modes. Keeping a per-reader set of followed celebrities, updated as people follow, unfollow, and as authors cross the threshold, would remove that scan. And fan-out on write could skip followers who have not been active recently, rebuilding their inbox on demand, which cuts the writes spent on inboxes nobody reads.

## Failure modes

Demonstrated, not just described.

The celebrity problem. One post by an account with N followers, one core:

```
followers   fan-out on write           fan-out on read   hybrid (threshold 10,000)
1,000       1,000 inbox writes         0 writes          1,000 inbox writes
10,000      10,000 inbox writes        0 writes          0 writes, single-digit microseconds
100,000     100,000 inbox writes       0 writes          0 writes, single-digit microseconds
1,000,000   1,000,000 inbox writes     0 writes          0 writes, single-digit microseconds
```

At 1,000,000 followers the fan-out took 0.82, 1.48, and 0.94 seconds in three runs. The count of writes is exact. The seconds vary by nearly a factor of two on identical code, and the cost per follower was not constant either, roughly 220 to 450 ns up to 100,000 followers and 820 to 1,480 ns at a million, so no figure is extrapolated to larger accounts.

Read cost as the number of followed accounts grows, median of three runs, newest 50 posts:

```
followed accounts   fan-out on write   fan-out on read   hybrid
13                  0.9 us             27.2 us           8.3 us
103                 0.9 us             128 us            14.7 us
1,003               1.0 us             1.21 ms           85.7 us
5,003               1.3 us             9.90 ms           369 us
```

Fan-out on write is flat. Fan-out on read grows essentially linearly, about 2 us per followed account at the top of the range. The hybrid is 3.3 times cheaper than fan-out on read at 13 followed accounts and 27 times cheaper at 5,003, but it is not flat: it still copies the reader's followee list on every read, about 74 ns per followed account, and at 5,003 it costs about 275 times what fan-out on write does. The measurement shows the textbook story is a little too tidy. The hybrid fixes the write side for celebrities. It does not by itself make reads independent of how many people you follow.

Ordinary authors, one follower: fan-out on write 323 ns per post, fan-out on read 2 ns, hybrid 406 ns. The hybrid pays a little extra to check whether the author is a celebrity.

Unfollowing, pinned by `TestUnfollowTakesEffectImmediatelyOnlyUnderFanoutOnRead`: under fan-out on read an unfollowed author's posts disappear at once. Under fan-out on write they stay in the inbox until pushed out by newer ones, because nothing removes them.

Full numbers, including the raw output of two runs, are in [benchmarks/RESULTS.md](benchmarks/RESULTS.md).

## Observability

`/stats` reports the strategy and `inbox_writes`, the running count of per-follower writes. That counter is the fan-out amplification of the system, and it is the one metric that would show a celebrity posting before anyone noticed latency. A full pass, per-post write counts, inbox sizes, read latency by followee count, is part of Phase 5 on the repository roadmap.

## Benchmark

Measured on this sandbox, Go 1.22.2, linux/amd64, 1 vCPU, Intel Xeon at 2.80GHz. This is a different machine from the one earlier labs were measured on, so do not compare absolute figures with theirs.

| Measurement | Fan-out on write | Fan-out on read | Hybrid |
|---|---|---|---|
| Post by an ordinary author, one follower | 323 ns | 2 ns | 406 ns |
| Timeline read, 5,003 followed accounts | 1.3 us | 9.90 ms | 369 us |
| One post, 1,000,000 followers | 0.82 to 1.48 s, 1,000,000 writes | no work | no writes |

Run to run variation here is large and is shown in [benchmarks/RESULTS.md](benchmarks/RESULTS.md) instead of hidden.

## Trade-offs

| Choice | What it buys | What it costs |
|---|---|---|
| Fan-out on write | Reads are one bounded, constant time inbox read | One write per follower per post, which is unbounded for the accounts that matter most, and stale inbox content after an unfollow |
| Fan-out on read | Posting costs the same for every author, unfollowing is immediate, a new follower sees recent history at once | Every read costs a lookup per followed account, about 2 us each here, paid again on every refresh |
| Hybrid | Bounded write cost for celebrities, cheap reads for the common case | The most code, a threshold that has to be tuned, a read path that still scans the followee list, and two sources of truth to keep consistent |
| A sticky celebrity flag | A post is never lost when an author's follower count dips below the threshold | An author who stops being large keeps being treated as large, so their new posts take the slower read-time path forever |
| A bounded inbox | Memory per user has a hard ceiling | A reader can never page back past the bound, and an inactive user's inbox is mostly stale |

A threshold-based hybrid is a decision about where the break-even sits, and this lab gives the two sides of that trade, writes proportional to followers and reads proportional to followed accounts, without claiming to know where a given product's break-even is. That depends on how many of an account's followers actually read, which has to be measured on real traffic.
