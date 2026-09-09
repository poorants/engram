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

// nOf renders a count.
//
// It has to accept both a float64 and an int because these maps arrive from two
// places: decoded JSON, where every number is a float64, and status, which the
// binary assembles in memory from typed fields. Handling only the JSON case
// prints "?" for a number that is sitting right there.
func nOf(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case float64:
		return fmt.Sprintf("%d", int64(v))
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case string:
		return v
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
	}
	for i, h := range hits {
		m := mapOf(h)
		loc := sOf(m, "path")
		if hp := sOf(m, "heading_path"); hp != "" {
			loc += "  ¶ " + hp
		}
		out("\n[%d] %s\n", i+1, loc)
		// A tiered page carries no repo (the path says it); the untiered one
		// still does, and it is worth a glance there because that page is
		// the one that mixes repos without a boost being echoed per hit.
		if repo := sOf(m, "repo"); repo != "" {
			out("    [%s] score=%.4f\n", repo, fOf(m, "score"))
		} else {
			out("    score=%.4f\n", fOf(m, "score"))
		}
		// A snippet is already cut to size by the store; a body is clipped
		// here for a reader, and --json gets it whole.
		text := sOf(m, "snippet")
		if text == "" {
			text = clip(sOf(m, "body"), chars)
		}
		out("    %s\n", strings.ReplaceAll(text, "\n", "\n    "))
	}
	var line string
	if idx := mapOf(res["index"]); len(idx) > 0 {
		line = fmt.Sprintf("\n— store: %s documents · last write %s",
			nOf(idx, "docs"), clip(sOf(idx, "updated_at"), 16))
	}
	// The boosted repo is echoed because a ranking nobody can explain is one
	// nobody trusts.
	if b := sOf(res, "boostRepo"); b != "" {
		line += fmt.Sprintf(" · boosted this repo '%s'", b)
	}
	// The search id is what a vote names — `engram feedback <path> --search N`
	// or `engram get <path> --from-search N` — so it is printed, not hidden
	// in the JSON.
	if sid := nOf(res, "search_id"); sid != "?" && sid != "" {
		line += " · search #" + sid
	}
	if t := nOf(res, "tier"); t != "?" {
		line += " · tier " + t
	}
	if line != "" {
		out("%s\n", line)
	}
	// The next tier is spelled out as the command to run, because the point
	// of a tier is that the caller does not have to remember the ladder.
	if next := mapOf(res["next"]); len(next) > 0 {
		out("  not it? engram search --tier %s %q   (%s)\n", nOf(next, "tier"), query, sOf(next, "why"))
	}
}

func renderUsage(res map[string]any) {
	t := mapOf(res["totals"])
	out("last %s days · %s sessions · %s calls · ~%s tokens · %s searches · %s reads · %s votes · %s writes\n",
		nOf(res, "days"), nOf(t, "sessions"), nOf(t, "calls"), nOf(t, "tokens"),
		nOf(t, "searches"), nOf(t, "reads"), nOf(t, "votes"), nOf(t, "writes"))
	if n := nOf(t, "tier1"); n != "0" && n != "?" {
		out("tier-1 hit rate %.0f%%  (%s of %s tier-1 searches were widened)\n",
			fOf(t, "tier1_hit_rate")*100, nOf(t, "escalated"), n)
	}
	if nOf(t, "searches") != "0" && nOf(t, "votes") == "0" {
		out("no votes in this window — the ranking is not learning; check the wrap-up nudge lands\n")
	}
	sessions := listOf(res["sessions"])
	if len(sessions) == 0 {
		out("\nnothing logged in this window\n")
		return
	}
	out("\n%-28s %-10s %-13s %6s %8s %6s %5s %5s %6s %8s\n",
		"session", "who", "from → to", "calls", "tokens", "search", "read", "vote", "write", "tier1")
	for _, s := range sessions {
		m := mapOf(s)
		id := sOf(m, "session")
		if id == "" {
			id = "(no session)"
		}
		hit := "—"
		if nOf(m, "tier1") != "0" {
			hit = fmt.Sprintf("%.0f%%", fOf(m, "tier1_hit_rate")*100)
		}
		first, last := sOf(m, "first"), sOf(m, "last")
		span := ""
		if len(first) >= 16 && len(last) >= 16 {
			span = strings.Replace(first[5:16], "T", " ", 1) + "→" + last[11:16]
		}
		out("%-28s %-10s %-13s %6s %8s %6s %5s %5s %6s %8s\n",
			clip(id, 28), clip(sOf(m, "author"), 10), span, nOf(m, "calls"), nOf(m, "tokens"),
			nOf(m, "searches"), nOf(m, "reads"), nOf(m, "votes"), nOf(m, "writes"), hit)
	}
	if rep := listOf(res["repeated"]); len(rep) > 0 {
		out("\nasked in more than one session — a document nobody wrote yet, or a hub that does not point at it:\n")
		for _, r := range rep {
			m := mapOf(r)
			out("  %s sessions · %s times · %s\n", nOf(m, "sessions"), nOf(m, "times"), sOf(m, "q"))
		}
	}
}

func renderFeedback(res map[string]any) {
	kind := sOf(res, "kind")
	for _, r := range listOf(res["results"]) {
		m := mapOf(r)
		if w := sOf(m, "warning"); w != "" {
			out("note: %s\n", w)
			continue
		}
		status := sOf(m, "status")
		switch status {
		case "recorded":
			out("%s: %s  (utility now %+.2f · useful %s · noise %s · opened %s)\n",
				kind, sOf(m, "path"), fOf(m, "utility"), nOf(m, "useful"), nOf(m, "noise"), nOf(m, "opened"))
		case "already":
			out("%s: %s  — already counted today (utility %+.2f)\n", kind, sOf(m, "path"), fOf(m, "utility"))
		default:
			out("%s: %s  — %s\n", kind, sOf(m, "path"), status)
		}
	}
	if sid := nOf(res, "search_id"); sid != "?" && sid != "" {
		out("— attributed to search #%s: the next similar question will find these first\n", sid)
	} else {
		out("— no search named: counted toward the documents' general usefulness only\n")
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

// renderPatch reports what a partial write touched. It names the LINES, not
// just the document: the one question a reader has after a patch is whether it
// landed where they meant, and a bare "ok" answers everything except that.
func renderPatch(res map[string]any, path string) {
	status := sOf(res, "status")
	for _, e := range listOf(res["edits"]) {
		m := mapOf(e)
		out("  %s  lines %s..%s  (-%s +%s chars)\n", sOf(m, "how"),
			nOf(m, "start_line"), nOf(m, "end_line"),
			nOf(m, "chars_removed"), nOf(m, "chars_added"))
	}
	if d := sOf(res, "diff"); d != "" {
		out("%s", d)
	}
	switch status {
	case "dry_run":
		out("dry run: %s — nothing written. Run it again without --dry-run.\n", path)
	case "unchanged":
		out("unchanged: %s — the edits produce the document that is already stored.\n", path)
	default:
		out("ok: store: %s  (%s chars, sha %s)\n", path, nOf(res, "chars"),
			clip(sOf(res, "sha256"), 12))
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

	// The version line appears only when it says something. Printing "you are
	// current" every time is how the one run that says otherwise gets skimmed
	// past.
	if latest := sOf(st, "updateAvailable"); latest != "" {
		out("version     %s  →  %s available  (install.sh to update; restart the session after)\n",
			sOf(st, "version"), latest)
	}

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
