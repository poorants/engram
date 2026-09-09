package mcpserver

import (
	"sync"
)

// recent is what the MCP server knows that the store cannot: which search this
// session ran last, and which documents it showed. The server is one process
// per session, so this is the one place the two halves of an implicit vote —
// "search showed X" and "then X was fetched" — meet without anyone passing an
// id around. brain_get consults it to tell the store which search led to the
// read, and brain_feedback falls back to it when the caller names no search.
//
// Only the LAST search is kept. A session that searched twice and then opened
// a document from the first page loses that attribution, and that is the
// right trade: guessing across searches would attribute reads to questions
// they did not come from, and a missed implicit vote costs nothing.
type recent struct {
	mu    sync.Mutex
	id    int64
	paths map[string]struct{}
}

// remember reads a search result as the store shaped it: search_id, and the
// paths of its hits. A result without a search_id (an older store, or one
// whose log failed) clears the memory rather than keeping a stale one.
func (r *recent) remember(res map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.id, r.paths = 0, nil
	id, ok := res["search_id"].(float64)
	if !ok || id <= 0 {
		return
	}
	hits, _ := res["hits"].([]any)
	paths := make(map[string]struct{}, len(hits))
	for _, h := range hits {
		if m, ok := h.(map[string]any); ok {
			if p, ok := m["path"].(string); ok && p != "" {
				paths[p] = struct{}{}
			}
		}
	}
	r.id, r.paths = int64(id), paths
}

// lookup returns the last search's id if it showed this path.
func (r *recent) lookup(path string) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id == 0 {
		return 0, false
	}
	_, shown := r.paths[path]
	return r.id, shown
}

// forAny returns the last search's id if it showed any of these paths — the
// fallback for a vote that names documents but no search.
func (r *recent) forAny(paths []string) int64 {
	for _, p := range paths {
		if id, ok := r.lookup(p); ok {
			return id
		}
	}
	return 0
}
