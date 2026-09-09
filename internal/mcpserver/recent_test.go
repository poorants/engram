package mcpserver

import "testing"

func result(id any, paths ...string) map[string]any {
	hits := make([]any, 0, len(paths))
	for _, p := range paths {
		hits = append(hits, map[string]any{"path": p, "score": 0.1})
	}
	m := map[string]any{"hits": hits}
	if id != nil {
		m["search_id"] = id
	}
	return m
}

func TestRecentAttributesOnlyWhatTheLastSearchShowed(t *testing.T) {
	r := &recent{}
	if _, ok := r.lookup("acme/repo/resources/a.md"); ok {
		t.Fatal("nothing searched yet, nothing to attribute")
	}
	r.remember(result(float64(11), "acme/repo/resources/a.md", "acme/repo/resources/b.md"))
	if id, ok := r.lookup("acme/repo/resources/a.md"); !ok || id != 11 {
		t.Fatalf("a.md should be attributed to search 11, got %d %v", id, ok)
	}
	if _, ok := r.lookup("acme/repo/resources/zzz.md"); ok {
		t.Fatal("a document the search did not show must not be attributed to it")
	}
	// A second search replaces the first entirely.
	r.remember(result(float64(12), "acme/repo/resources/c.md"))
	if _, ok := r.lookup("acme/repo/resources/a.md"); ok {
		t.Fatal("a.md was shown by the previous search, not this one")
	}
	if id := r.forAny([]string{"acme/repo/resources/zzz.md", "acme/repo/resources/c.md"}); id != 12 {
		t.Fatalf("forAny = %d, want 12", id)
	}
}

func TestRecentForgetsWhenTheStoreLoggedNothing(t *testing.T) {
	r := &recent{}
	r.remember(result(float64(5), "acme/repo/resources/a.md"))
	// An older store answers without search_id; a store whose log failed
	// answers with null. Either way the previous id must not be reused.
	r.remember(result(nil, "acme/repo/resources/a.md"))
	if _, ok := r.lookup("acme/repo/resources/a.md"); ok {
		t.Fatal("a stale search id must not survive a result without one")
	}
	if r.forAny([]string{"acme/repo/resources/a.md"}) != 0 {
		t.Fatal("forAny must be 0 with no attributable search")
	}
}
