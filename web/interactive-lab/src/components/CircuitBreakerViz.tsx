import { useRef, useState } from "react";
import { Breaker, State } from "../lib/circuitBreaker";

type Entry = { t: number; result: "success" | "failure" | "open" };

export function CircuitBreakerViz() {
  const [threshold, setThreshold] = useState(3);
  const [cooldownMs, setCooldownMs] = useState(3000);
  const [state, setState] = useState<State>("closed");
  const [log, setLog] = useState<Entry[]>([]);
  const startRef = useRef(Date.now());
  const breakerRef = useRef(new Breaker(threshold, cooldownMs));

  const key = `${threshold}:${cooldownMs}`;
  const keyRef = useRef(key);
  if (keyRef.current !== key) {
    keyRef.current = key;
    breakerRef.current = new Breaker(threshold, cooldownMs);
    startRef.current = Date.now();
    setState("closed");
    setLog([]);
  }

  function call(outcome: boolean) {
    const now = Date.now() - startRef.current;
    const result = breakerRef.current.call(now, () => outcome);
    setState(breakerRef.current.getState());
    setLog((prev) => [...prev, { t: now, result }].slice(-200));
  }

  return (
    <div>
      <h2>Circuit Breakers</h2>
      <p className="desc">
        The same closed, open, half-open state machine as <code>labs/circuit-breakers</code>, ported to TypeScript.
        Simulate calls succeeding or failing and watch the state transition, including the cooldown, which is only
        checked lazily, on the next actual call, not on a timer.
      </p>

      <div className="panel">
        <div className="controls">
          <label>
            failure threshold
            <input
              type="number"
              min={1}
              max={20}
              value={threshold}
              onChange={(e) => setThreshold(Math.max(1, Number(e.target.value) || 1))}
              style={{ width: 56 }}
            />
          </label>
          <label>
            cooldown (ms)
            <input
              type="number"
              min={200}
              step={200}
              max={20000}
              value={cooldownMs}
              onChange={(e) => setCooldownMs(Math.max(200, Number(e.target.value) || 200))}
              style={{ width: 72 }}
            />
          </label>
          <span className={`badge ${state}`}>{state}</span>
        </div>

        <div className="controls">
          <button className="action" onClick={() => call(true)}>
            call succeeds
          </button>
          <button className="action secondary" onClick={() => call(false)}>
            call fails
          </button>
        </div>

        <div className="log" style={{ marginTop: 14 }}>
          {log
            .slice()
            .reverse()
            .map((e, i) => (
              <div key={i} className={e.result === "success" ? "accepted" : e.result === "failure" ? "rejected" : ""} style={e.result === "open" ? { color: "var(--warn)" } : undefined}>
                t={e.t}ms{" "}
                {e.result === "open"
                  ? "rejected fast, circuit open, the dependency was never called"
                  : e.result === "failure"
                    ? "call failed"
                    : "call succeeded"}
              </div>
            ))}
          {log.length === 0 && <span className="note">simulate a few calls to see them here</span>}
        </div>

        <p className="note">
          Open the circuit by failing <code>{threshold}</code> times in a row. While open, calls are rejected without
          ever reaching the dependency, look for "the dependency was never called" in the log above. Wait past the
          cooldown, or lower it, then make one more call: that call is the half-open trial, and it alone decides
          whether the circuit closes again or reopens.
        </p>
      </div>
    </div>
  );
}
