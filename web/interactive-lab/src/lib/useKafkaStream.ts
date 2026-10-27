import { useEffect, useState } from "react";
import { appendSnapshot, HISTORY_LEN, isKafkaSnapshot, KafkaSnapshot } from "./kafkaLive";

export type StreamStatus = "connecting" | "live" | "down";

const RETRY_MS = 2000;
const CLOSED = 2; // EventSource.readyState once the browser has given up on a connection

function parse(raw: string): unknown {
  try {
    return JSON.parse(raw);
  } catch {
    return null; // a malformed event is skipped, it must not take the page down
  }
}

/**
 * Subscribes to the lab server's event stream and keeps it alive.
 *
 * A browser retries a dropped connection by itself, but only that. If the
 * server answers with an error status, which is what the dev proxy does while
 * the Go server is down (502), the browser closes the EventSource for good and
 * never tries again. So when it reports being closed, this opens a new one
 * after a short pause. The server sends the recent history on every
 * connection, so the page picks up where the run is without any extra work.
 */
export function useKafkaStream(url = "/api/kafka/stream") {
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const [history, setHistory] = useState<KafkaSnapshot[]>([]);

  useEffect(() => {
    if (typeof EventSource === "undefined") {
      setStatus("down");
      return;
    }
    let source: EventSource | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;

    const connect = () => {
      const es = new EventSource(url);
      source = es;
      es.onopen = () => setStatus("live");
      es.onerror = () => {
        setStatus("down");
        if (es.readyState === CLOSED && !stopped) {
          retry = setTimeout(connect, RETRY_MS);
        }
      };
      es.addEventListener("history", (e) => {
        const list = parse((e as MessageEvent).data);
        if (!Array.isArray(list) || !list.every(isKafkaSnapshot)) return;
        setHistory(list.slice(-HISTORY_LEN));
        setStatus("live");
      });
      es.addEventListener("snapshot", (e) => {
        const snap = parse((e as MessageEvent).data);
        if (!isKafkaSnapshot(snap)) return;
        setHistory((h) => appendSnapshot(h, snap));
        setStatus("live");
      });
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(retry);
      source?.close();
    };
  }, [url]);

  return { status, history, latest: history[history.length - 1] as KafkaSnapshot | undefined };
}
