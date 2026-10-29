package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	socialfeed "github.com/Sumit6258/Faultline/systems/social-feed"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestFanoutAmplificationAndStrategyArePublished(t *testing.T) {
	svc, err := socialfeed.New(socialfeed.Config{Strategy: socialfeed.FanoutOnWrite, MaxInbox: 100})
	if err != nil {
		t.Fatal(err)
	}
	h := observe(socialfeed.NewHandler(svc), svc)

	for _, f := range []string{"a", "b", "c", "d"} {
		svc.Follow(f, "star")
	}
	svc.Post("star", "hello") // fans out to four inboxes

	out := scrape(t, h)
	for _, want := range []string{
		"socialfeed_inbox_writes_total 4\n",
		`socialfeed_strategy_info{strategy="write"} 1` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestEachOfTheFeedsRoutesKeepsItsOwnLabel(t *testing.T) {
	svc, _ := socialfeed.New(socialfeed.Config{Strategy: socialfeed.Hybrid, MaxInbox: 100, CelebrityThreshold: 10})
	h := observe(socialfeed.NewHandler(svc), svc)

	do := func(method, path, body string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	do("POST", "/follow", `{"follower":"alice","followee":"bob"}`)
	do("POST", "/posts", `{"author":"bob","body":"hi"}`)
	do("GET", "/timeline/alice", "")
	do("GET", "/timeline/carol", "")

	out := scrape(t, h)
	for _, route := range []string{`route="/follow"`, `route="/posts"`, `route="/timeline/"`} {
		if !strings.Contains(out, route) {
			t.Errorf("no series for %s in\n%s", route, out)
		}
	}
	if strings.Contains(out, "alice") || strings.Contains(out, "carol") {
		t.Error("a user name leaked into a label, one series per user would follow")
	}
	if !strings.Contains(out, `http_requests_total{method="GET",route="/timeline/",status="200"} 2`+"\n") {
		t.Errorf("both timeline reads should share one series:\n%s", out)
	}
}
