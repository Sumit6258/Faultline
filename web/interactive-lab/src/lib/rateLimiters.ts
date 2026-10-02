// The same four algorithms as labs/rate-limiting, reimplemented in
// TypeScript for the browser. Each takes a caller-supplied now, in
// milliseconds, rather than reading the clock itself, so the visualization
// can drive them from either real time or a scrubbable timeline, and so
// tests are deterministic, the same reason the Go versions take an
// injectable Clock.

export interface Limiter {
  allow(now: number): boolean;
}

export class FixedWindow implements Limiter {
  private windowStart = -Infinity;
  private count = 0;
  constructor(private limit: number, private windowMs: number) {}

  allow(now: number): boolean {
    if (now - this.windowStart >= this.windowMs) {
      this.windowStart = now;
      this.count = 1;
      return true;
    }
    if (this.count >= this.limit) return false;
    this.count++;
    return true;
  }
}

export class SlidingWindowCounter implements Limiter {
  private windowStart = -Infinity;
  private currCount = 0;
  private prevCount = 0;
  constructor(private limit: number, private windowMs: number) {}

  allow(now: number): boolean {
    const currentWindowStart = Math.floor(now / this.windowMs) * this.windowMs;
    if (currentWindowStart > this.windowStart) {
      const windowsPassed = this.windowStart === -Infinity ? Infinity : (currentWindowStart - this.windowStart) / this.windowMs;
      this.prevCount = windowsPassed === 1 ? this.currCount : 0;
      this.currCount = 0;
      this.windowStart = currentWindowStart;
    }
    const elapsedFraction = (now - this.windowStart) / this.windowMs;
    const estimated = this.prevCount * (1 - elapsedFraction) + this.currCount;
    if (estimated >= this.limit) return false;
    this.currCount++;
    return true;
  }
}

export class TokenBucket implements Limiter {
  private tokens: number;
  private lastRefill = -Infinity;
  constructor(private capacity: number, private refillPerSecond: number) {
    this.tokens = capacity;
  }

  allow(now: number): boolean {
    if (this.lastRefill === -Infinity) {
      this.lastRefill = now;
      this.tokens = this.capacity - 1;
      return true;
    }
    const elapsedSeconds = (now - this.lastRefill) / 1000;
    this.tokens = Math.min(this.capacity, this.tokens + elapsedSeconds * this.refillPerSecond);
    this.lastRefill = now;
    if (this.tokens < 1) return false;
    this.tokens -= 1;
    return true;
  }

  level(): number {
    return this.tokens;
  }
}

export class LeakyBucket implements Limiter {
  private level = 0;
  private lastLeak = -Infinity;
  constructor(private capacity: number, private leakPerSecond: number) {}

  allow(now: number): boolean {
    if (this.lastLeak === -Infinity) {
      this.lastLeak = now;
      this.level = 1;
      return true;
    }
    const elapsedSeconds = (now - this.lastLeak) / 1000;
    this.level = Math.max(0, this.level - elapsedSeconds * this.leakPerSecond);
    this.lastLeak = now;
    if (this.level + 1 > this.capacity) return false;
    this.level += 1;
    return true;
  }

  level_(): number {
    return this.level;
  }
}

export type AlgorithmName = "fixed-window" | "sliding-window" | "token-bucket" | "leaky-bucket";

export function createLimiter(name: AlgorithmName, limit: number, windowMs: number): Limiter {
  switch (name) {
    case "fixed-window":
      return new FixedWindow(limit, windowMs);
    case "sliding-window":
      return new SlidingWindowCounter(limit, windowMs);
    case "token-bucket":
      return new TokenBucket(limit, limit / (windowMs / 1000));
    case "leaky-bucket":
      return new LeakyBucket(limit, limit / (windowMs / 1000));
  }
}
