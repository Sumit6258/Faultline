package socialfeed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T, kind StrategyKind) (http.Handler, *Service) {
	t.Helper()
	svc, err := New(Config{Strategy: kind, MaxInbox: 800, CelebrityThreshold: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return NewHandler(svc), svc
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNew_UnknownStrategyIsAnError(t *testing.T) {
	if _, err := New(Config{Strategy: "sideways"}); err == nil {
		t.Fatal("expected an error for an unknown strategy")
	}
}

// The same scripted conversation, sent through the real HTTP handler under
// every strategy, must produce the same timeline. bob ends up with 3
// followers, which meets the test threshold of 3, so under the hybrid his
// posts take the celebrity path while carol's take the inbox path.
func TestEveryStrategyServesTheSameTimelineOverHTTP(t *testing.T) {
	var timelines [][]string
	for _, kind := range []StrategyKind{FanoutOnWrite, FanoutOnRead, Hybrid} {
		h, _ := newTestHandler(t, kind)
		for _, f := range []string{
			`{"follower":"alice","followee":"bob"}`,
			`{"follower":"dave","followee":"bob"}`,
			`{"follower":"erin","followee":"bob"}`,
			`{"follower":"alice","followee":"carol"}`,
		} {
			if rec := do(h, http.MethodPost, "/follow", f); rec.Code != http.StatusNoContent {
				t.Fatalf("%s: follow returned %d", kind, rec.Code)
			}
		}
		for _, p := range []string{
			`{"author":"bob","body":"one"}`,
			`{"author":"carol","body":"two"}`,
			`{"author":"bob","body":"three"}`,
		} {
			if rec := do(h, http.MethodPost, "/posts", p); rec.Code != http.StatusOK {
				t.Fatalf("%s: post returned %d", kind, rec.Code)
			}
		}

		rec := do(h, http.MethodGet, "/timeline/alice", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: timeline returned %d", kind, rec.Code)
		}
		var got []postJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: bad JSON: %v", kind, err)
		}
		var bodies []string
		for _, p := range got {
			bodies = append(bodies, p.Body)
		}
		timelines = append(timelines, bodies)
	}

	want := []string{"three", "two", "one"}
	for i, got := range timelines {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("strategy %d: expected %v, got %v", i, want, got)
		}
	}
}

func TestStatsReportsFanoutWork(t *testing.T) {
	for _, tc := range []struct {
		kind StrategyKind
		want int64
	}{
		{FanoutOnWrite, 3}, // 3 followers of bob, 1 post
		{FanoutOnRead, 0},
		{Hybrid, 0}, // bob has 3 followers, meeting the threshold of 3
	} {
		h, _ := newTestHandler(t, tc.kind)
		for _, u := range []string{"a", "b", "c"} {
			do(h, http.MethodPost, "/follow", `{"follower":"`+u+`","followee":"bob"}`)
		}
		do(h, http.MethodPost, "/posts", `{"author":"bob","body":"hi"}`)

		var st Stats
		json.Unmarshal(do(h, http.MethodGet, "/stats", "").Body.Bytes(), &st)
		if st.InboxWrites != tc.want || st.Strategy != tc.kind {
			t.Fatalf("%s: expected %d inbox writes, got %+v", tc.kind, tc.want, st)
		}
	}
}

func TestHandlerRejectsBadRequests(t *testing.T) {
	h, _ := newTestHandler(t, Hybrid)
	for _, tc := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"follow with wrong method", http.MethodGet, "/follow", "", http.StatusMethodNotAllowed},
		{"follow with bad json", http.MethodPost, "/follow", "{", http.StatusBadRequest},
		{"follow with empty names", http.MethodPost, "/follow", `{"follower":"","followee":""}`, http.StatusBadRequest},
		{"post with wrong method", http.MethodGet, "/posts", "", http.StatusMethodNotAllowed},
		{"post with no author", http.MethodPost, "/posts", `{"body":"x"}`, http.StatusBadRequest},
		{"timeline with no user", http.MethodGet, "/timeline/", "", http.StatusNotFound},
		{"timeline with non numeric limit", http.MethodGet, "/timeline/a?limit=lots", "", http.StatusBadRequest},
		{"timeline with limit too large", http.MethodGet, "/timeline/a?limit=5000", "", http.StatusBadRequest},
	} {
		if rec := do(h, tc.method, tc.path, tc.body); rec.Code != tc.want {
			t.Errorf("%s: expected %d, got %d", tc.name, tc.want, rec.Code)
		}
	}
}

func TestTimelineForAUserWithNoFolloweesIsAnEmptyListNotNull(t *testing.T) {
	h, _ := newTestHandler(t, FanoutOnRead)
	rec := do(h, http.MethodGet, "/timeline/nobody", "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("expected [], got %q", rec.Body.String())
	}
}
