// A consistent hash ring, the same algorithm as labs/consistent-hashing,
// reimplemented here since a browser can't import Go. Virtual nodes per
// real node, points ordered by (hash, node) so a hash collision between
// two different nodes can never silently drop one of them, the same fix
// applied to the Go version after a real bug was found there.

export type Point = { hash: number; node: string };

// A small, fast string hash (FNV-1a, 32 bit). Deterministic and
// well-distributed, not cryptographic, which is exactly what a hash ring
// needs and nothing more.
export function fnv1a(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

export class Ring {
  private replicas: number;
  private points: Point[] = [];
  private nodeSet = new Set<string>();
  private hashFn: (s: string) => number;

  constructor(replicas: number, hashFn: (s: string) => number = fnv1a) {
    this.replicas = replicas;
    this.hashFn = hashFn;
  }

  addNode(node: string): void {
    if (this.nodeSet.has(node)) return;
    this.nodeSet.add(node);
    for (let i = 0; i < this.replicas; i++) {
      this.points.push({ hash: this.hashFn(`${node}#${i}`), node });
    }
    this.points.sort((a, b) => (a.hash !== b.hash ? a.hash - b.hash : a.node < b.node ? -1 : 1));
  }

  removeNode(node: string): void {
    if (!this.nodeSet.has(node)) return;
    this.nodeSet.delete(node);
    this.points = this.points.filter((p) => p.node !== node);
  }

  get(key: string): string {
    if (this.points.length === 0) return "";
    const h = this.hashFn(key);
    let lo = 0;
    let hi = this.points.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.points[mid].hash < h) lo = mid + 1;
      else hi = mid;
    }
    if (lo === this.points.length) lo = 0;
    return this.points[lo].node;
  }

  nodes(): string[] {
    return [...this.nodeSet].sort();
  }

  // pointsForNode exposes each virtual point's position, 0 to 1 around the
  // ring, purely for drawing. Not used by get() or any real logic.
  visiblePoints(): { node: string; angle: number }[] {
    return this.points.map((p) => ({ node: p.node, angle: p.hash / 0xffffffff }));
  }
}

export function naiveModN(nodes: string[], key: string, hashFn: (s: string) => number = fnv1a): string {
  if (nodes.length === 0) return "";
  const sorted = [...nodes].sort();
  return sorted[hashFn(key) % sorted.length];
}

export function distribution(assign: (key: string) => string, keys: string[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const k of keys) {
    const owner = assign(k);
    counts.set(owner, (counts.get(owner) ?? 0) + 1);
  }
  return counts;
}
