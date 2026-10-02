// The same closed, open, half-open state machine as labs/circuit-breakers,
// reimplemented in TypeScript, including the halfOpenInFlight guard that
// stops a flood of concurrent callers from all becoming trial calls the
// instant the cooldown elapses.

export type State = "closed" | "open" | "half-open";

export class Breaker {
  private state: State = "closed";
  private consecutiveFails = 0;
  private openedAt = -Infinity;
  private halfOpenInFlight = false;

  constructor(private failureThreshold: number, private cooldownMs: number) {}

  getState(): State {
    return this.state;
  }

  // call mirrors the Go version's signature: fn is invoked synchronously,
  // now is the caller-supplied clock. Returns "open" without invoking fn at
  // all when the circuit is open and the cooldown hasn't elapsed.
  call(now: number, fn: () => boolean): "success" | "failure" | "open" {
    if (this.state === "open") {
      if (now - this.openedAt < this.cooldownMs) return "open";
      this.state = "half-open";
      this.halfOpenInFlight = true;
    } else if (this.state === "half-open") {
      if (this.halfOpenInFlight) return "open";
      this.halfOpenInFlight = true;
    }
    const wasHalfOpen = this.state === "half-open";

    const ok = fn();
    this.halfOpenInFlight = false;

    if (!ok) {
      this.consecutiveFails++;
      if (wasHalfOpen || this.consecutiveFails >= this.failureThreshold) {
        this.state = "open";
        this.openedAt = now;
      }
      return "failure";
    }

    this.consecutiveFails = 0;
    this.state = "closed";
    return "success";
  }
}
