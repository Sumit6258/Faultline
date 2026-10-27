// Types and pure helpers for the live Kafka lag page. The numbers themselves
// come from the Go server in web/interactive-lab/server, which runs the real
// labs/kafka-messaging code. Nothing in this file simulates Kafka.

export type KafkaConfig = {
  consumers: number;
  produceRate: number;
  consumeRate: number;
  hotKeyPercent: number;
};

export type KafkaSnapshot = {
  epoch: number;
  tick: number;
  partitions: number;
  produced: number;
  consumed: number;
  lag: number[];
  totalLag: number;
  owner: number[];
  stalled: boolean[];
  config: KafkaConfig;
  producerHalted: boolean;
};

export const HISTORY_LEN = 120;

const isNumberArray = (x: unknown): x is number[] => Array.isArray(x) && x.every((v) => typeof v === "number");

/**
 * TypeScript types disappear at runtime, and this data arrives over the
 * network from a separate Go program. Checking its shape here means a renamed
 * or missing field is skipped at the edge, instead of crashing a component
 * somewhere further in. A golden file shared with the Go tests keeps the two
 * sides honest, see kafkaLive.test.ts.
 */
export function isKafkaSnapshot(x: unknown): x is KafkaSnapshot {
  if (typeof x !== "object" || x === null) return false;
  const s = x as Record<string, unknown>;
  const c = s.config as Record<string, unknown> | null | undefined;
  return (
    typeof s.epoch === "number" &&
    typeof s.tick === "number" &&
    typeof s.partitions === "number" &&
    typeof s.produced === "number" &&
    typeof s.consumed === "number" &&
    typeof s.totalLag === "number" &&
    isNumberArray(s.lag) &&
    isNumberArray(s.owner) &&
    Array.isArray(s.stalled) &&
    s.stalled.every((v) => typeof v === "boolean") &&
    typeof s.producerHalted === "boolean" &&
    typeof c === "object" &&
    c !== null &&
    typeof c.consumers === "number" &&
    typeof c.produceRate === "number" &&
    typeof c.consumeRate === "number" &&
    typeof c.hotKeyPercent === "number"
  );
}

/**
 * Folds one incoming snapshot into the history the page keeps. A client that
 * connects mid tick can receive the newest history entry a second time, and a
 * reset starts a new epoch, so duplicates and stale snapshots are dropped and
 * a newer epoch starts the history over.
 */
export function appendSnapshot(history: KafkaSnapshot[], snap: KafkaSnapshot): KafkaSnapshot[] {
  const last = history[history.length - 1];
  if (!last) return [snap];
  if (snap.epoch < last.epoch) return history;
  if (snap.epoch > last.epoch) return [snap];
  if (snap.tick <= last.tick) return history;
  return [...history, snap].slice(-HISTORY_LEN);
}

export type ConsumerView = {
  index: number;
  partitions: number[];
  idle: boolean;
  stalled: boolean;
};

/** One entry per consumer instance, including the ones that own nothing. */
export function describeConsumers(snap: KafkaSnapshot): ConsumerView[] {
  return Array.from({ length: snap.config.consumers }, (_, index) => {
    const partitions = snap.owner.flatMap((o, p) => (o === index ? [p] : []));
    return { index, partitions, idle: partitions.length === 0, stalled: snap.stalled[index] ?? false };
  });
}

/**
 * Points for an SVG polyline. The newest value sits at the right edge, so the
 * line grows leftwards into the chart as history fills up. `max` fixes the
 * scale so several lines can share one axis.
 */
export function sparklinePoints(values: number[], width: number, height: number, max: number): string {
  if (values.length === 0) return "";
  const top = Math.max(max, 1);
  const step = width / (HISTORY_LEN - 1);
  return values
    .map((v, i) => {
      const x = width - (values.length - 1 - i) * step;
      const y = height - (Math.min(v, top) / top) * height;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
}

/** How much total lag changed per tick over the last `window` ticks. */
export function lagTrend(history: KafkaSnapshot[], window = 10): number {
  if (history.length < 2) return 0;
  const last = history[history.length - 1];
  const first = history[Math.max(0, history.length - 1 - window)];
  const ticks = last.tick - first.tick;
  return ticks > 0 ? (last.totalLag - first.totalLag) / ticks : 0;
}
