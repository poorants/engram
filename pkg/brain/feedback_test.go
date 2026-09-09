package brain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// A store that records what it was asked, so the wire form of each call can
// be checked without a database behind it.
type recorded struct {
	method string
	path   string
	query  url.Values
	body   map[string]any
}

func recordingStore(t *testing.T) (*Client, *recorded) {
	t.Helper()
	rec := &recorded{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.query = r.Method, r.URL.Path, r.URL.Query()
		rec.body = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	t.Cleanup(s.Close)
	return New(Config{BaseURL: s.URL, Token: "t"}), rec
}

func TestSearchSendsTierAndAuthorAndNothingItWasNotGiven(t *testing.T) {
	c, rec := recordingStore(t)
	if _, err := c.Search(context.Background(), SearchOpts{Query: "q", Tier: 2, Author: "me"}); err != nil {
		t.Fatal(err)
	}
	if rec.query.Get("tier") != "2" || rec.query.Get("author") != "me" {
		t.Errorf("query = %v", rec.query)
	}
	for _, absent := range []string{"limit", "archives", "repo"} {
		if rec.query.Has(absent) {
			t.Errorf("%s was sent unasked: %v — the tier decides the page", absent, rec.query)
		}
	}
	// Untiered stays exactly the old call.
	if _, err := c.Search(context.Background(), SearchOpts{Query: "q", Limit: 6}); err != nil {
		t.Fatal(err)
	}
	if rec.query.Has("tier") || rec.query.Has("author") || rec.query.Get("limit") != "6" {
		t.Errorf("untiered query = %v", rec.query)
	}
}

func TestSearchRefusesATierTheStoreDoesNotHave(t *testing.T) {
	c, _ := recordingStore(t)
	if _, err := c.Search(context.Background(), SearchOpts{Query: "q", Tier: 4}); err == nil {
		t.Fatal("tier 4 must be refused locally, not sent to become a 422")
	}
}

func TestDocFromNamesTheSearchOnlyWhenThereIsOne(t *testing.T) {
	c, rec := recordingStore(t)
	if _, err := c.DocFrom(context.Background(), "acme/repo/resources/x.md", ReadOpts{SearchID: 42, Author: "me"}); err != nil {
		t.Fatal(err)
	}
	if rec.query.Get("search") != "42" || rec.query.Get("author") != "me" {
		t.Errorf("query = %v", rec.query)
	}
	// A plain Doc must not invent a search — an unattributed read records
	// nothing, and that is correct.
	if _, err := c.Doc(context.Background(), "acme/repo/resources/x.md"); err != nil {
		t.Fatal(err)
	}
	if rec.query.Has("search") || rec.query.Has("author") {
		t.Errorf("a plain read must carry no attribution: %v", rec.query)
	}
}

func TestFeedbackPostsAVoteAndDefaultsToUseful(t *testing.T) {
	c, rec := recordingStore(t)
	_, err := c.Feedback(context.Background(), Vote{
		Paths:    []string{"acme/repo/resources/x.md", " ", "acme/repo/resources/y.md"},
		SearchID: 7, Author: "me", Note: "answered it",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/feedback" {
		t.Fatalf("%s %s", rec.method, rec.path)
	}
	if rec.body["kind"] != "useful" {
		t.Errorf("kind = %v — an unnamed vote is a useful one", rec.body["kind"])
	}
	if paths, _ := rec.body["paths"].([]any); len(paths) != 2 {
		t.Errorf("paths = %v — blanks must be dropped, the rest kept", rec.body["paths"])
	}
	if rec.body["search_id"] != float64(7) || rec.body["author"] != "me" || rec.body["note"] != "answered it" {
		t.Errorf("body = %v", rec.body)
	}
}

func TestFeedbackRefusesWhatTheStoreWouldRefuse(t *testing.T) {
	c, _ := recordingStore(t)
	ctx := context.Background()
	if _, err := c.Feedback(ctx, Vote{Paths: []string{"acme/repo/resources/x.md"}, Kind: "opened"}); err == nil {
		t.Fatal("opened is the store's own signal; a client must not be able to fake it")
	}
	if _, err := c.Feedback(ctx, Vote{Paths: nil}); err == nil {
		t.Fatal("a vote on nothing must be refused")
	}
	if _, err := c.Feedback(ctx, Vote{Paths: []string{"not a path"}}); err == nil {
		t.Fatal("a malformed path must be refused before the call leaves")
	}
	// No search_id key at all when there is none — the store treats the key's
	// presence as a claim.
	c2, rec := recordingStore(t)
	if _, err := c2.Feedback(ctx, Vote{Paths: []string{"acme/repo/resources/x.md"}}); err != nil {
		t.Fatal(err)
	}
	if _, has := rec.body["search_id"]; has {
		t.Errorf("search_id must be absent, not zero: %v", rec.body)
	}
}

func TestFeedbackNeedsTheToken(t *testing.T) {
	c := New(Config{BaseURL: "http://127.0.0.1:1"})
	_, err := c.Feedback(context.Background(), Vote{Paths: []string{"acme/repo/resources/x.md"}})
	if err != ErrNoToken {
		t.Fatalf("err = %v, want ErrNoToken — a vote is a write", err)
	}
}
