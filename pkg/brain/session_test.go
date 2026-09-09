package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTheSessionHeaderGoesOnEveryCallOnlyWhenThereIsOne(t *testing.T) {
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(SessionHeader))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer s.Close()
	ctx := context.Background()

	with := New(Config{BaseURL: s.URL, Token: "t", Session: " sess-1 "})
	if with.Session() != "sess-1" {
		t.Fatalf("Session() = %q — must be trimmed", with.Session())
	}
	_, _ = with.Search(ctx, SearchOpts{Query: "q", Tier: 1})
	_, _ = with.Doc(ctx, "acme/repo/resources/x.md")
	_, _ = with.Usage(ctx, UsageOpts{Days: 3})
	for i, h := range seen {
		if h != "sess-1" {
			t.Errorf("call %d carried session %q, want sess-1 — every call in a session must be attributable", i, h)
		}
	}

	seen = nil
	without := New(Config{BaseURL: s.URL})
	_, _ = without.Search(ctx, SearchOpts{Query: "q"})
	if len(seen) != 1 || seen[0] != "" {
		t.Errorf("a client with no session must send no header, got %v", seen)
	}
}

func TestUsageSendsOnlyWhatItWasGiven(t *testing.T) {
	c, rec := recordingStore(t)
	if _, err := c.Usage(context.Background(), UsageOpts{}); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/api/usage" || len(rec.query) != 0 {
		t.Errorf("%s %v — defaults are the store's to decide", rec.path, rec.query)
	}
	if _, err := c.Usage(context.Background(), UsageOpts{Days: 30, Session: "s", Limit: 5}); err != nil {
		t.Fatal(err)
	}
	if rec.query.Get("days") != "30" || rec.query.Get("session") != "s" || rec.query.Get("limit") != "5" {
		t.Errorf("query = %v", rec.query)
	}
}
