import { useEffect, useState } from "react";
import { describeConsumers, KafkaConfig, lagTrend, sparklinePoints } from "../lib/kafkaLive";
import { useKafkaStream } from "../lib/useKafkaStream";
import "./KafkaLagLive.css";

const CHART_W = 600;
const CHART_H = 120;
const PARTITION_COLORS = ["#5b9dff", "#3ecf8e", "#e8a33d", "#c084fc", "#f472b6", "#22d3ee"];
const PARTITION_CHOICES = [2, 4, 6, 8, 12];

// Returns an error message, or null on success.
async function post(path: string, body: unknown): Promise<string | null> {
  try {
    const res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (res.ok) return null;
    const data = await res.json().catch(() => null);
    return data?.error ?? `request failed (${res.status})`;
  } catch {
    return "could not reach the lab server";
  }
}

export function KafkaLagLive() {
  const { status, history, latest } = useKafkaStream();
  const [pending, setPending] = useState<Partial<KafkaConfig>>({});
  const [error, setError] = useState<string | null>(null);

  // A slider shows the value the user just chose until the server reports it,
  // so dragging feels immediate and a second tab's change still shows up.
  useEffect(() => {
    if (!latest) return;
    setPending((p) => {
      const next = { ...p };
      let changed = false;
      for (const key of Object.keys(p) as (keyof KafkaConfig)[]) {
        if (p[key] === latest.config[key]) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : p;
    });
  }, [latest]);

  async function change(patch: Partial<KafkaConfig>) {
    setPending((p) => ({ ...p, ...patch }));
    setError(null);
    const err = await post("/api/kafka/config", patch);
    if (err) {
      setError(err);
      setPending((p) => {
        const next = { ...p };
        for (const key of Object.keys(patch) as (keyof KafkaConfig)[]) delete next[key];
        return next;
      });
    }
  }

  async function act(path: string, body: unknown) {
    setError(null);
    const err = await post(path, body);
    if (err) setError(err);
  }

  const header = (
    <>
      <h2>Kafka Consumer Lag (live)</h2>
      <p className="desc">
        Unlike the other pages, this one is not a TypeScript port. It streams from a small Go server that runs the
        real <code>labs/kafka-messaging</code> code, so every number below is what that lab reports. Change the
        settings and watch the group's lag respond.
      </p>
    </>
  );

  if (!latest) {
    return (
      <div>
        {header}
        <div className="panel">
          <span className={`badge kl-${status}`}>{status === "down" ? "disconnected" : status}</span>
          {status === "down" ? (
            <div className="note">
              <p>
                Can't reach the lab server. Start it in another terminal and this page connects by itself:
              </p>
              <p>
                <code>make run-lab-server</code>
              </p>
              <p>
                or <code>cd web/interactive-lab/server &amp;&amp; go run ./cmd/labserver</code>
              </p>
            </div>
          ) : (
            <p className="note">Waiting for the first tick from the lab server.</p>
          )}
        </div>
      </div>
    );
  }

  const cfg: KafkaConfig = { ...latest.config, ...pending };
  const consumers = describeConsumers(latest);
  const busy = consumers.filter((c) => !c.idle).length;
  const trend = lagTrend(history);
  const trendText = Math.abs(trend) < 0.5 ? "steady" : `${trend > 0 ? "+" : ""}${trend.toFixed(1)} per tick`;
  const trendClass = trend > 0.5 ? "bad" : trend < -0.5 ? "good" : "";
  const barMax = Math.max(50, ...latest.lag);
  const chartMax = Math.max(50, ...history.map((h) => h.totalLag));

  return (
    <div>
      {header}

      <div className="panel">
        <div className="controls">
          <span className={`badge kl-${status}`}>{status === "down" ? "disconnected" : status}</span>
          {status === "down" && <span className="note">connection lost, reconnecting</span>}
        </div>

        <div className="kl-sliders">
          <label>
            <span>consumers</span>
            <input
              type="range"
              aria-label="consumers"
              min={1}
              max={12}
              value={cfg.consumers}
              onChange={(e) => change({ consumers: Number(e.target.value) })}
            />
            <output>{cfg.consumers}</output>
          </label>
          <label>
            <span>produced per tick</span>
            <input
              type="range"
              aria-label="produced per tick"
              min={0}
              max={200}
              step={10}
              value={cfg.produceRate}
              onChange={(e) => change({ produceRate: Number(e.target.value) })}
            />
            <output>{cfg.produceRate}</output>
          </label>
          <label>
            <span>consumed per tick, per consumer</span>
            <input
              type="range"
              aria-label="consumed per tick per consumer"
              min={0}
              max={50}
              value={cfg.consumeRate}
              onChange={(e) => change({ consumeRate: Number(e.target.value) })}
            />
            <output>{cfg.consumeRate}</output>
          </label>
          <label>
            <span>share of messages with one hot key</span>
            <input
              type="range"
              aria-label="hot key percent"
              min={0}
              max={100}
              step={5}
              value={cfg.hotKeyPercent}
              onChange={(e) => change({ hotKeyPercent: Number(e.target.value) })}
            />
            <output>{cfg.hotKeyPercent}%</output>
          </label>
        </div>

        <div className="controls" style={{ marginTop: 14 }}>
          <label>
            partitions
            <select
              aria-label="partitions"
              value={latest.partitions}
              onChange={(e) => act("/api/kafka/reset", { partitions: Number(e.target.value) })}
            >
              {Array.from(new Set([...PARTITION_CHOICES, latest.partitions]))
                .sort((a, b) => a - b)
                .map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
            </select>
          </label>
          <button className="action secondary" onClick={() => act("/api/kafka/reset", {})}>
            reset
          </button>
        </div>

        {error && (
          <p className="kl-error" role="alert">
            {error}
          </p>
        )}
        {latest.producerHalted && (
          <p className="kl-error" role="status">
            The producer stopped at the lab's message limit, because the lab never trims its log. Reset to continue.
          </p>
        )}
      </div>

      <div className="panel">
        <div className="stat-row">
          <div className="stat">
            <span className="label">produced</span>
            <span className="value">{latest.produced}</span>
          </div>
          <div className="stat">
            <span className="label">consumed</span>
            <span className="value">{latest.consumed}</span>
          </div>
          <div className="stat">
            <span className="label">total lag</span>
            <span className={`value ${trendClass}`}>{latest.totalLag}</span>
          </div>
          <div className="stat">
            <span className="label">lag trend</span>
            <span className={`value ${trendClass}`}>{trendText}</span>
          </div>
          <div className="stat">
            <span className="label">consumers doing work</span>
            <span className="value">
              {busy} of {consumers.length}
            </span>
          </div>
        </div>

        <svg
          className="kl-chart"
          viewBox={`0 0 ${CHART_W} ${CHART_H}`}
          role="img"
          aria-label={`Lag over the last ${history.length} ticks. Thick line is the total, thin lines are partitions.`}
        >
          {Array.from({ length: latest.partitions }, (_, p) => (
            <polyline
              key={p}
              fill="none"
              stroke={PARTITION_COLORS[p % PARTITION_COLORS.length]}
              strokeWidth={1}
              opacity={0.7}
              points={sparklinePoints(
                history.map((h) => h.lag[p] ?? 0),
                CHART_W,
                CHART_H,
                chartMax,
              )}
            />
          ))}
          <polyline
            fill="none"
            stroke="var(--text)"
            strokeWidth={2}
            points={sparklinePoints(
              history.map((h) => h.totalLag),
              CHART_W,
              CHART_H,
              chartMax,
            )}
          />
        </svg>
        <p className="note" style={{ marginTop: 4 }}>
          total lag over the last {history.length} ticks (thick line), with each partition's lag in color. The top of
          the chart is {chartMax}.
        </p>
      </div>

      <div className="panel">
        <div className="kl-bars">
          {latest.lag.map((lag, p) => (
            <div className="kl-bar-row" key={p}>
              <span className="kl-bar-label">partition {p}</span>
              <div className="kl-bar-track">
                <div
                  className="kl-bar-fill"
                  style={{
                    width: `${(lag / barMax) * 100}%`,
                    background: PARTITION_COLORS[p % PARTITION_COLORS.length],
                  }}
                />
              </div>
              <span className="kl-bar-value">{lag}</span>
              <span className="kl-bar-owner">consumer {latest.owner[p]}</span>
            </div>
          ))}
        </div>

        <div className="kl-consumers">
          {consumers.map((c) => (
            <div key={c.index} className={`kl-consumer${c.idle ? " idle" : ""}${c.stalled ? " stalled" : ""}`}>
              <span className="kl-consumer-name">consumer {c.index}</span>
              <span className="kl-consumer-owns">
                {c.idle ? "owns no partition, idle" : `owns ${c.partitions.map((p) => `p${p}`).join(", ")}`}
              </span>
              <button
                className="action secondary"
                aria-label={`${c.stalled ? "resume" : "stall"} consumer ${c.index}`}
                onClick={() => act("/api/kafka/stall", { consumer: c.index, stalled: !c.stalled })}
              >
                {c.stalled ? "resume" : "stall"}
              </button>
            </div>
          ))}
        </div>
      </div>

      <div className="panel">
        <p className="note" style={{ marginTop: 0 }}>
          Things to try. Start with one consumer and watch lag climb, then add consumers until it stops growing. Keep
          adding them: past the partition count nothing changes, because a partition has one owner and the extras
          sit idle. Next, raise the hot key share to 100%: every message lands on one partition and more consumers
          cannot help. Last, stall one consumer and see that only its own partitions back up. The lab's README
          explains each of these in <code>labs/kafka-messaging</code>.
        </p>
      </div>
    </div>
  );
}
