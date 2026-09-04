package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Human output for the store verbs.
//
// These used to live in the skill's store.py, which ran the binary and reshaped
// its JSON for a reader. The reshaping is the part worth keeping — a session
// that reads raw JSON pays for every field it did not need — so it moved here
// and the wrapper went away. JSON is still one flag off (--json), and every
// caller that parses output passes it.

// out writes UTF-8 bytes. On a Korean Windows console the code page is cp949,
// and a runtime that transcodes would either mangle a Hangul title or fail the
// whole command over one character in a path.
func out(format string, a ...any) {
	fmt.Fprintf(os.Stdout, format, a...)
}

func mapOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func listOf(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func sOf(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func fOf(m map[string]any, key string) float64 {
	if f, ok := m[key].(float64); ok {
		return f
	}
	return 0
}

func nOf(m map[string]any, key string) string {
	if f, ok := m[key].(float64); ok {
		return fmt.Sprintf("%d", int64(f))
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return "?"
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func renderSearch(res map[string]any, query string, chars int) {
	hits := listOf(res["hits"])
	if len(hits) == 0 {
		out("no results: %s\n", query)
		return
	}
	for i, h := range hits {
		m := mapOf(h)
		loc := sOf(m, "path")
		if hp := sOf(m, "heading_path"); hp != "" {
			loc += "  ¶ " + hp
		}
		repo := sOf(m, "repo")
		if repo == "" {
			repo = "?"
		}
		out("\n[%d] %s\n", i+1, loc)
		out("    [%s] score=%.4f\n", repo, fOf(m, "score"))
		body := strings.ReplaceAll(clip(sOf(m, "body"), chars), "\n", "\n    ")
		out("    %s\n", body)
	}
	if idx := mapOf(res["index"]); len(idx) > 0 {
		line := fmt.Sprintf("\n— store: %s documents · last write %s",
			nOf(idx, "docs"), clip(sOf(idx, "updated_at"), 16))
		// The boosted repo is echoed because a ranking nobody can explain is one
		// nobody trusts.
		if b := sOf(res, "boostRepo"); b != "" {
			line += fmt.Sprintf(" · boosted this repo '%s'", b)
		}
		out("%s\n", line)
	}
}

func renderDoc(doc map[string]any) {
	if f := sOf(doc, "savedTo"); f != "" {
		out("saved: %s\n", f)
	} else {
		out("%s\n", sOf(doc, "body"))
	}
	if bl := listOf(doc["backlinks"]); len(bl) > 0 {
		var paths []string
		for _, b := range bl {
			paths = append(paths, sOf(mapOf(b), "path"))
		}
		if len(paths) > 10 {
			paths = paths[:10]
		}
		out("\n— linked from %d document(s): %s\n", len(bl), strings.Join(paths, ", "))
	}
}

func renderRevisions(res map[string]any, path string) {
	revs := listOf(res["revisions"])
	if len(revs) == 0 {
		out("no history: %s\n", path)
		return
	}
	for _, r := range revs {
		m := mapOf(r)
		out("  %5s  %-19s  %-16s %6s chars  %s\n",
			nOf(m, "id"), clip(sOf(m, "at"), 19), sOf(m, "author"), nOf(m, "chars"), sOf(m, "note"))
	}
}

// renderIntegrity prints the store's own measurement of the link graph. The
// orphan count is the floor, not the goal — the weak-node list is the line that
// actually tells you the graph is still a folder tree.
func renderIntegrity(res map[string]any) {
	c := mapOf(res["counts"])
	out("broken links %s · orphans %s · weak nodes %s\n",
		nOf(c, "broken"), nOf(c, "orphans"), nOf(c, "weak"))

	byKind := mapOf(c["by_kind"])
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		v := mapOf(byKind[k])
		label := "structural (md/MOC)"
		if k == "wiki" {
			label = "contextual (wiki)"
		}
		out("  %-20s %5s edges · %s broken\n", label, nOf(v, "total"), nOf(v, "broken"))
	}

	if bl := listOf(res["broken_links"]); len(bl) > 0 {
		out("\nbroken links\n")
		for _, b := range bl {
			m := mapOf(b)
			out("  %s  →  [[%s]]  (%s)\n", sOf(m, "from"), sOf(m, "to"), sOf(m, "kind"))
		}
	}
	if o := listOf(res["orphans"]); len(o) > 0 {
		out("\norphans — nothing links to these\n")
		for _, v := range o {
			out("  %v\n", v)
		}
	}
	if w := listOf(res["weak_nodes"]); len(w) > 0 {
		out("\nweak nodes — reachable only from a MOC. Weave a contextual link into related prose\n")
		for _, v := range w {
			out("  %v\n", v)
		}
	}
}

func renderStatus(st map[string]any) {
	token := "absent"
	if b, _ := st["canWrite"].(bool); b {
		token = "present"
	}
	store := sOf(st, "store")
	if store == "" {
		store = "(not configured)"
	}
	out("store       %s  token %s\n", store, token)

	authorNote := "  (resolved automatically)"
	if sOf(st, "authorSource") != "" {
		authorNote = "  (configured)"
	}
	author := sOf(st, "author")
	if author == "" {
		author = "?"
	}
	out("author      %s%s\n", author, authorNote)

	if sOf(st, "root") != "" {
		out("here        owner=%s  repo=%s\n", sOf(st, "owner"), sOf(st, "repo"))
	} else {
		e := sOf(st, "scopeError")
		if e == "" {
			e = "(no git remote)"
		}
		out("here        %s\n", e)
	}

	if reachable, _ := st["reachable"].(bool); !reachable {
		out("connection  FAILED — %s\n", sOf(st, "error"))
		return
	}
	out("connection  ok · %s documents\n", nOf(st, "docs"))

	if ao := listOf(st["allowedOwners"]); len(ao) > 0 {
		var owners []string
		for _, o := range ao {
			owners = append(owners, fmt.Sprint(o))
		}
		out("admitted    %s\n", strings.Join(owners, ", "))
	}
	if w, ok := st["writesHere"].(bool); ok {
		if w {
			out("this repo   writes to the store\n")
		} else {
			out("this repo   is refused by the store → local file brain\n")
		}
	}
	for _, p := range listOf(st["present"]) {
		m := mapOf(p)
		out("    %s/%s: %s documents\n", sOf(m, "owner"), sOf(m, "repo"), nOf(m, "docs"))
	}
}
