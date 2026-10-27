// Package httpapi serves a kafkalag.Session to the browser: JSON endpoints to
// steer it and a server-sent event stream that carries every tick.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/kafkalag"
)

type event struct {
	name string
	data []byte
}

// hub fans each tick out to every connected browser. A subscriber whose
// buffer is full simply misses ticks, so one stuck tab can never slow the
// simulation or the other tabs.
type hub struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

func newHub() *hub { return &hub{subs: map[chan event]struct{}{}} }

func (h *hub) subscribe() (chan event, func()) {
	ch := make(chan event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(e event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

type Server struct {
	sess      *kafkalag.Session
	hub       *hub
	heartbeat time.Duration
	done      chan struct{}
	closeOnce sync.Once
}

func New(sess *kafkalag.Session) *Server {
	return &Server{sess: sess, hub: newHub(), heartbeat: 15 * time.Second, done: make(chan struct{})}
}

// Close ends every open event stream. Call it before shutting the HTTP
// server down, or shutdown waits on streams that never finish by themselves.
func (s *Server) Close() { s.closeOnce.Do(func() { close(s.done) }) }

// Run advances the simulation one tick at a time and publishes each result
// until ctx is cancelled.
func (s *Server) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.hub.publish(event{"snapshot", mustJSON(s.sess.Step())})
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/kafka/state", s.state)
	mux.HandleFunc("GET /api/kafka/stream", s.stream)
	mux.HandleFunc("POST /api/kafka/config", s.config)
	mux.HandleFunc("POST /api/kafka/stall", s.stall)
	mux.HandleFunc("POST /api/kafka/reset", s.reset)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scenarios": []string{"kafka-lag"}})
}

func (s *Server) state(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": s.sess.Snapshot(), "history": historyOrEmpty(s.sess)})
}

func historyOrEmpty(sess *kafkalag.Session) []kafkalag.Snapshot {
	h := sess.History()
	if h == nil {
		return []kafkalag.Snapshot{}
	}
	return h
}

// stream sends the recent history once, then one event per tick. A client
// that connects mid tick may see the newest history entry again as its first
// snapshot, so clients drop any snapshot at or below the tick they already have.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this connection")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	// Subscribe before reading the history so no tick falls between the two.
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()

	if err := writeSSE(w, "history", mustJSON(historyOrEmpty(s.sess))); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(s.heartbeat)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case e := <-events:
			if err := writeSSE(w, e.name, e.data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	var patch kafkalag.ConfigPatch
	if !decode(w, r, &patch, false) {
		return
	}
	cfg, err := s.sess.SetConfig(patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg})
}

func (s *Server) stall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Consumer *int  `json:"consumer"`
		Stalled  *bool `json:"stalled"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Consumer == nil || body.Stalled == nil {
		writeError(w, http.StatusBadRequest, `both "consumer" and "stalled" are required`)
		return
	}
	if err := s.sess.SetStalled(*body.Consumer, *body.Stalled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"consumer": *body.Consumer, "stalled": *body.Stalled})
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Partitions int `json:"partitions"`
	}
	if !decode(w, r, &body, true) {
		return
	}
	if err := s.sess.Reset(body.Partitions); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Tell every open page now, instead of leaving them showing the old run
	// until the next tick.
	snap := s.sess.Snapshot()
	s.hub.publish(event{"snapshot", mustJSON(snap)})
	writeJSON(w, http.StatusOK, map[string]any{"epoch": snap.Epoch, "partitions": snap.Partitions})
}

// decode reads a small JSON body and rejects unknown fields, so a misspelled
// setting is an error instead of being silently ignored. With allowEmpty a
// missing body is accepted and leaves v untouched.
func decode(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeSSE(w http.ResponseWriter, name string, data []byte) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the types here are plain structs, so this cannot happen
	}
	return b
}
