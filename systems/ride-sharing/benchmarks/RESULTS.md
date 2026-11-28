# Ride sharing: benchmark results

## Environment

| | |
|---|---|
| CPU | Intel(R) Xeon(R) Processor @ 2.30GHz, 1 logical CPU |
| Platform | linux/amd64 |
| Go | go1.22.2 |
| Runs | 5 of each benchmark, 200ms each |

**This is a shared single CPU virtual machine, and a noisy one.** A `*` marks a row whose five runs differed by more than 15%, which is 21 of the 34 here. Treat the numbers as orders of magnitude and as comparisons between rows measured together, and do not compare them with a table from other hardware. The tool that took the measurements, `faultbench`, and why it reports a median and a range, are described in `bench/README.md`. Regenerate these on your own machine with `make bench-all`.

## The driver index

The 5 nearest drivers within 3 km of a random point, as the number of drivers in the city grows. Brute force compares against every driver. The grid looks at the cells around the rider.

| Drivers | Brute force | Grid | Grid is faster by |
|---|---|---|---|
| 1,000 | 61.4 µs | 4.9 µs * | 13x |
| 10,000 | 817.0 µs * | 8.4 µs * | 97x |
| 100,000 | 6.76 ms | 80.0 µs | 84x |

On 20,000 drivers, in the test that checks the grid against brute force, the grid examined 48 drivers per query and brute force examined 20,000. Brute force grows with the city, and the grid grows with how crowded the rider's neighbourhood is, which is why 100 times more drivers costs it about 16 times more here and not 100.

## Pairing riders with drivers

The solvers alone, on square problems where every pairing is allowed:

| Size | Greedy | Optimal (Hungarian) |
|---|---|---|
| 10 riders, 10 drivers | 123 ns | 1.9 µs * |
| 50 riders, 50 drivers | 2.1 µs | 136.0 µs |
| 200 riders, 200 drivers | 158.6 µs * | 2.86 ms |

Real problems are lopsided: a rider can reach perhaps a tenth of the drivers, and there are several times more drivers than riders. The solver works on the smaller side:

| Problem | Optimal |
|---|---|
| 10 riders, 80 drivers | 11.0 µs * |
| 50 riders, 400 drivers | 198.3 µs * |
| 150 riders, 1,200 drivers | 1.90 ms |

A whole dispatch decision, with each waiting rider's 8 nearest drivers searched for and the pairing solved:

| Riders waiting | Greedy | Batched |
|---|---|---|
| 10 | 22.2 µs | 105.0 µs * |
| 50 | 140.4 µs * | 748.7 µs * |
| 150 | 377.6 µs * | 3.45 ms * |

Batched costs more than greedy and stays small. At 150 riders waiting it is a few milliseconds, which fits easily inside a 2 second batch window.

## The service

|  | Time | Allocations |
|---|---|---|
| Location update, one at a time | 440 ns * | 2 |
| Location update, parallel | 443 ns | 2 |
| Location update through the HTTP handler | 4.4 µs * | 24 |
| Request, match and cancel, grid index, 5,000 drivers | 11.3 µs * | 34 |
| The same with brute force | 528.3 µs | 64 |

The dispatch tick runs a few times a second whatever the load. It expires offers, drops silent drivers, runs matching and updates supply for pricing, with 5,000 drivers online and a queue of trips nobody can take yet:

| Trips waiting | Greedy | Batched | Allocations per tick |
|---|---|---|---|
| 0 | 420.2 µs * | 409.5 µs | 150 |
| 50 | 649.4 µs * | 646.3 µs | 209 |
| 200 | 1.47 ms * | 2.10 ms * | 359 |

## Memory

50,000 online drivers hold 14.3 MB, which is 287 bytes each, measured by `TestMemoryPerOnlineDriver` after a garbage collection.

## What the benchmarks changed

Two things in this code were slow in ways the tests could not show, and both were found by measuring.

- **The assignment solver padded every problem to a square.** A batched decision with 150 riders waiting took 652 ms, for a problem of 150 riders against about 1,200 drivers that is nothing like that hard. Solving the rectangular problem directly brought the same decision to about 3.5 ms. The exhaustive search that checks the solver was extended to lopsided shapes in both directions before the change, and passed after it.
- **The dispatch tick made an allocation per driver.** It built a text label for each available driver just to count drivers per neighbourhood: 5,013 allocations and about 1.0 ms a tick with 5,000 drivers. Counting by cell coordinates and naming only the occupied cells took it to 150 allocations and about 0.41 ms. The allocation baseline that CI enforces was recorded after this, so it holds the good number and not the wasteful one.

## Does batching pay? The simulation

`go run ./tools/sim -rates 6,10,13,15,17 -strategies greedy,batched:2s,batched:5s,batched:10s -seeds 5 -per-seed`

The simulation drives the real service on a virtual clock. Riders appear at random, 40% of them near one of three hotspots where drivers are scarce. Drivers are spread evenly, answer offers after 2 to 6 seconds, ignore 3% of them and decline 10% of those they answer. Trips take straight line distance divided by 8.3 m/s. Every strategy at a given rate sees the same riders and makes the same choices when offered the same trip, so differences between rows are down to the strategy. Five random cities are pooled per row, after a 10 minute warm up. A driver completes a trip about every 11 minutes, so 200 drivers saturate near 17 riders a minute.

200 drivers, 1h0m0s of riders after a 10m0s warm up, 5 cities pooled per row. Same riders and same driver choices for every strategy at a given rate.

| Riders/min | Strategy | Trips | No driver | Match wait p50 / p95 | Pickup ETA mean | Time to pickup mean / p95 | vs greedy | Deadhead per trip | Driver busy | Offers per trip |
|---|---|---|---|---|---|---|---|---|---|---|
| 6 | greedy | 1452 | 0.0% | 4.3 s / 9.0 s | 94 s | 99 s / 245 s | baseline | 780 m | 28% | 1.13 |
| 6 | batched 2s | 1452 | 0.0% | 5.3 s / 10.7 s | 94 s | 100 s / 246 s | +1.1% | 780 m | 28% | 1.13 |
| 6 | batched 5s | 1452 | 0.0% | 6.8 s / 14.3 s | 93 s | 101 s / 246 s | +2.1% | 774 m | 28% | 1.13 |
| 6 | batched 10s | 1452 | 0.0% | 9.4 s / 20.2 s | 93 s | 103 s / 245 s | +4.4% | 771 m | 28% | 1.13 |
| 10 | greedy | 2403 | 0.0% | 4.3 s / 8.8 s | 132 s | 137 s / 322 s | baseline | 1095 m | 49% | 1.13 |
| 10 | batched 2s | 2403 | 0.0% | 5.3 s / 10.4 s | 131 s | 137 s / 321 s | -0.1% | 1085 m | 49% | 1.13 |
| 10 | batched 5s | 2403 | 0.0% | 6.9 s / 14.1 s | 132 s | 139 s / 327 s | +2.1% | 1096 m | 49% | 1.13 |
| 10 | batched 10s | 2403 | 0.0% | 9.7 s / 19.6 s | 129 s | 140 s / 324 s | +2.2% | 1074 m | 49% | 1.13 |
| 13 | greedy | 3111 | 0.0% | 4.3 s / 9.3 s | 169 s | 173 s / 405 s | baseline | 1401 m | 67% | 1.13 |
| 13 | batched 2s | 3111 | 0.0% | 5.3 s / 10.8 s | 166 s | 172 s / 406 s | -0.7% | 1381 m | 67% | 1.13 |
| 13 | batched 5s | 3111 | 0.0% | 6.8 s / 14.7 s | 166 s | 173 s / 398 s | +0.1% | 1380 m | 67% | 1.13 |
| 13 | batched 10s | 3111 | 0.0% | 9.7 s / 20.1 s | 163 s | 173 s / 394 s | -0.2% | 1352 m | 67% | 1.13 |
| 15 | greedy | 3631 | 0.1% | 4.4 s / 14.7 s | 200 s | 206 s / 486 s | baseline | 1655 m | 80% | 1.13 |
| 15 | batched 2s | 3631 | 0.2% | 5.4 s / 13.5 s | 195 s | 202 s / 463 s | -1.8% | 1620 m | 80% | 1.13 |
| 15 | batched 5s | 3631 | 0.4% | 6.9 s / 16.5 s | 197 s | 205 s / 471 s | -0.2% | 1633 m | 80% | 1.13 |
| 15 | batched 10s | 3631 | 0.3% | 9.8 s / 21.5 s | 189 s | 200 s / 459 s | -2.9% | 1566 m | 80% | 1.13 |
| 17 | greedy | 4123 | 6.6% | 8.7 s / 118.7 s | 293 s | 335 s / 638 s | baseline | 2431 m | 93% | 1.07 |
| 17 | batched 2s | 4123 | 3.4% | 6.5 s / 69.6 s | 230 s | 246 s / 524 s | -26.6% | 1905 m | 92% | 1.10 |
| 17 | batched 5s | 4123 | 3.6% | 8.2 s / 68.4 s | 227 s | 244 s / 526 s | -27.1% | 1881 m | 92% | 1.10 |
| 17 | batched 10s | 4123 | 3.1% | 11.2 s / 67.9 s | 222 s | 240 s / 514 s | -28.2% | 1843 m | 92% | 1.10 |

Mean time to pickup in each city, in seconds, and the change from greedy in the same city:

| Riders/min | Strategy | City 1 | City 2 | City 3 | City 4 | City 5 |
|---|---|---|---|---|---|---|
| 6 | greedy | 116 | 93 | 101 | 77 | 106 |
| 6 | batched 2s | 117 (+1.0%) | 95 (+2.1%) | 101 (-0.4%) | 78 (+1.6%) | 107 (+1.1%) |
| 6 | batched 5s | 119 (+2.4%) | 95 (+2.4%) | 103 (+1.5%) | 80 (+3.7%) | 107 (+0.9%) |
| 6 | batched 10s | 121 (+4.5%) | 96 (+3.4%) | 106 (+5.1%) | 81 (+5.4%) | 110 (+4.1%) |
| 10 | greedy | 150 | 137 | 136 | 115 | 144 |
| 10 | batched 2s | 148 (-1.5%) | 140 (+1.6%) | 140 (+2.3%) | 115 (-0.3%) | 140 (-2.3%) |
| 10 | batched 5s | 151 (+0.7%) | 143 (+4.2%) | 145 (+6.1%) | 114 (-1.1%) | 144 (+0.4%) |
| 10 | batched 10s | 152 (+1.4%) | 142 (+3.2%) | 139 (+1.9%) | 118 (+2.7%) | 146 (+1.9%) |
| 13 | greedy | 173 | 161 | 198 | 154 | 181 |
| 13 | batched 2s | 171 (-1.2%) | 165 (+2.1%) | 195 (-1.5%) | 149 (-3.7%) | 182 (+0.5%) |
| 13 | batched 5s | 171 (-1.1%) | 164 (+1.4%) | 198 (+0.0%) | 155 (+0.1%) | 181 (+0.1%) |
| 13 | batched 10s | 172 (-0.9%) | 163 (+1.2%) | 193 (-2.5%) | 158 (+2.2%) | 180 (-0.5%) |
| 15 | greedy | 210 | 189 | 254 | 177 | 198 |
| 15 | batched 2s | 208 (-1.0%) | 193 (+2.2%) | 235 (-7.4%) | 174 (-1.4%) | 199 (+0.5%) |
| 15 | batched 5s | 212 (+1.1%) | 190 (+0.5%) | 236 (-6.9%) | 177 (-0.1%) | 211 (+6.6%) |
| 15 | batched 10s | 202 (-3.9%) | 188 (-0.4%) | 231 (-9.2%) | 182 (+3.2%) | 195 (-1.3%) |
| 17 | greedy | 386 | 372 | 327 | 268 | 322 |
| 17 | batched 2s | 254 (-34.1%) | 252 (-32.3%) | 243 (-25.8%) | 233 (-13.0%) | 246 (-23.4%) |
| 17 | batched 5s | 255 (-33.9%) | 244 (-34.2%) | 244 (-25.3%) | 231 (-13.8%) | 244 (-24.1%) |
| 17 | batched 10s | 251 (-35.1%) | 250 (-32.8%) | 242 (-26.1%) | 231 (-13.9%) | 229 (-28.9%) |

How to read it:

- **Time to pickup** is the match wait plus the drive to the rider. It is what a rider experiences.
- **Deadhead** is the distance a driver drives empty to reach a rider. When drivers are scarce, shorter empty drives are capacity.
- Below about 80% busy, the differences are a few percent and change sign from city to city, so they are noise. The only reliable effect there is that a longer window adds waiting.
- At 93% busy, batching is better in every city, and the window size makes little difference.

Limits. Travel is straight line at constant speed, with no roads or traffic. Idle drivers stay where their last trip ended. Riders do not react to price. There are five cities of 200 drivers, so differences under about 3% are not evidence. The shape of the result, nothing to gain at low load and a lot at saturation, is what to take from it. The percentages belong to this simulated city.

## Reproduce

```bash
cd systems/ride-sharing
go test -run '^$' -bench . -benchmem ./...
go test -run TestMemoryPerOnlineDriver -v .
go run ./tools/sim -rates 6,10,13,15,17 -strategies greedy,batched:2s,batched:5s,batched:10s -seeds 5 -per-seed
```
