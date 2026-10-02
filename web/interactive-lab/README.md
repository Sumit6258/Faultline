# Interactive Lab

A browser based visualization of three of this repository's labs: consistent hashing, rate limiting, and circuit breakers. Vite, React, and TypeScript, no other framework.

## Why these three first

They're the three named in [ROADMAP.md](../../ROADMAP.md)'s Phase 4, and each one is genuinely clearer to understand by interacting with it than by reading a README: watching which keys move when a node is removed, watching a burst get smoothed or not depending on algorithm, watching a circuit actually flip through closed, open, and half-open.

## What's real here

Every algorithm in `src/lib/` is a full reimplementation in TypeScript, not a scripted animation standing in for one, ported from the corresponding Go lab (`labs/consistent-hashing`, `labs/rate-limiting`, `labs/circuit-breakers`) including the fixes that came out of testing those labs for real: the consistent hashing ring stores one point per owner, not one node per hash value, specifically because that bug was found and fixed in the Go version first, see `labs/consistent-hashing/README.md`. 32 tests cover the three algorithm libraries and the three components: `npm test`.

## What hasn't been visually verified

This was built and tested in a sandbox with no real browser available, `npm run build` succeeds and every test passes under jsdom, which checks DOM structure and behavior, but nothing here has actually been looked at rendered. The CSS is deliberately plain (flexbox, CSS custom properties, no exotic layout) to keep that risk low, but "looks right" is genuinely unverified until you run it yourself.

## Requirements

Node.js 24 or newer. This was built against Vite 8 and Vitest 5, both recent enough that their dependencies (rolldown, jsdom 30, among others) need a Node version from late 2025 or newer, 24.15.0 at minimum; jsdom specifically needs 22.22.2, 24.15.0, or 26.0.0 and up, nothing in between. Node 18, still common on machines that haven't been updated recently, fails immediately on `npm run dev` with a `styleText` import error, that function was added to `node:util` after Node 18 stopped receiving updates. `.nvmrc` in this folder pins 24, so `nvm use` picks the right version automatically if you have nvm installed.

## Running it

```bash
nvm use            # or: nvm install 24 && nvm use 24, if you don't have it yet
npm install
npm run dev       # starts a local dev server
npm test          # runs all 32 tests
npm run build     # type-checks and produces a production build in dist/
```

## Structure

```text
src/
├── lib/                  pure algorithm ports, no React, fully unit tested
│   ├── consistentHash.ts
│   ├── rateLimiters.ts
│   └── circuitBreaker.ts
├── components/           one visualization per lab
│   ├── ConsistentHashingViz.tsx
│   ├── RateLimiterViz.tsx
│   └── CircuitBreakerViz.tsx
├── App.tsx               sidebar navigation between the three
└── styles.css             shared design tokens, plain CSS custom properties
```

## What's next

Notification Platform and Social Feed are the rest of Phase 4. A second tier of visualization, backend driven rather than client side simulated, e.g. real consumer lag from a running `labs/kafka-messaging` instance, is planned but not started; see this repository's `ARCHITECTURE.md` for the client-side-first, backend-driven-second split and why.
