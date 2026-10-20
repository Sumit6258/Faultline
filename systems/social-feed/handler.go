package socialfeed

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type postJSON struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// NewHandler exposes a Service over HTTP:
//
//	POST /follow            {"follower": "a", "followee": "b"}
//	POST /posts             {"author": "a", "body": "..."}
//	GET  /timeline/{user}   ?limit=20
//	GET  /stats
func NewHandler(svc *Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/follow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct{ Follower, Followee string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Follower == "" || body.Followee == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		svc.Follow(body.Follower, body.Followee)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/posts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct{ Author, Body string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Author == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p := svc.Post(body.Author, body.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(postJSON{ID: p.ID, Author: p.Author, Body: p.Body, CreatedAt: p.CreatedAt})
	})

	mux.HandleFunc("/timeline/", func(w http.ResponseWriter, r *http.Request) {
		user := strings.TrimPrefix(r.URL.Path, "/timeline/")
		if user == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 200 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			limit = n
		}
		out := []postJSON{}
		for _, p := range svc.Timeline(user, limit) {
			out = append(out, postJSON{ID: p.ID, Author: p.Author, Body: p.Body, CreatedAt: p.CreatedAt})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(svc.Stats())
	})

	return mux
}
