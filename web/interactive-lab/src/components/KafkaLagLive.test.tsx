import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import App from "../App";
import { KafkaLagLive } from "./KafkaLagLive";
import type { KafkaSnapshot } from "../lib/kafkaLive";

// A stand-in for the browser's EventSource that the test drives by hand.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  readyState = 0; // 0 connecting, 1 open, 2 closed for good
  private listeners = new Map<string, ((e: MessageEvent) => void)[]>();

  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  emitRaw(type: string, raw: string) {
    for (const fn of this.listeners.get(type) ?? []) fn({ data: raw } as MessageEvent);
  }
  emit(type: string, data: unknown) {
    this.emitRaw(type, JSON.stringify(data));
  }
}

const stream = () => FakeEventSource.instances[FakeEventSource.instances.length - 1];

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

const ok = { ok: true, status: 200, json: async () => ({}) };
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  FakeEventSource.instances = [];
  fetchMock = vi.fn(async () => ok);
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function showData(s: KafkaSnapshot = snap()) {
  render(<KafkaLagLive />);
  act(() => stream().emit("history", [s]));
}

describe("KafkaLagLive connection states", () => {
  it("says it is connecting until the server answers", () => {
    render(<KafkaLagLive />);
    expect(screen.getByText("connecting")).toBeTruthy();
    expect(screen.getByText(/Waiting for the first tick/)).toBeTruthy();
  });

  it("explains how to start the server when it cannot connect", () => {
    render(<KafkaLagLive />);
    act(() => stream().onerror?.());
    expect(screen.getByText(/Can't reach the lab server/)).toBeTruthy();
    expect(screen.getByText("make run-lab-server")).toBeTruthy();
  });

  it("says the same when the browser has no EventSource at all", () => {
    vi.stubGlobal("EventSource", undefined);
    render(<KafkaLagLive />);
    expect(screen.getByText(/Can't reach the lab server/)).toBeTruthy();
  });

  it("keeps the last numbers on screen and says it is reconnecting if the connection drops", () => {
    showData(snap({ totalLag: 4242, lag: [1, 2, 3, 4] }));
    act(() => stream().onerror?.());
    expect(screen.getByText("4242")).toBeTruthy();
    expect(screen.getByText(/reconnecting/)).toBeTruthy();
  });

  describe("when the browser gives up on a connection", () => {
    // A browser only retries a dropped connection. An error status, such as the
    // 502 the dev proxy sends while the Go server is down, closes the
    // EventSource for good, so the page has to open a new one itself.
    afterEach(() => {
      vi.useRealTimers();
    });

    it("opens a new connection after a pause, and shows data from it", () => {
      vi.useFakeTimers();
      render(<KafkaLagLive />);
      const first = stream();
      first.readyState = 2;
      act(() => first.onerror?.());
      expect(FakeEventSource.instances).toHaveLength(1);

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(FakeEventSource.instances).toHaveLength(2);

      act(() => stream().emit("history", [snap({ totalLag: 4321, lag: [1, 2, 3, 4] })]));
      expect(screen.getByText("4321")).toBeTruthy();
    });

    it("keeps trying for as long as the server stays away", () => {
      vi.useFakeTimers();
      render(<KafkaLagLive />);
      for (let attempt = 1; attempt <= 3; attempt++) {
        const es = stream();
        es.readyState = 2;
        act(() => es.onerror?.());
        act(() => {
          vi.advanceTimersByTime(2000);
        });
        expect(FakeEventSource.instances).toHaveLength(attempt + 1);
      }
    });

    it("leaves a connection alone while the browser is retrying it by itself", () => {
      vi.useFakeTimers();
      render(<KafkaLagLive />);
      act(() => stream().onerror?.()); // readyState is still 0, connecting
      act(() => {
        vi.advanceTimersByTime(20000);
      });
      expect(FakeEventSource.instances).toHaveLength(1);
    });

    it("stops retrying once the page is left", () => {
      vi.useFakeTimers();
      const { unmount } = render(<KafkaLagLive />);
      const es = stream();
      es.readyState = 2;
      act(() => es.onerror?.());
      unmount();
      act(() => {
        vi.advanceTimersByTime(20000);
      });
      expect(FakeEventSource.instances).toHaveLength(1);
    });
  });

  it("closes the stream when the page is left", () => {
    const { unmount } = render(<KafkaLagLive />);
    const es = stream();
    unmount();
    expect(es.closed).toBe(true);
  });

  it("skips a malformed event instead of failing", () => {
    render(<KafkaLagLive />);
    act(() => stream().emitRaw("snapshot", "{not json"));
    expect(screen.getByText(/Waiting for the first tick/)).toBeTruthy();
  });

  it("skips an event that is valid JSON but not a snapshot, in either event type", () => {
    render(<KafkaLagLive />);
    act(() => stream().emit("snapshot", { hello: "world" }));
    act(() => stream().emit("history", [{ tick: 1 }]));
    act(() => stream().emit("history", { not: "a list" }));
    expect(screen.getByText(/Waiting for the first tick/)).toBeTruthy();
  });
});

describe("KafkaLagLive display", () => {
  it("shows per partition lag, which consumer owns each partition, and the totals", () => {
    showData(snap({ lag: [11, 12, 13, 14], totalLag: 5050, owner: [0, 1, 0, 1], produced: 777, consumed: 888, stalled: [false, false], config: { consumers: 2, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 } }));
    expect(screen.getByText("partition 2")).toBeTruthy();
    expect(screen.getByText("13")).toBeTruthy();
    expect(screen.getAllByText("consumer 1").length).toBeGreaterThan(0);
    expect(screen.getByText("5050")).toBeTruthy();
    expect(screen.getByText("777")).toBeTruthy();
    expect(screen.getByText("888")).toBeTruthy();
  });

  it("marks consumers beyond the partition count as idle", () => {
    showData(
      snap({
        owner: [0, 1, 2, 3],
        stalled: Array(8).fill(false),
        config: { consumers: 8, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 },
      }),
    );
    expect(screen.getAllByText("owns no partition, idle")).toHaveLength(4);
    expect(screen.getByText("4 of 8")).toBeTruthy();
  });

  it("warns when the producer has stopped at the lab's message limit", () => {
    showData(snap({ producerHalted: true }));
    expect(screen.getByText(/producer stopped/)).toBeTruthy();
  });

  it("drops a duplicate tick and starts over on a new epoch", () => {
    showData(snap({ tick: 5, totalLag: 31337, lag: [1, 2, 3, 4] }));
    act(() => stream().emit("snapshot", snap({ tick: 5, totalLag: 999, lag: [1, 2, 3, 4] })));
    expect(screen.queryByText("999")).toBeNull();
    expect(screen.getByText("31337")).toBeTruthy();

    act(() => stream().emit("snapshot", snap({ epoch: 1, tick: 0, totalLag: 7, lag: [1, 2, 3, 1] })));
    expect(screen.queryByText("31337")).toBeNull();
    expect(screen.getByText("7")).toBeTruthy();
  });
});

describe("KafkaLagLive controls", () => {
  it("a slider posts only the setting that changed and shows the chosen value at once", async () => {
    showData();
    const slider = screen.getByRole("slider", { name: "consumers" }) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(slider, { target: { value: "6" } });
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/kafka/config",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ consumers: 6 }) }),
    );
    expect(slider.value).toBe("6");
  });

  it("follows the server again once it reports the value, including a change from another tab", async () => {
    showData();
    const slider = screen.getByRole("slider", { name: "consumers" }) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(slider, { target: { value: "6" } });
    });
    act(() => stream().emit("snapshot", snap({ tick: 2, config: { consumers: 6, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 }, stalled: Array(6).fill(false) })));
    expect(slider.value).toBe("6");

    act(() => stream().emit("snapshot", snap({ tick: 3, config: { consumers: 3, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 }, stalled: Array(3).fill(false) })));
    expect(slider.value).toBe("3");
  });

  it("puts the slider back and shows the server's message when a change is rejected", async () => {
    fetchMock.mockResolvedValueOnce({ ok: false, status: 400, json: async () => ({ error: "consumers must be between 1 and 12, got 99" }) });
    showData();
    const slider = screen.getByRole("slider", { name: "consumers" }) as HTMLInputElement;
    await act(async () => {
      fireEvent.change(slider, { target: { value: "12" } });
    });
    expect(screen.getByRole("alert").textContent).toContain("consumers must be between 1 and 12");
    expect(slider.value).toBe("1");
  });

  it("reports an unreachable server when a request fails outright", async () => {
    fetchMock.mockRejectedValueOnce(new Error("network down"));
    showData();
    await act(async () => {
      fireEvent.change(screen.getByRole("slider", { name: "hot key percent" }), { target: { value: "50" } });
    });
    expect(screen.getByRole("alert").textContent).toContain("could not reach the lab server");
  });

  it("stalls and resumes one consumer", async () => {
    const two = { stalled: [false, false], owner: [0, 1, 0, 1], config: { consumers: 2, produceRate: 40, consumeRate: 10, hotKeyPercent: 0 } };
    showData(snap(two));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "stall consumer 1" }));
    });
    expect(fetchMock).toHaveBeenLastCalledWith("/api/kafka/stall", expect.objectContaining({ body: JSON.stringify({ consumer: 1, stalled: true }) }));

    act(() => stream().emit("snapshot", snap({ ...two, tick: 2, stalled: [false, true] })));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "resume consumer 1" }));
    });
    expect(fetchMock).toHaveBeenLastCalledWith("/api/kafka/stall", expect.objectContaining({ body: JSON.stringify({ consumer: 1, stalled: false }) }));
  });

  it("resets, and changes the partition count through a reset", async () => {
    showData();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "reset" }));
    });
    expect(fetchMock).toHaveBeenLastCalledWith("/api/kafka/reset", expect.objectContaining({ body: "{}" }));

    await act(async () => {
      fireEvent.change(screen.getByRole("combobox", { name: "partitions" }), { target: { value: "8" } });
    });
    expect(fetchMock).toHaveBeenLastCalledWith("/api/kafka/reset", expect.objectContaining({ body: JSON.stringify({ partitions: 8 }) }));
  });
});

describe("the page inside the app", () => {
  it("is reachable from the sidebar", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "Kafka Lag (live)" }));
    expect(screen.getByRole("heading", { name: "Kafka Consumer Lag (live)" })).toBeTruthy();
  });
});
