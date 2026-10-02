import { describe, it, expect } from "vitest";
import { Breaker } from "./circuitBreaker";

describe("Breaker", () => {
  it("stays closed on repeated success", () => {
    const b = new Breaker(3, 1000);
    for (let i = 0; i < 5; i++) expect(b.call(0, () => true)).toBe("success");
    expect(b.getState()).toBe("closed");
  });

  it("opens after the threshold of consecutive failures", () => {
    const b = new Breaker(3, 1000);
    for (let i = 0; i < 3; i++) b.call(0, () => false);
    expect(b.getState()).toBe("open");
  });

  it("rejects without calling fn while open", () => {
    const b = new Breaker(1, 100000);
    b.call(0, () => false); // opens it
    let called = false;
    const result = b.call(1, () => {
      called = true;
      return true;
    });
    expect(result).toBe("open");
    expect(called).toBe(false);
  });

  it("stays open before the cooldown elapses", () => {
    const b = new Breaker(1, 1000);
    b.call(0, () => false);
    expect(b.call(500, () => true)).toBe("open");
  });

  it("a successful half-open trial closes the circuit", () => {
    const b = new Breaker(1, 1000);
    b.call(0, () => false);
    expect(b.call(2000, () => true)).toBe("success");
    expect(b.getState()).toBe("closed");
  });

  it("a failed half-open trial reopens the circuit", () => {
    const b = new Breaker(1, 1000);
    b.call(0, () => false);
    b.call(2000, () => false);
    expect(b.getState()).toBe("open");
  });

  it("a success resets the consecutive failure count before threshold", () => {
    const b = new Breaker(3, 1000);
    b.call(0, () => false);
    b.call(0, () => false);
    b.call(0, () => true); // resets the streak
    b.call(0, () => false);
    b.call(0, () => false);
    expect(b.getState()).toBe("closed");
  });

  it("only one half-open trial runs at a time, a second concurrent call is rejected", () => {
    const b = new Breaker(1, 1000);
    b.call(0, () => false); // opens it
    let innerCalled = false;
    // fn for the first half-open trial calls back into the breaker before
    // returning, simulating a second request arriving while the trial is
    // still in flight.
    const outer = b.call(2000, () => {
      const inner = b.call(2000, () => true);
      innerCalled = inner !== "open";
      return true;
    });
    expect(outer).toBe("success");
    expect(innerCalled).toBe(false);
  });

  it("runs the full closed, open, half-open, closed cycle", () => {
    const b = new Breaker(3, 1000);
    let call = 0;
    const dependency = () => {
      call++;
      return !(call >= 3 && call <= 5); // fails on calls 3 through 5
    };

    const states: string[] = [];
    for (let i = 0; i < 6; i++) states.push(b.call(i, dependency));
    expect(states).toEqual(["success", "success", "failure", "failure", "failure", "open"]);
    expect(b.getState()).toBe("open");

    expect(b.call(1500, dependency)).toBe("success"); // cooldown elapsed, trial succeeds (call 6, not in [3,5])
    expect(b.getState()).toBe("closed");
  });
});
