package main

import "testing"

// The CLI's one privilege over the MCP tools is that it knows which checkout it
// is standing in. That derivation reads a git remote URL, which comes in both
// SSH and HTTPS spellings — and it decides the owner coordinate, which is the
// confidentiality boundary, so a miss here files knowledge under the wrong name.
func TestOriginRegexHandlesBothURLForms(t *testing.T) {
	for _, tc := range []struct{ url, owner, repo string }{
		{"git@github.com:poorants/engram.git", "poorants", "engram"},
		{"https://github.com/poorants/engram.git", "poorants", "engram"},
		{"https://github.com/poorants/engram", "poorants", "engram"},
		{"https://github.com/poorants/engram/", "poorants", "engram"},
		{"ssh://git@gitlab.example.com:2222/acme/webapp.git", "acme", "webapp"},
		{"/srv/git/acme/webapp.git", "acme", "webapp"},
	} {
		m := originRe.FindStringSubmatch(tc.url)
		if m == nil {
			t.Errorf("%s: no match", tc.url)
			continue
		}
		if m[1] != tc.owner || m[2] != tc.repo {
			t.Errorf("%s -> %s/%s, want %s/%s", tc.url, m[1], m[2], tc.owner, tc.repo)
		}
	}
}

// An explicit full path must pass through untouched. The failure this prevents:
// a caller deliberately writing to acme/shared from inside another repo, and
// the CLI helpfully rewriting it to that repo's scope.
func TestExpandPathLeavesFullPathsAlone(t *testing.T) {
	for _, p := range []string{
		"acme/shared/resources/x.md",
		"acme/webapp/README.md",
		"  acme/webapp/areas/y.md  ",
	} {
		got, err := expandPath(p)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got != trimSpace(p) {
			t.Errorf("expandPath(%q) = %q — an explicit path must not be rewritten", p, got)
		}
	}
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// parseInterspersed exists because `engram put <path> --note "why"` is how
// anyone actually types it, and Go's flag package stops at the first positional
// — leaving --note unset and producing an error about the wrong thing.
func TestParseInterspersedTakesFlagsAfterPositionals(t *testing.T) {
	fs := newTestFlagSet()
	note := fs.String("note", "", "")
	dry := fs.Bool("dry-run", false, "")
	pos, err := parseInterspersed(fs, []string{"acme/webapp/resources/x.md", "--note", "why this exists", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 1 || pos[0] != "acme/webapp/resources/x.md" {
		t.Fatalf("positional = %v", pos)
	}
	if *note != "why this exists" {
		t.Fatalf("note = %q — a flag after the path must still be parsed", *note)
	}
	if !*dry {
		t.Fatal("dry-run = false — a bare flag after the path must still be parsed")
	}
}
