# Contributing

Faultline takes contributions, but every addition has to clear the same bar the existing labs are held to.

## Definition of done, per lab

- [ ] README follows the standard structure: problem, production context, requirements, capacity estimation, architecture, data model, API design, implementation, scaling, failure modes, observability, benchmark, trade-offs
- [ ] Every benchmark number has a matching RESULTS.md, or is labeled "expected, not yet run"
- [ ] At least one failure mode is implemented and demonstrated, not just described in prose
- [ ] Tests pass with the race detector (go test -race ./... for Go labs), including at least one concurrency or duplicate delivery case where relevant
- [ ] Metrics use the naming convention in docs/observability/conventions.md
- [ ] Architecture diagrams are text or Mermaid, not screenshots
- [ ] Every claim in the README is either something the code actually does, documented public practice with a source, or explicitly labeled a simplification

## Adding a new lab

1. Copy the structure of an existing lab under labs/
2. Write the implementation first, then the tests, then the README
3. Run the benchmarks and commit the real output to benchmarks/RESULTS.md
4. Open a pull request. The checklist above is the review criteria.

## Writing style

See docs/STYLE.md. The short version: explain what a thing is for and what it costs, and don't claim something ran if it didn't.
