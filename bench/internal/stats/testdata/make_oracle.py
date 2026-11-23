"""Generates testdata/mannwhitney_oracle.json: p-values from scipy for random samples.

scipy is an independent implementation, so agreeing with it means the Go code
computes what the literature says it computes, not just what its author
thought it should. From internal/stats, regenerate with:
    python3 testdata/make_oracle.py
Requires numpy and scipy, which the Go code does not.
"""
import json
import pathlib
import random

from scipy.stats import mannwhitneyu

MAX_EXACT = 25  # keep in step with stats.MaxExact
rng = random.Random(20261101)
cases = []
for i in range(400):
    n1, n2 = rng.randint(2, 12), rng.randint(2, 12)
    if i % 10 == 0:
        n1, n2 = rng.randint(20, 40), rng.randint(20, 40)  # large enough to need the approximation
    shape = i % 4
    if shape == 0:      # clearly separated
        a = [rng.gauss(100, 3) for _ in range(n1)]; b = [rng.gauss(120, 3) for _ in range(n2)]
    elif shape == 1:    # the same distribution
        a = [rng.gauss(100, 8) for _ in range(n1)]; b = [rng.gauss(100, 8) for _ in range(n2)]
    elif shape == 2:    # integer timings, so ties are certain
        a = [float(rng.randint(90, 110)) for _ in range(n1)]; b = [float(rng.randint(95, 115)) for _ in range(n2)]
    else:               # skewed with an outlier, like real benchmark runs
        a = [rng.expovariate(1 / 10) + 100 for _ in range(n1)]; b = [rng.expovariate(1 / 10) + 104 for _ in range(n2)]
    has_ties = len(set(a + b)) < len(a) + len(b)
    exact = (not has_ties) and n1 <= MAX_EXACT and n2 <= MAX_EXACT
    p = mannwhitneyu(a, b, alternative="two-sided", method="exact" if exact else "asymptotic", use_continuity=True).pvalue
    cases.append({"a": a, "b": b, "p": float(p), "exact": exact})
out = pathlib.Path(__file__).parent / "mannwhitney_oracle.json"
json.dump(cases, open(out, "w"))
print(len(cases), "cases,", sum(c["exact"] for c in cases), "exact,", sum(not c["exact"] for c in cases), "asymptotic")
