import { describe, it, expect } from "vitest";
import { FixedWindow, SlidingWindowCounter, TokenBucket, LeakyBucket } from "./rateLimiters";

describe("FixedWindow", () => {
  it("allows up to the limit then blocks", () => {
    const fw = new FixedWindow(3, 1000);
    expect(fw.allow(0)).toBe(true);
    expect(fw.allow(0)).toBe(true);
    expect(fw.allow(0)).toBe(true);
    expect(fw.allow(0)).toBe(false);
  });

  it("resets on a new window", () => {
    const fw = new FixedWindow(2, 1000);
    fw.allow(0);
    fw.allow(0);
    expect(fw.allow(0)).toBe(false);
    expect(fw.allow(1000)).toBe(true);
  });

  it("lets close to double the limit through across a window boundary", () => {
    // The classic fixed-window failure: a full window's worth right before
    // the boundary, another full window's worth right after. limit=4
    // should let up to 8 through. The window boundary is set by the first
    // call (this implementation is not aligned to absolute clock ticks),
    // so the first call here is what fixes that boundary at t=0.
    const fw = new FixedWindow(4, 1000);
    let accepted = 0;
    if (fw.allow(0)) accepted++;
    for (let i = 0; i < 3; i++) if (fw.allow(900)) accepted++;
    for (let i = 0; i < 4; i++) if (fw.allow(1100)) accepted++;
    expect(accepted).toBe(8);
  });
});

describe("SlidingWindowCounter", () => {
  it("allows up to the limit then blocks", () => {
    const sw = new SlidingWindowCounter(3, 1000);
    expect(sw.allow(0)).toBe(true);
    expect(sw.allow(0)).toBe(true);
    expect(sw.allow(0)).toBe(true);
    expect(sw.allow(0)).toBe(false);
  });

  it("smooths the same boundary case FixedWindow fails", () => {
    const sw = new SlidingWindowCounter(4, 1000);
    let accepted = 0;
    for (let i = 0; i < 4; i++) if (sw.allow(900)) accepted++;
    for (let i = 0; i < 4; i++) if (sw.allow(1100)) accepted++;
    // Matches the measured result in labs/rate-limiting: 5 of 8, not 8 of 8.
    expect(accepted).toBe(5);
  });
});

describe("TokenBucket", () => {
  it("allows a burst up to capacity then blocks", () => {
    const tb = new TokenBucket(5, 1);
    for (let i = 0; i < 5; i++) expect(tb.allow(0)).toBe(true);
    expect(tb.allow(0)).toBe(false);
  });

  it("refills over time", () => {
    const tb = new TokenBucket(2, 1); // 1 token per second
    tb.allow(0);
    tb.allow(0);
    expect(tb.allow(0)).toBe(false);
    expect(tb.allow(1000)).toBe(true);
    expect(tb.allow(1000)).toBe(false);
  });
});

describe("LeakyBucket", () => {
  it("allows up to capacity then blocks", () => {
    const lb = new LeakyBucket(3, 1);
    for (let i = 0; i < 3; i++) expect(lb.allow(0)).toBe(true);
    expect(lb.allow(0)).toBe(false);
  });

  it("leaks over time, making room", () => {
    const lb = new LeakyBucket(2, 1);
    lb.allow(0);
    lb.allow(0);
    expect(lb.allow(0)).toBe(false);
    expect(lb.allow(1000)).toBe(true);
  });
});
