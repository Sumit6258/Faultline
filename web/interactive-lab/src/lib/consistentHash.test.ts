import { describe, it, expect } from "vitest";
import { Ring, naiveModN, distribution, fnv1a } from "./consistentHash";

describe("Ring", () => {
  it("returns a node that was added", () => {
    const r = new Ring(10);
    r.addNode("a");
    r.addNode("b");
    expect(["a", "b"]).toContain(r.get("some-key"));
  });

  it("returns empty string for an empty ring", () => {
    const r = new Ring(10);
    expect(r.get("anything")).toBe("");
  });

  it("maps the same key to the same node while the ring is unchanged", () => {
    const r = new Ring(50);
    for (const n of ["a", "b", "c", "d"]) r.addNode(n);
    const first = r.get("stable-key");
    for (let i = 0; i < 100; i++) expect(r.get("stable-key")).toBe(first);
  });

  it("only moves a removed node's own keys", () => {
    const r = new Ring(100);
    for (const n of ["a", "b", "c", "d", "e"]) r.addNode(n);

    const keys = Array.from({ length: 2000 }, (_, i) => `key-${i}`);
    const before = new Map(keys.map((k) => [k, r.get(k)]));

    r.removeNode("c");

    for (const k of keys) {
      const after = r.get(k);
      if (before.get(k) !== "c") {
        expect(after).toBe(before.get(k));
      }
    }
  });

  it("survives a hash collision between two different nodes", () => {
    // Force collisions with a 4 bit hash space, far smaller than the 24
    // points three nodes at 8 replicas each will place. This is the exact
    // bug found in the Go version: a map keyed by hash alone silently lost
    // whichever node placed a point second, and returned "" for its keys
    // after the other node was removed.
    const weak = (s: string) => fnv1a(s) % 16;
    const r = new Ring(8, weak);
    for (const n of ["a", "b", "c"]) r.addNode(n);

    const keys = Array.from({ length: 300 }, (_, i) => `key-${i}`);
    for (const k of keys) {
      expect(["a", "b", "c"]).toContain(r.get(k));
    }

    r.removeNode("b");
    for (const k of keys) {
      expect(["a", "c"]).toContain(r.get(k));
    }
  });

  it("lists nodes sorted and deduplicated", () => {
    const r = new Ring(10);
    r.addNode("b");
    r.addNode("a");
    r.addNode("a");
    expect(r.nodes()).toEqual(["a", "b"]);
  });
});

describe("naiveModN vs Ring on remapping cost", () => {
  it("remaps far fewer keys than naive mod-N when a node is removed", () => {
    const nodes = ["node-a", "node-b", "node-c", "node-d", "node-e"];
    const keys = Array.from({ length: 5000 }, (_, i) => `key-${i}`);

    const beforeNaive = new Map(keys.map((k) => [k, naiveModN(nodes, k)]));
    const afterNaive = new Map(keys.map((k) => [k, naiveModN(nodes.slice(0, 4), k)]));
    const movedNaive = keys.filter((k) => beforeNaive.get(k) !== afterNaive.get(k)).length;

    const ring = new Ring(150);
    for (const n of nodes) ring.addNode(n);
    const beforeRing = new Map(keys.map((k) => [k, ring.get(k)]));
    ring.removeNode("node-e");
    const movedRing = keys.filter((k) => beforeRing.get(k) !== ring.get(k)).length;

    // Naive should move close to 80% (removing 1 of 5 changes the modulus
    // for nearly every key), the ring close to 20% (only that node's own
    // share). Loose bounds, exact percentages are measured and reported by
    // the UI itself, not asserted precisely here.
    expect(movedNaive).toBeGreaterThan(keys.length * 0.6);
    expect(movedRing).toBeLessThan(keys.length * 0.3);
  });
});

describe("distribution", () => {
  it("counts how many keys each node owns", () => {
    const r = new Ring(150);
    for (const n of ["a", "b", "c"]) r.addNode(n);
    const keys = Array.from({ length: 3000 }, (_, i) => `key-${i}`);
    const counts = distribution((k) => r.get(k), keys);
    expect([...counts.keys()].sort()).toEqual(["a", "b", "c"]);
    const total = [...counts.values()].reduce((a, b) => a + b, 0);
    expect(total).toBe(3000);
  });
});
