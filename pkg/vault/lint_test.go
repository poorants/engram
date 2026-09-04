package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// write lays out a vault from a map of relative path -> body.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	base := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A link from a README is a folder spoke, not a weave. Confusing the two is the
// whole reason weak nodes exist as a category: the document passes the orphan
// check while the graph is still a star.
func TestHubInboundMakesAWeakNodeNotAWovenOne(t *testing.T) {
	base := write(t, map[string]string{
		"resources/README.md": "# hub\n- [spoke](spoke.md)\n- [woven](woven.md)\n",
		"resources/spoke.md":  "# spoke\n",
		"resources/woven.md":  "# woven\n",
		"areas/essay.md":      "# essay\nsee [woven](../resources/woven.md)\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	if !has(res.WeakNodes, "resources/spoke.md") {
		t.Errorf("spoke.md should be weak (only a README points at it); weak=%v", res.WeakNodes)
	}
	if has(res.WeakNodes, "resources/woven.md") {
		t.Errorf("woven.md has a contextual inbound and must not be weak; weak=%v", res.WeakNodes)
	}
	// essay.md is itself unlinked — the fixture is deliberately small, and the
	// point is that the two resources docs are not orphans.
	if has(res.Orphans, "resources/spoke.md") || has(res.Orphans, "resources/woven.md") {
		t.Errorf("both resources docs have inbound links; orphans=%v", res.Orphans)
	}
	if res.Metrics.Woven != 1 {
		t.Errorf("woven = %d, want 1", res.Metrics.Woven)
	}
}

// Go's RE2 has no lookbehind, so the `(?<!!)` guard became a leading-"!" check.
// An embed and an image must still be excluded, and — the part a naive
// `(^|[^!])` prefix would break — two adjacent wikilinks must both be seen.
func TestEmbedsAreNotLinksAndAdjacentLinksBothCount(t *testing.T) {
	base := write(t, map[string]string{
		"areas/src.md": "# src\n[[a]][[b]]\n![[c]]\n![shot](d.md)\n",
		"areas/a.md":   "# a\n",
		"areas/b.md":   "# b\n",
		"areas/c.md":   "# c\n",
		"areas/d.md":   "# d\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"areas/a.md", "areas/b.md"} {
		if has(res.Orphans, want) {
			t.Errorf("%s is linked and must not be an orphan; orphans=%v", want, res.Orphans)
		}
	}
	for _, want := range []string{"areas/c.md", "areas/d.md"} {
		if !has(res.Orphans, want) {
			t.Errorf("%s is only embedded/imaged, which is not a link; orphans=%v", want, res.Orphans)
		}
	}
}

// Inside a markdown table an alias pipe has to be written `\|`. Miss the
// unescape and the target keeps a trailing backslash: the link is counted
// dangling AND the real target is reported as an orphan — two wrong answers out
// of one missed character.
func TestEscapedAliasPipeInATableResolves(t *testing.T) {
	base := write(t, map[string]string{
		"areas/table.md":  "# table\n\n| doc | why |\n| --- | --- |\n| [[target\\|the target]] | because |\n",
		"areas/target.md": "# target\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DanglingWiki) != 0 {
		t.Errorf("the wikilink resolves; dangling=%v", res.DanglingWiki)
	}
	if has(res.Orphans, "areas/target.md") {
		t.Errorf("target.md is linked from the table; orphans=%v", res.Orphans)
	}
}

// Archived documents are set aside on purpose. Counting them as orphans buries
// the live graph's real problems under a list of things nobody lost.
func TestArchivesAreExemptFromTheAccounting(t *testing.T) {
	base := write(t, map[string]string{
		"archives/old.md":  "# old\n",
		"areas/live.md":    "# live\n",
		"areas/blog/p1.md": "# post\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != "areas/live.md" {
		t.Errorf("orphans = %v, want only areas/live.md", res.Orphans)
	}
	if res.Metrics.ContentDocs != 1 {
		t.Errorf("content_docs = %d, want 1 — archives and blog are out", res.Metrics.ContentDocs)
	}
}

// A link written inside a fenced block is an example, not an edge.
func TestCodeBlocksAreNotEdges(t *testing.T) {
	base := write(t, map[string]string{
		"areas/doc.md":    "# doc\n\n```\n[[target]]\n```\n\nand `[[target]]` inline.\n",
		"areas/target.md": "# target\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	if !has(res.Orphans, "areas/target.md") {
		t.Errorf("only code mentions it, so it is an orphan; orphans=%v", res.Orphans)
	}
}

// A relative markdown link to a file that is not there is an error; a wikilink
// to a note not written yet is only a warning. Merging the two would make the
// lint cry wolf on the Obsidian convention of writing the link first.
func TestBrokenLinkAndDanglingWikilinkAreDifferentThings(t *testing.T) {
	base := write(t, map[string]string{
		"areas/doc.md": "# doc\n[gone](./nope.md) and [[not-yet]]\n",
	})
	res, err := Lint(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.BrokenMDLinks) != 1 || res.BrokenMDLinks[0].Target != "./nope.md" {
		t.Errorf("broken = %v, want one ./nope.md", res.BrokenMDLinks)
	}
	if len(res.DanglingWiki) != 1 || res.DanglingWiki[0].Name != "not-yet" {
		t.Errorf("dangling = %v, want one not-yet", res.DanglingWiki)
	}
	if !res.HasIssues() {
		t.Error("a broken markdown link is an issue")
	}
}
