package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/kafkalag"
)

type sseEvent struct {
	name string
	data string
}

type fixture struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	ctx context.Context
}

// newFixture starts a server whose simulation ticks every few milliseconds, so
// tests wait for ticks instead of sleeping for a fixed time.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	sess, err := kafkalag.New(4)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(sess)
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Run(ctx, 5*time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		cancel()
		srv.Close()
		ts.CloseClientConnections()
		ts.Close()
	})
	return &fixture{t: t, srv: srv, ts: ts, ctx: ctx}
}

func (f *fixture) openStream() <-chan sseEvent {
	f.t.Helper()
	ctx, cancel := context.WithCancel(f.ctx)
	f.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.ts.URL+"/api/kafka/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		f.t.Fatalf("content type %q", ct)
	}
	out := make(chan sseEvent, 256)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				select {
				case out <- sseEvent{name, strings.TrimPrefix(line, "data: ")}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// nextSnapshot returns the first snapshot on the stream that satisfies ok.
func (f *fixture) nextSnapshot(stream <-chan sseEvent, ok func(kafkalag.Snapshot) bool) kafkalag.Snapshot {
	f.t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e, open := <-stream:
			if !open {
				f.t.Fatal("stream closed while waiting for a snapshot")
			}
			if e.name != "snapshot" {
				continue
			}
			var s kafkalag.Snapshot
			if err := json.Unmarshal([]byte(e.data), &s); err != nil {
				f.t.Fatalf("bad snapshot JSON %q: %v", e.data, err)
			}
			if ok(s) {
				return s
			}
		case <-timeout:
			f.t.Fatal("timed out waiting for a matching snapshot")
		}
	}
}

func (f *fixture) post(path, body string) (int, map[string]any) {
	f.t.Helper()
	resp, err := http.Post(f.ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestStreamSendsHistoryThenOneSnapshotPerTick(t *testing.T) {
	f := newFixture(t)
	// Let a few ticks happen first so the history is not empty.
	f.nextSnapshot(f.openStream(), func(s kafkalag.Snapshot) bool { return s.Tick >= 3 })

	stream := f.openStream()
	first := <-stream
	if first.name != "history" {
		t.Fatalf("first event is %q, want history", first.name)
	}
	var history []kafkalag.Snapshot
	if err := json.Unmarshal([]byte(first.data), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("history should not be empty after several ticks")
	}

	last := history[len(history)-1].Tick
	snap := f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return s.Tick > last })
	if snap.Config != kafkalag.DefaultConfig() {
		t.Fatalf("snapshot config %+v", snap.Config)
	}
	if len(snap.Lag) != 4 || len(snap.Owner) != 4 {
		t.Fatalf("expected 4 partitions, got lag %v owner %v", snap.Lag, snap.Owner)
	}
}

func TestConfigChangesShowUpInLaterSnapshots(t *testing.T) {
	f := newFixture(t)
	stream := f.openStream()

	status, body := f.post("/api/kafka/config", `{"consumers": 4, "hotKeyPercent": 25}`)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	snap := f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return s.Config.Consumers == 4 })
	if snap.Config.HotKeyPercent != 25 || snap.Config.ProduceRate != 40 {
		t.Fatalf("partial update should leave other fields alone, got %+v", snap.Config)
	}
}

func TestBadRequestsAreRejectedWithAMessage(t *testing.T) {
	f := newFixture(t)
	cases := []struct{ name, path, body string }{
		{"consumers out of range", "/api/kafka/config", `{"consumers": 99}`},
		{"unknown field", "/api/kafka/config", `{"consumerz": 2}`},
		{"not json", "/api/kafka/config", `consumers=2`},
		{"stall without a consumer", "/api/kafka/stall", `{"stalled": true}`},
		{"stall a consumer that does not exist", "/api/kafka/stall", `{"consumer": 5, "stalled": true}`},
		{"too many partitions", "/api/kafka/reset", `{"partitions": 99}`},
	}
	for _, c := range cases {
		status, body := f.post(c.path, c.body)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", c.name, status)
		}
		if msg, _ := body["error"].(string); msg == "" {
			t.Errorf("%s: no error message in %v", c.name, body)
		}
	}

	resp, err := http.Get(f.ts.URL + "/api/kafka/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on a POST endpoint: status %d, want 405", resp.StatusCode)
	}
}

func TestStallingAConsumerShowsInSnapshots(t *testing.T) {
	f := newFixture(t)
	stream := f.openStream()
	if status, body := f.post("/api/kafka/stall", `{"consumer": 0, "stalled": true}`); status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return len(s.Stalled) == 1 && s.Stalled[0] })
}

func TestResetStartsANewEpochAndTellsOpenPagesAtOnce(t *testing.T) {
	f := newFixture(t)
	stream := f.openStream()
	f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return s.Tick >= 5 })

	if status, body := f.post("/api/kafka/reset", `{"partitions": 6}`); status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	snap := f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return s.Epoch == 1 })
	if snap.Partitions != 6 {
		t.Fatalf("partitions %d after reset to 6", snap.Partitions)
	}
	if snap.Tick > 3 {
		t.Fatalf("the first snapshot of the new epoch is at tick %d, it should be at the start", snap.Tick)
	}

	// An empty body keeps the partition count.
	resp, err := http.Post(f.ts.URL+"/api/kafka/reset", "application/json", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset with no body: status %d", resp.StatusCode)
	}
	f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return s.Epoch == 2 && s.Partitions == 6 })
}

func TestHealthAndStateEndpoints(t *testing.T) {
	f := newFixture(t)
	f.nextSnapshot(f.openStream(), func(s kafkalag.Snapshot) bool { return s.Tick >= 2 })

	resp, err := http.Get(f.ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		OK        bool     `json:"ok"`
		Scenarios []string `json:"scenarios"`
	}
	json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if !health.OK || len(health.Scenarios) != 1 || health.Scenarios[0] != "kafka-lag" {
		t.Fatalf("health %+v", health)
	}

	resp, err = http.Get(f.ts.URL + "/api/kafka/state")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Snapshot kafkalag.Snapshot   `json:"snapshot"`
		History  []kafkalag.Snapshot `json:"history"`
	}
	json.NewDecoder(resp.Body).Decode(&state)
	resp.Body.Close()
	if len(state.History) == 0 || len(state.Snapshot.Lag) != 4 {
		t.Fatalf("state %+v", state)
	}
}

func TestAFreshSessionReportsEmptyHistoryAsAnArrayNotNull(t *testing.T) {
	sess, _ := kafkalag.New(4)
	srv := New(sess)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/kafka/state", nil))
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"history":[]`)) {
		t.Fatalf("a client iterating over null would crash, body: %s", rec.Body.String())
	}
}

func TestADisconnectedBrowserIsDroppedFromTheHub(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.ts.URL+"/api/kafka/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.srv.hub.count() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("expected 1 subscriber, have %d", f.srv.hub.count())
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	resp.Body.Close()
	for f.srv.hub.count() != 0 {
		if time.Now().After(deadline.Add(2 * time.Second)) {
			t.Fatalf("subscriber leaked after the browser left, have %d", f.srv.hub.count())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestAStuckSubscriberCannotBlockThePublisher(t *testing.T) {
	h := newHub()
	stuck, unsubscribe := h.subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.publish(event{"snapshot", []byte("{}")}) // nobody ever reads from stuck
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a subscriber that stopped reading")
	}
	if len(stuck) != cap(stuck) {
		t.Fatalf("the stuck subscriber should hold a full buffer, has %d of %d", len(stuck), cap(stuck))
	}
}

func TestCloseEndsOpenStreams(t *testing.T) {
	f := newFixture(t)
	stream := f.openStream()
	<-stream // history
	f.srv.Close()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-stream:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("stream still open after Close")
		}
	}
}

// The simulation here is never started, so no tick can arrive. The only way
// an open page learns about a reset is the snapshot the reset handler
// publishes itself.
func TestResetNotifiesOpenPagesWithoutWaitingForATick(t *testing.T) {
	sess, _ := kafkalag.New(4)
	srv := New(sess)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.Close()
		ts.CloseClientConnections()
		ts.Close()
	})
	f := &fixture{t: t, srv: srv, ts: ts, ctx: context.Background()}

	stream := f.openStream()
	<-stream // history
	if status, body := f.post("/api/kafka/reset", `{}`); status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	snap := f.nextSnapshot(stream, func(s kafkalag.Snapshot) bool { return true })
	if snap.Epoch != 1 || snap.Tick != 0 {
		t.Fatalf("got epoch %d tick %d, want epoch 1 tick 0", snap.Epoch, snap.Tick)
	}
}
