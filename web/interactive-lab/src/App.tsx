import { useState } from "react";
import { ConsistentHashingViz } from "./components/ConsistentHashingViz";
import { RateLimiterViz } from "./components/RateLimiterViz";
import { CircuitBreakerViz } from "./components/CircuitBreakerViz";

const PAGES = {
  "consistent-hashing": { label: "Consistent Hashing", component: ConsistentHashingViz },
  "rate-limiting": { label: "Rate Limiting", component: RateLimiterViz },
  "circuit-breakers": { label: "Circuit Breakers", component: CircuitBreakerViz },
} as const;

type PageKey = keyof typeof PAGES;

export default function App() {
  const [page, setPage] = useState<PageKey>("consistent-hashing");
  const Active = PAGES[page].component;

  return (
    <div className="app">
      <aside className="sidebar">
        <h1>Faultline</h1>
        <p className="subtitle">Interactive lab</p>
        <nav>
          {(Object.keys(PAGES) as PageKey[]).map((k) => (
            <button key={k} className={k === page ? "active" : ""} onClick={() => setPage(k)}>
              {PAGES[k].label}
            </button>
          ))}
        </nav>
      </aside>
      <main className="main">
        <Active />
      </main>
    </div>
  );
}
