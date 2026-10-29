#!/usr/bin/env python3
"""Fails if a dashboard or a rule refers to a metric that no service defines.

A renamed metric is the quiet way for observability to break: the service
still runs, the dashboard still loads, and the panel just goes empty, or the
alert never fires. This compares the metric names used in the Grafana
dashboards and the Prometheus rules against the names the code defines, so a
rename that misses one side fails CI instead of failing silently in
production.

It reads source text, so it needs no running services. observability/README.md
describes the end to end check that does run them.
"""

import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
OBS = ROOT / "observability"

# ----------------------------------------------------------- defined names

GO_DEF = re.compile(r'\bNew(Counter|Gauge|Histogram)(Vec|Func|FuncVec)?\(\s*"([a-zA-Z_:][a-zA-Z0-9_:]*)"')
PY_DEF = re.compile(r'\b(Counter|Gauge|Histogram|CounterMetricFamily|GaugeMetricFamily)\(\s*"([a-zA-Z_:][a-zA-Z0-9_:]*)"')


def expand(kind: str, name: str, counter_suffix: bool) -> set[str]:
    if kind == "Histogram":
        return {name + "_bucket", name + "_sum", name + "_count"}
    if kind.startswith("Counter") and counter_suffix and not name.endswith("_total"):
        return {name + "_total"}  # the Python client adds the suffix itself
    return {name}


def defined_names() -> set[str]:
    names = {"up"}
    go_files = list((OBS / "kit" / "go" / "obs").glob("*.go")) + [
        p for p in ROOT.rglob("metrics.go") if "node_modules" not in p.parts and "internal" not in p.parts
    ]
    for f in go_files:
        if f.name.endswith("_test.go"):
            continue
        for kind, _, name in GO_DEF.findall(f.read_text()):
            names |= expand(kind, name, counter_suffix=False)
    for f in ROOT.rglob("metrics.py"):
        if "node_modules" in f.parts:
            continue
        for kind, name in PY_DEF.findall(f.read_text()):
            names |= expand(kind, name, counter_suffix=True)
    return names


# ---------------------------------------------------------- referenced names

KEYWORDS = {
    "sum", "avg", "min", "max", "count", "topk", "bottomk", "quantile", "stddev", "group",
    "rate", "irate", "increase", "delta", "idelta", "deriv", "predict_linear", "histogram_quantile",
    "round", "abs", "ceil", "floor", "clamp", "clamp_min", "clamp_max", "absent", "absent_over_time",
    "label_replace", "label_join", "sort", "sort_desc", "time", "vector", "scalar", "changes", "resets",
    "avg_over_time", "max_over_time", "min_over_time", "sum_over_time", "count_over_time",
    "by", "without", "on", "ignoring", "group_left", "group_right", "and", "or", "unless", "bool", "offset",
}


def metrics_in(expr: str) -> set[str]:
    expr = re.sub(r'"[^"]*"|\'[^\']*\'', "", expr)          # string literals
    expr = re.sub(r"\{[^}]*\}", "", expr)                    # label matchers
    expr = re.sub(r"\[[^\]]*\]", "", expr)                   # range selectors
    expr = re.sub(r"\$\w+", "", expr)                        # grafana variables
    expr = re.sub(r"\b(by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)", "", expr)  # label lists
    return {w for w in re.findall(r"[a-zA-Z_:][a-zA-Z0-9_:]*", expr) if w not in KEYWORDS}


def dashboard_exprs() -> list[tuple[str, str]]:
    out = []
    for f in sorted((OBS / "grafana" / "dashboards").glob("*.json")):
        for panel in json.loads(f.read_text())["panels"]:
            for t in panel.get("targets", []):
                out.append((f"{f.name}: {panel['title']}", t["expr"]))
    return out


def rule_exprs() -> tuple[list[tuple[str, str]], set[str]]:
    exprs, recorded = [], set()
    for f in sorted((OBS / "prometheus" / "rules").glob("*.yml")):
        lines = f.read_text().splitlines()
        name = ""
        i = 0
        while i < len(lines):
            line = lines[i]
            m = re.match(r"\s*- (alert|record): (\S+)", line)
            if m:
                name = m.group(2)
                if m.group(1) == "record":
                    recorded.add(name)
            m = re.match(r"(\s*)expr:\s*(.*)$", line)
            if m:
                indent, rest = len(m.group(1)), m.group(2).strip()
                if rest in ("|", ">"):
                    block = []
                    i += 1
                    while i < len(lines) and (not lines[i].strip() or len(lines[i]) - len(lines[i].lstrip()) > indent):
                        block.append(lines[i])
                        i += 1
                    exprs.append((f"{f.name}: {name}", "\n".join(block)))
                    continue
                exprs.append((f"{f.name}: {name}", rest))
            i += 1
    return exprs, recorded


def readme_names() -> set[str]:
    """Metric names in the first column of the README's tables, as `name`."""
    names = set()
    for line in (OBS / "README.md").read_text().splitlines():
        m = re.match(r"\| `([a-zA-Z_:][a-zA-Z0-9_:]*)` \|", line)
        if m:
            names.add(m.group(1))
    return names


def main() -> int:
    defined = defined_names()
    rules, recorded = rule_exprs()
    known = defined | recorded
    problems = []
    for where, expr in dashboard_exprs() + rules:
        for name in sorted(metrics_in(expr) - known):
            problems.append(f"  {where}\n      uses '{name}', which no service defines")
    for name in sorted(readme_names() - known - {"InstanceDown"} - set(re.findall(r"^\| `([A-Z]\w+)`", (OBS / "README.md").read_text(), re.M))):
        problems.append(f"  README.md\n      lists '{name}', which no service defines")
    if problems:
        print("Metric names that nothing defines:")
        print("\n".join(problems))
        print(f"\n{len(defined)} metric names are defined in code. Rename the reference, or the metric, so they agree.")
        return 1
    uses = len(dashboard_exprs()) + len(rules)
    print(f"ok: {uses} expressions use only metrics that the code defines ({len(defined)} defined, {len(recorded)} recorded)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
