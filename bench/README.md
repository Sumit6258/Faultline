# faultbench

One tool for the repository's benchmarks: run all of them, report them together, compare two versions without being fooled by noise, and fail a build when something starts allocating that did not before.

It exists because the individual `benchmarks/RESULTS.md` files in each lab and system were written by hand, from different machines, on different days, and cannot be compared with each other or with next month's code.

## Use it

Run from the repository root. Build it once:

```bash
cd bench && go build -o ../faultbench ./cmd/faultbench && cd ..
```

**Measure everything and write the report.** About four minutes at five runs of each benchmark.

```bash
./faultbench run -count 5 -o bench-results.json
./faultbench report -in bench-results.json -o docs/BENCHMARKS.md
```

**Find out whether a change made something slower.** This is the command to use for it. It needs a second checkout of the code as it was:

```bash
git worktree add ../faultline-base main
./faultbench ab -old ../faultline-base -new . -only labs/caching
```

**Guard allocation counts.** This is what CI runs. After a deliberate change, record a new baseline.

```bash
./faultbench check
./faultbench check -update
```

`./faultbench compare old.json new.json` compares two saved results files. Use it only for two runs made back to back on a quiet machine, and see the next section for why `ab` is better.

## Why it is built this way

Each decision below was forced by something measured on this repository, on a shared one CPU virtual machine, which is a noisy place and so a good test of whether the tool can be trusted.

**Timings are noisy, and the report says by how much.** In the reference run, 56 benchmarks run five times each, the slowest run of a benchmark was a median of 23% slower than its fastest, 57% at the 90th percentile, and 70% at worst. 41 of the 56 differ by more than 15%, and the report marks them with a `*`. A table that printed one number per benchmark would hide that. Each row shows the median and the range of the runs instead.

**Comparing two separate batches of timings gives false changes.** The same code was run twice, a few minutes apart, and the two results compared. 17 of 56 benchmarks were flagged as having changed. The machine's speed drifted between the batches, and the statistics cannot know that. Dividing out the average drift afterwards only brought it to 11, because the noise is different for each benchmark.

**Interleaving fixes it.** `ab` builds both versions once, then runs them alternately, old then new, new then old, round after round, so anything that changes the machine's speed lands on both sides. The same identical-code test, interleaved, flagged 2 of 56. At a 5% significance level, about 3 of 56 would be flagged by chance alone with a perfectly sound method, so 2 is what a sound method looks like. The comparison output says how many to expect by chance, so a single flagged benchmark among many is read as weak evidence.

**A change has to be both significant and large enough to matter.** A benchmark is called slower or faster when a Mann-Whitney U test gives a p-value below 0.05 and the median moved by at least 5%. The test compares ranks, so one wild run cannot dominate it, and it assumes nothing about the shape of the timings. With too few runs it says so: three runs on each side can never reach significance, however different they are. The implementation was checked against scipy on 400 random cases covering clear differences, none, ties and outliers, and agrees to within 1e-9 where exact and 1e-7 where approximate.

**Allocations are exact, so they are what the build can fail on.** Go counts allocations rather than timing them, and for the same code they come out identical every run. Run three times, all 54 benchmarks that report allocations gave the same count every time. A change from zero allocations to one on a hot path is a real regression that no amount of noise can fake or hide. The baseline records the allocation count of every benchmark that reports one, and `check` fails if any gains an allocation, grows its bytes by more than 25% plus 64, or disappears. A benchmark with at least 100 allocations is allowed 1% more, because Go's maps allocate a slightly different number of overflow buckets from run to run, and without that a gate would fail on noise. Below 100 the count is exact, and a benchmark at zero can never gain one.

**Timings are never compared across machines.** A time is a property of a program on a computer. Results from different CPUs, operating systems or Go minor versions are refused, with the reason, unless `-force` is given, and then the output carries a warning. Allocation counts are still compared, because they do not depend on the machine.

## Reading the output

In the report, **Time** is the median with the fastest and slowest run in brackets, and `*` marks a benchmark whose runs differed by more than 15%. **B/op** and **allocs/op** are exact.

In a comparison, **Verdict** is one of slower, faster, no change, or too few runs. The change in allocations is shown separately and counts as a regression on its own.

## Limits

**The reference numbers are from a noisy machine.** `docs/BENCHMARKS.md` was generated on a shared one CPU virtual machine, and 41 of its 56 rows are starred. Treat it as a demonstration of the tool and a rough guide to relative costs. Regenerate it on your own machine, ideally a quiet one, and commit that.

**The allocation check holds benchmarks to their per-operation count.** Go reports allocations per operation as a whole number, so an allocation that happens once in a few thousand operations, such as a map growing, rounds down to zero at the fixed 1000 iterations the check uses. Some of the 22 zero allocation benchmarks are like that. The check is a tripwire for per-operation allocations, not a statement about steady state, and the iteration count is fixed so the baseline and the check always agree.

**The baseline is for one platform.** It was recorded on linux/amd64 with Go 1.22, which is what CI runs. On another platform, such as an Apple silicon Mac, the compiler can make different escape decisions, so the check says it has nothing to compare against and passes, instead of reporting false regressions. Record your own with `faultbench check -update`. CI uses `-require-baseline`, so a missing baseline there is a failure.

**The Python benchmark has times and no allocations.** `systems/payment-platform/benchmarks/bench_create_payment.py` prints in Go's benchmark format with `--format go`, which is how it joins the report.

**Statistics need runs.** The default is five for `run` and seven for `ab`. More makes the verdicts sharper, and on a noisy machine it is the only thing that does.

## In CI

`.github/workflows/bench.yml` builds the tool and runs `faultbench check -require-baseline`, which takes about twenty seconds. It deliberately does not fail on timings: a shared CI runner is noisier than the machine above, and a job that fails on noise gets ignored. Timings are for you to check with `ab` before merging something that touches a hot path.

## Adding a benchmark

Write a `func BenchmarkX(b *testing.B)` in any module. It is found automatically, and `check` reports it as new until the baseline is updated. Scripts that print Go benchmark lines are listed in `bench/benchmarks.json`.

## How it was tested

The parser, the statistics, the comparison and the baseline have unit tests. The runner is tested against real throwaway modules, including one that panics, a vendored copy that must be skipped, a script, and a module with a second package. The command line is tested end to end: measure, report, compare, record a baseline, add an allocation and watch the check fail, delete the benchmarks and watch it fail again. The interleaved runner is tested for strict alternation, for a tree that does five times the work, for identical trees, and for relative paths, which an early version got wrong because every test used absolute ones. The logic was broken 24 ways on purpose, and every break is caught. Two surprises came out of that: a test where the size of a change rather than its p-value decided the outcome, and the relative path bug above.
