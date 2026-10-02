import { useMemo, useState } from "react";
import { Ring, naiveModN, distribution } from "../lib/consistentHash";

const COLORS = ["#5b9dff", "#3ecf8e", "#e8a33d", "#ef5a6f", "#c792ea", "#4fd1c5", "#f6c177"];
const KEY_COUNT = 2000;
const keys = Array.from({ length: KEY_COUNT }, (_, i) => `key-${i}`);

export function ConsistentHashingViz() {
  const [nodes, setNodes] = useState<string[]>(["node-a", "node-b", "node-c", "node-d", "node-e"]);
  const [replicas, setReplicas] = useState(150);
  const [mode, setMode] = useState<"ring" | "naive">("ring");
  const [lastRemoval, setLastRemoval] = useState<{ node: string; movedPct: number } | null>(null);

  const colorFor = (node: string) => COLORS[nodes.indexOf(node) % COLORS.length] ?? "#888";

  const ring = useMemo(() => {
    const r = new Ring(replicas);
    for (const n of nodes) r.addNode(n);
    return r;
  }, [nodes, replicas]);

  const assign = (key: string) => (mode === "ring" ? ring.get(key) : naiveModN(nodes, key));
  const counts = useMemo(() => distribution(assign, keys), [ring, nodes, mode]);
  const maxCount = Math.max(1, ...counts.values());

  const points = useMemo(() => (mode === "ring" ? ring.visiblePoints() : []), [ring, mode]);

  function addNode() {
    const name = `node-${String.fromCharCode(97 + nodes.length)}`;
    setNodes([...nodes, name]);
  }

  function removeNode(node: string) {
    const before = new Map(keys.map((k) => [k, assign(k)]));
    const remaining = nodes.filter((n) => n !== node);
    const after = (key: string) =>
      mode === "ring"
        ? (() => {
            const r2 = new Ring(replicas);
            for (const n of remaining) r2.addNode(n);
            return r2.get(key);
          })()
        : naiveModN(remaining, key);
    let moved = 0;
    for (const k of keys) if (before.get(k) !== after(k)) moved++;
    setLastRemoval({ node, movedPct: (100 * moved) / keys.length });
    setNodes(remaining);
  }

  const cx = 180;
  const cy = 180;
  const r = 150;

  return (
    <div>
      <h2>Consistent Hashing</h2>
      <p className="desc">
        {KEY_COUNT} keys assigned to {nodes.length} nodes. Switch modes and remove a node to compare how many keys
        actually move. Same algorithm as{" "}
        <code>labs/consistent-hashing</code>, ported to TypeScript, including the point-per-owner fix for hash
        collisions between different nodes.
      </p>

      <div className="panel">
        <div className="controls">
          <label>
            mode
            <select value={mode} onChange={(e) => setMode(e.target.value as "ring" | "naive")}>
              <option value="ring">consistent hash ring</option>
              <option value="naive">naive mod-N</option>
            </select>
          </label>
          {mode === "ring" && (
            <label>
              virtual nodes per real node
              <input
                type="number"
                min={1}
                max={300}
                value={replicas}
                onChange={(e) => setReplicas(Math.max(1, Number(e.target.value) || 1))}
                style={{ width: 64 }}
              />
            </label>
          )}
          <button className="action" onClick={addNode} disabled={nodes.length >= COLORS.length}>
            add node
          </button>
        </div>

        {mode === "ring" && (
          <svg viewBox="0 0 360 360" width={280} height={280} style={{ display: "block", margin: "0 auto 12px" }}>
            <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--panel-border)" strokeWidth={1} />
            {points.map((p, i) => {
              const theta = p.angle * 2 * Math.PI - Math.PI / 2;
              const x = cx + r * Math.cos(theta);
              const y = cy + r * Math.sin(theta);
              return <circle key={i} cx={x} cy={y} r={2.5} fill={colorFor(p.node)} opacity={0.85} />;
            })}
          </svg>
        )}

        <div className="stat-row" style={{ marginBottom: 10 }}>
          {nodes.map((n) => (
            <div className="stat" key={n}>
              <span className="label" style={{ color: colorFor(n) }}>
                {n}
              </span>
              <span className="value">{counts.get(n) ?? 0}</span>
            </div>
          ))}
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {nodes.map((n) => (
            <div key={n} style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <div style={{ width: 60, fontSize: 11, fontFamily: "var(--mono)", color: "var(--text-dim)" }}>{n}</div>
              <div style={{ flex: 1, background: "var(--bg)", borderRadius: 4, overflow: "hidden", height: 14 }}>
                <div
                  style={{
                    width: `${(100 * (counts.get(n) ?? 0)) / maxCount}%`,
                    background: colorFor(n),
                    height: "100%",
                  }}
                />
              </div>
              <button className="action secondary" onClick={() => removeNode(n)} disabled={nodes.length <= 1}>
                remove
              </button>
            </div>
          ))}
        </div>

        {lastRemoval && (
          <p className="note">
            Removing <code>{lastRemoval.node}</code> moved <strong>{lastRemoval.movedPct.toFixed(1)}%</strong> of the{" "}
            {KEY_COUNT} keys ({mode === "ring" ? "consistent hash ring" : "naive mod-N"}).
            {mode === "naive"
              ? " Naive mod-N remaps almost everything because the modulus itself changed."
              : ` Close to the theoretical minimum of 1/${nodes.length + 1} for removing one of ${nodes.length + 1} nodes.`}
          </p>
        )}
      </div>
    </div>
  );
}
