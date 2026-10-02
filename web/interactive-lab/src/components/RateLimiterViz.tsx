import { useMemo, useRef, useState } from "react";
import { createLimiter, AlgorithmName, Limiter } from "../lib/rateLimiters";

const ALGORITHMS: { value: AlgorithmName; label: string }[] = [
  { value: "fixed-window", label: "Fixed window" },
  { value: "sliding-window", label: "Sliding window counter" },
  { value: "token-bucket", label: "Token bucket" },
  { value: "leaky-bucket", label: "Leaky bucket (meter)" },
];

type Entry = { t: number; ok: boolean };

export function RateLimiterViz() {
  const [algo, setAlgo] = useState<AlgorithmName>("token-bucket");
  const [limit, setLimit] = useState(10);
  const [windowMs, setWindowMs] = useState(1000);
  const [log, setLog] = useState<Entry[]>([]);
  const startRef = useRef(Date.now());
  const limiterRef = useRef<Limiter>(createLimiter(algo, limit, windowMs));

  const key = `${algo}:${limit}:${windowMs}`;
  const limiterKeyRef = useRef(key);
  if (limiterKeyRef.current !== key) {
    limiterKeyRef.current = key;
    limiterRef.current = createLimiter(algo, limit, windowMs);
    startRef.current = Date.now();
    setLog([]);
  }

  function fire(count: number) {
    const entries: Entry[] = [];
    const now = Date.now() - startRef.current;
    for (let i = 0; i < count; i++) {
      entries.push({ t: now, ok: limiterRef.current.allow(now) });
    }
    setLog((prev) => [...prev, ...entries].slice(-300));
  }

  function reset() {
    limiterRef.current = createLimiter(algo, limit, windowMs);
    startRef.current = Date.now();
    setLog([]);
  }

  const accepted = log.filter((e) => e.ok).length;
  const rejected = log.length - accepted;
  const last40 = log.slice(-40);

  const description = useMemo(() => {
    switch (algo) {
      case "fixed-window":
        return `Allows up to ${limit} requests per ${windowMs}ms window, counted from whichever request starts the window. A burst right at the edge of one window and another right after can let through close to 2x the limit.`;
      case "sliding-window":
        return `Approximates a true sliding window by weighting the previous ${windowMs}ms window's count against how far into the current one you are. Smooths most of the boundary burst fixed window allows.`;
      case "token-bucket":
        return `Starts with ${limit} tokens, refills at ${limit}/${windowMs}ms. Allows a burst up to capacity, then enforces the steady rate.`;
      case "leaky-bucket":
        return `Capacity ${limit}, drains at ${limit}/${windowMs}ms. Mathematically the same accept/reject decision as a token bucket of the same size, see labs/rate-limiting's equivalence test.`;
    }
  }, [algo, limit, windowMs]);

  return (
    <div>
      <h2>Rate Limiting</h2>
      <p className="desc">
        The same four algorithms as <code>labs/rate-limiting</code>, ported to TypeScript. Fire requests and watch
        which ones get through.
      </p>

      <div className="panel">
        <div className="controls">
          <label>
            algorithm
            <select value={algo} onChange={(e) => setAlgo(e.target.value as AlgorithmName)}>
              {ALGORITHMS.map((a) => (
                <option key={a.value} value={a.value}>
                  {a.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            limit
            <input type="number" min={1} max={100} value={limit} onChange={(e) => setLimit(Math.max(1, Number(e.target.value) || 1))} style={{ width: 56 }} />
          </label>
          <label>
            window/interval (ms)
            <input
              type="number"
              min={100}
              step={100}
              max={10000}
              value={windowMs}
              onChange={(e) => setWindowMs(Math.max(100, Number(e.target.value) || 100))}
              style={{ width: 72 }}
            />
          </label>
        </div>

        <p className="note" style={{ marginTop: 0, marginBottom: 14 }}>
          {description}
        </p>

        <div className="controls">
          <button className="action" onClick={() => fire(1)}>
            send 1 request
          </button>
          <button className="action" onClick={() => fire(10)}>
            send burst of 10
          </button>
          <button className="action" onClick={() => fire(50)}>
            send burst of 50
          </button>
          <button className="action secondary" onClick={reset}>
            reset
          </button>
        </div>

        <div className="stat-row" style={{ margin: "14px 0" }}>
          <div className="stat">
            <span className="label">accepted</span>
            <span className="value good">{accepted}</span>
          </div>
          <div className="stat">
            <span className="label">rejected</span>
            <span className="value bad">{rejected}</span>
          </div>
          <div className="stat">
            <span className="label">total</span>
            <span className="value">{log.length}</span>
          </div>
        </div>

        <div style={{ display: "flex", gap: 2, height: 28, alignItems: "flex-end" }}>
          {last40.map((e, i) => (
            <div
              key={i}
              title={`t=${e.t}ms ${e.ok ? "accepted" : "rejected"}`}
              style={{
                flex: 1,
                height: "100%",
                background: e.ok ? "var(--good)" : "var(--bad)",
                opacity: 0.85,
                borderRadius: 2,
              }}
            />
          ))}
          {last40.length === 0 && <span className="note">send some requests to see them here</span>}
        </div>

        <div className="log">
          {log
            .slice(-15)
            .reverse()
            .map((e, i) => (
              <div key={i} className={e.ok ? "accepted" : "rejected"}>
                t={e.t}ms {e.ok ? "accepted" : "rejected"}
              </div>
            ))}
        </div>
      </div>
    </div>
  );
}
