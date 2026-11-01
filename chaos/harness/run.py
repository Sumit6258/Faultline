"""Runs the experiments.

    python -m chaos.harness.run --list
    python -m chaos.harness.run                       run all of them
    python -m chaos.harness.run --only lost-response  run some, comma separated
    python -m chaos.harness.run --report chaos/RESULTS.md

Run it from the repository root, with the payment platform's requirements
installed. It needs go to build the proxy, unless CHAOSPROXY points at a built
one. Set PROMETHEUS to a prometheus binary to also check that the alerts fire.
"""

import argparse
import os
import platform
import sys
import tempfile
import time
from pathlib import Path

from .experiments import EXPERIMENTS, Context, Result


def render(results: list[Result]) -> str:
    lines = []
    for r in results:
        lines.append(f"{'PASS' if r.passed else 'FAIL'}  {r.name}  ({r.seconds:.0f}s)")
        lines.append(f"      hypothesis: {r.hypothesis}")
        for ok, msg in r.checks:
            lines.append(f"      {'ok  ' if ok else 'FAIL'} {msg}")
        for f in r.findings:
            lines.append(f"      measured: {f}")
        for n in r.notes:
            lines.append(f"      note: {n}")
        if r.error:
            lines.append("      crashed:\n" + "\n".join("        " + l for l in r.error.rstrip().splitlines()))
        lines.append("")
    passed = sum(r.passed for r in results)
    lines.append(f"{passed} of {len(results)} experiments passed")
    return "\n".join(lines)


def render_markdown(results: list[Result]) -> str:
    out = [
        "# Chaos experiment results",
        "",
        f"Run on {time.strftime('%Y-%m-%d')} with Python {platform.python_version()} on {platform.system()} {platform.machine()}.",
        "These are one run of each experiment, on a shared one core sandbox. Counts are exact. Timings are not.",
        "",
    ]
    for r in results:
        out += [f"## {r.name}: {'passed' if r.passed else 'FAILED'}", "", f"**Hypothesis.** {r.hypothesis}", ""]
        out += [f"- {'ok' if ok else 'FAILED'}: {msg}" for ok, msg in r.checks]
        out += ["", "```text"] + r.findings + ["```", ""]
        out += [f"_{n}_" for n in r.notes] + [""]
        if r.error:
            out += ["```", r.error.rstrip(), "```", ""]
    return "\n".join(out)


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true")
    ap.add_argument("--only", help="comma separated experiment names")
    ap.add_argument("--report", help="also write a markdown report to this path")
    args = ap.parse_args(argv)

    if args.list:
        for name, (hypothesis, _) in EXPERIMENTS.items():
            print(f"{name}\n    {hypothesis}")
        return 0

    names = args.only.split(",") if args.only else list(EXPERIMENTS)
    unknown = [n for n in names if n not in EXPERIMENTS]
    if unknown:
        print(f"unknown experiment(s): {', '.join(unknown)}. Try --list.", file=sys.stderr)
        return 2

    results = []
    for name in names:
        print(f"running {name} ...", flush=True)
        with tempfile.TemporaryDirectory(prefix=f"chaos-{name}-") as d:
            ctx = Context(Path(d), os.environ.get("PROMETHEUS"), os.environ.get("PROMTOOL"))
            results.append(EXPERIMENTS[name][1](ctx))
    print()
    print(render(results))
    if args.report:
        Path(args.report).write_text(render_markdown(results))
    return 0 if all(r.passed for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())
