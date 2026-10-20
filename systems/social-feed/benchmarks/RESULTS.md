# Benchmark results

Measured, not estimated. Reproduce with:

```bash
go test -bench=. -benchmem -benchtime=300ms -count=3 ./internal/timeline/
go run ./load-tests/celebrity
```

## Environment

- Go 1.22.2, linux/amd64
- CPU: Intel Xeon @ 2.80GHz, 1 vCPU, about 4 GB of memory. A shared sandbox, not a dedicated benchmarking host. This is a different machine than the 2.10GHz one the earlier labs and systems were measured on, so absolute figures here should not be compared with theirs.
- Date: 2026-10-15

## How much to trust these numbers

Run to run variation on this machine is large, and it is shown instead of hidden. The 1 million follower fan-out below took 0.82 s in a first run whose output was not saved, 1.48 s in a second, and 0.94 s in a third: nearly a factor of two on identical code. Garbage collection pressure from allocating a million inbox entries is the likely cause, but that is a guess, not something measured here. Benchmark tables give the median of three runs with the full range beside it. Read the orders of magnitude and the shapes of the curves. Do not read the third digit.

## The celebrity problem: the write side

One account with N followers makes one post, in one process on one core. "Inbox writes" is a counter inside the inbox store, not an estimate: it is exactly how many per-follower writes the post caused.

Third run, verbatim:

```
one post by an account with N followers, median of 3 runs, GOMAXPROCS=1

followers  strategy                   post latency   inbox writes  ns per follower
1000       fan-out on write                  252µs           1000              252
1000       fan-out on read                      0s              0                0
1000       hybrid (threshold 10k)            259µs           1000              259

10000      fan-out on write                2.506ms          10000              251
10000      fan-out on read                      0s              0                0
10000      hybrid (threshold 10k)              2µs              0                0

100000     fan-out on write                28.01ms         100000              280
100000     fan-out on read                     1µs              0                0
100000     hybrid (threshold 10k)              8µs              0                0

read cost for one follower afterward (reader follows only the star), N=100000, mean of 2000 reads:
  fan-out on write               70ns per read
  fan-out on read               680ns per read
  hybrid (threshold 10k)        630ns per read

1000000    fan-out on write              937.745ms        1000000              938
1000000    fan-out on read                     1µs              0                0
1000000    hybrid (threshold 10k)              9µs              0                0

fan-out on write cost between 251 and 938 ns per follower across the sizes measured, so no single
rate is quoted for larger accounts. What does not vary is the count, exactly one inbox write per follower:
  one post by an account with 10000000 followers = 10000000 inbox writes
  one post by an account with 100000000 followers = 100000000 inbox writes
```

Second run, verbatim, to show the spread:

```
one post by an account with N followers, median of 3 runs, GOMAXPROCS=1

followers  strategy                   post latency   inbox writes  ns per follower
1000       fan-out on write                  280µs           1000              280
1000       fan-out on read                      0s              0                0
1000       hybrid (threshold 10k)            324µs           1000              324

10000      fan-out on write                3.071ms          10000              307
10000      fan-out on read                      0s              0                0
10000      hybrid (threshold 10k)              2µs              0                0

100000     fan-out on write               44.665ms         100000              447
100000     fan-out on read                     1µs              0                0
100000     hybrid (threshold 10k)              4µs              0                0

read cost for one follower afterward (reader follows only the star), N=100000, mean of 2000 reads:
  fan-out on write               70ns per read
  fan-out on read               650ns per read
  hybrid (threshold 10k)        980ns per read

1000000    fan-out on write              1.475543s        1000000             1476
1000000    fan-out on read                     1µs              0                0
1000000    hybrid (threshold 10k)              8µs              0                0

extrapolated, not measured: at the 1476 ns per follower measured above for fan-out on write,
  one post by an account with 10000000 followers = 10000000 inbox writes, about 14.8 s of single-core write time
  one post by an account with 100000000 followers = 100000000 inbox writes, about 147.6 s of single-core write time
```

What this does show. Under fan-out on write the number of inbox writes is exactly the follower count, one per follower, every post. At 1,000,000 followers that was between 0.82 and 1.48 seconds of single core work for one post. Fan-out on read did no per-follower work at all, a microsecond or less. The hybrid fanned out normally below its threshold of 10,000 (1,000 writes at 1,000 followers) and did zero writes from 10,000 followers up, in single-digit microseconds.

What it does not show. The cost per follower was not constant: roughly 220 to 450 ns up to 100,000 followers, then roughly 820 to 1,480 ns at 1,000,000. So this does not support extrapolating a seconds figure to larger accounts, and none is quoted. The count of writes is the part that scales exactly. Also, these are in-process map writes. A real inbox store is a networked cache or database, where one write costs far more than a map insert and the ratio to a read is different, so the absolute times here say nothing about a production deployment. The shape, writes proportional to followers, does carry over.

## The read side

One reader, following F ordinary authors plus 3 celebrities, reads the newest 50 posts. Every author has posted 40 times. Every strategy holds the same state.

| Followed accounts | Strategy | Median (range of 3 runs) | Bytes per read | Allocs per read |
|---|---|---|---|---|
| 13 | fan-out on write | 924 ns (772 ns to 1.1 us) | 416 | 1 |
| 13 | fan-out on read | 27.2 us (23.1 us to 30.5 us) | 5656 | 31 |
| 13 | hybrid | 8.3 us (8.0 us to 12.9 us) | 2328 | 14 |
| 103 | fan-out on write | 943 ns (908 ns to 944 ns) | 416 | 1 |
| 103 | fan-out on read | 128.0 us (127.1 us to 133.1 us) | 42072 | 211 |
| 103 | hybrid | 14.7 us (13.8 us to 14.8 us) | 3912 | 14 |
| 1003 | fan-out on write | 978 ns (962 ns to 1.1 us) | 416 | 1 |
| 1003 | fan-out on read | 1.21 ms (1.21 ms to 1.37 ms) | 402648 | 2011 |
| 1003 | hybrid | 85.7 us (84.6 us to 89.5 us) | 18504 | 14 |
| 5003 | fan-out on write | 1.3 us (1.1 us to 1.5 us) | 416 | 1 |
| 5003 | fan-out on read | 9.90 ms (9.24 ms to 11.51 ms) | 2007256 | 10011 |
| 5003 | hybrid | 369.4 us (367.6 us to 381.0 us) | 84040 | 14 |

- **Fan-out on write** is flat, about 1 us, from 13 to 5,003 followed accounts (the median moved by a factor of 1.5 over a 385x increase in accounts).
- **Fan-out on read** grows with the number followed: the median rose by a factor of 365 over the 385x increase, essentially linearly, about 2.0 us per followed account at 5,003. It also allocates two objects per followed account, about 2 MB per read at 5,003.
- **Hybrid** is far cheaper than fan-out on read, 3.3x at 13 followed accounts and 27x at 5,003, but it is not flat. Its median rose by a factor of 45 over the same range, about 74 ns per followed account at 5,003, and at that size it is about 275x the cost of fan-out on write. Allocation count stays at 14, but bytes grow with the followee count, because each read copies the reader's whole followee list to find which of them are celebrities. A per-reader index of followed celebrities, kept up to date as people follow, unfollow, and cross the threshold, would remove that scan. It is not built here.

## The write side for an ordinary author

An author with one follower, so every strategy does its normal amount of work:

| Strategy | Median (range of 3 runs) | Allocs per post |
|---|---|---|
| fan-out on write | 323 ns (320 ns to 346 ns) | 1 |
| fan-out on read | 2 ns (2 ns to 2 ns) | 0 |
| hybrid | 406 ns (378 ns to 409 ns) | 1 |

Fan-out on read's no-op is the cost of an interface call. The hybrid costs a little more than plain fan-out on write because each post also checks whether the author is a celebrity, two read locks and two map lookups.
