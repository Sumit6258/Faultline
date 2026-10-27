import { describe, it, expect } from "vitest";
import golden from "../../server/testdata/snapshot.golden.json";
import {
  appendSnapshot,
  describeConsumers,
  HISTORY_LEN,
  isKafkaSnapshot,
  KafkaSnapshot,
  lagTrend,
  sparklinePoints,
} from "./kafkaLive";

function snap(over: Partial<KafkaSnapshot> = {}): KafkaSnapshot {
  return {
    epoch: 0,
    tick: 1,
    partitions: 4,
    produced: 40,
    consumed: 10,
    lag: [8, 7, 8, 7],
    totalLag: 30,
    owner: [0, 0, 0, 0],
    stalled: [false],
    config: { consumers: 1, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 },
    producerHalted: false,
    ...over,
  };
}

describe("appendSnapshot", () => {
  it("starts a history from the first snapshot", () => {
    expect(appendSnapshot([], snap({ tick: 1 }))).toHaveLength(1);
  });

  it("appends newer ticks in order", () => {
    const h = appendSnapshot(appendSnapshot([], snap({ tick: 1 })), snap({ tick: 2 }));
    expect(h.map((s) => s.tick)).toEqual([1, 2]);
  });

  it("drops a tick it already has, which a client that connects mid tick can receive twice", () => {
    const h = appendSnapshot([snap({ tick: 5 })], snap({ tick: 5, totalLag: 999 }));
    expect(h).toHaveLength(1);
    expect(h[0].totalLag).toBe(30);
  });

  it("drops an older tick", () => {
    expect(appendSnapshot([snap({ tick: 5 })], snap({ tick: 3 }))).toHaveLength(1);
  });

  it("starts over when a reset begins a new epoch, even though its tick number is lower", () => {
    const h = appendSnapshot([snap({ epoch: 0, tick: 50 })], snap({ epoch: 1, tick: 0 }));
    expect(h).toHaveLength(1);
    expect(h[0].epoch).toBe(1);
  });

  it("ignores a late snapshot from an earlier epoch", () => {
    const h = appendSnapshot([snap({ epoch: 2, tick: 1 })], snap({ epoch: 1, tick: 99 }));
    expect(h[0].epoch).toBe(2);
  });

  it("keeps at most HISTORY_LEN entries, newest last", () => {
    let h: KafkaSnapshot[] = [];
    for (let t = 1; t <= HISTORY_LEN + 30; t++) h = appendSnapshot(h, snap({ tick: t }));
    expect(h).toHaveLength(HISTORY_LEN);
    expect(h[h.length - 1].tick).toBe(HISTORY_LEN + 30);
    expect(h[0].tick).toBe(31);
  });
});

describe("describeConsumers", () => {
  it("lists every consumer, marking the ones that own no partition as idle", () => {
    const view = describeConsumers(
      snap({
        owner: [0, 1, 2, 3],
        stalled: Array(8).fill(false),
        config: { consumers: 8, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 },
      }),
    );
    expect(view).toHaveLength(8);
    expect(view.filter((c) => c.idle).map((c) => c.index)).toEqual([4, 5, 6, 7]);
    expect(view[1].partitions).toEqual([1]);
  });

  it("gives a consumer every partition it owns", () => {
    const view = describeConsumers(snap({ owner: [0, 1, 0, 1], stalled: [false, false], config: { consumers: 2, produceRate: 1, consumeRate: 1, hotKeyPercent: 0 } }));
    expect(view[0].partitions).toEqual([0, 2]);
    expect(view[1].partitions).toEqual([1, 3]);
  });

  it("reports which consumers are stalled", () => {
    const view = describeConsumers(snap({ owner: [0, 1], stalled: [false, true], config: { consumers: 2, produceRate: 1, consumeRate: 1, hotKeyPercent: 0 } }));
    expect(view.map((c) => c.stalled)).toEqual([false, true]);
  });
});

describe("sparklinePoints", () => {
  it("is empty for no data", () => {
    expect(sparklinePoints([], 600, 100, 50)).toBe("");
  });

  it("puts the newest value at the right edge and scales to the given maximum", () => {
    const pts = sparklinePoints([0, 100], 600, 100, 100).split(" ");
    expect(pts).toHaveLength(2);
    expect(pts[1]).toBe("600.0,0.0");
    expect(pts[0].split(",")[1]).toBe("100.0");
  });

  it("never draws outside the chart when a value exceeds the maximum", () => {
    const y = Number(sparklinePoints([500], 600, 100, 100).split(",")[1]);
    expect(y).toBe(0);
  });

  it("survives a maximum of zero", () => {
    expect(sparklinePoints([0], 600, 100, 0)).toBe("600.0,100.0");
  });
});

describe("lagTrend", () => {
  const at = (tick: number, totalLag: number) => snap({ tick, totalLag });

  it("is zero with too little history", () => {
    expect(lagTrend([])).toBe(0);
    expect(lagTrend([at(1, 5)])).toBe(0);
  });

  it("measures change per tick over the window", () => {
    const h = Array.from({ length: 20 }, (_, i) => at(i + 1, (i + 1) * 30));
    expect(lagTrend(h, 10)).toBe(30);
  });

  it("is negative while the group drains", () => {
    const h = [at(1, 100), at(2, 90), at(3, 80)];
    expect(lagTrend(h)).toBe(-10);
  });
});

// server/testdata/snapshot.golden.json is generated from a real session by the
// Go tests, which fail if the server's JSON ever stops matching it. Reading it
// here ties this page to what the server actually sends.
describe("the JSON the Go server sends", () => {
  it("passes the page's own validator", () => {
    expect(isKafkaSnapshot(golden)).toBe(true);
  });

  it("means what the page thinks it means", () => {
    const view = describeConsumers(golden as KafkaSnapshot);
    expect(view.map((c) => c.partitions)).toEqual([[0, 2], [1, 3]]);
    expect(view.map((c) => c.stalled)).toEqual([false, true]);
    expect(golden.totalLag).toBe(golden.lag.reduce((a, b) => a + b, 0));
  });
});

describe("isKafkaSnapshot", () => {
  it("rejects things that are not snapshots", () => {
    for (const bad of [null, undefined, 42, "snapshot", [], {}]) expect(isKafkaSnapshot(bad)).toBe(false);
  });

  it("rejects a snapshot with a renamed field", () => {
    const { totalLag, ...rest } = golden;
    expect(isKafkaSnapshot({ ...rest, total_lag: totalLag })).toBe(false);
  });

  it("rejects a snapshot with the wrong type in a field or in a list", () => {
    expect(isKafkaSnapshot({ ...golden, tick: "3" })).toBe(false);
    expect(isKafkaSnapshot({ ...golden, lag: [0, "23", 42, 25] })).toBe(false);
    expect(isKafkaSnapshot({ ...golden, stalled: [false, "yes"] })).toBe(false);
  });

  it("rejects a snapshot with a missing or incomplete config", () => {
    const { config: _config, ...noConfig } = golden;
    expect(isKafkaSnapshot(noConfig)).toBe(false);
    expect(isKafkaSnapshot({ ...golden, config: { consumers: 2 } })).toBe(false);
    expect(isKafkaSnapshot({ ...golden, config: null })).toBe(false);
  });
});
