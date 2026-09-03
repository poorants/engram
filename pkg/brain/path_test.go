package brain

import "testing"

// The address rule is a depth CEILING below the document root, not a minimum
// segment count. Writing it as a minimum is the tempting mistake, and it
// refuses acme/webapp/README.md — the repo hub MOC, which the store indexes and
// serves happily, making every repo hub unreachable through the tools.
func TestValidatePath(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
		why  string
	}{
		{"acme/webapp/README.md", true, "repo hub MOC — no area, and that is legal"},
		{"acme/shared/resources/git-conventions.md", true, "the ordinary shape"},
		{"acme/webapp/areas/backend/architecture/schema-design.md", true, "4 levels"},
		{"acme/webapp/areas/a/b/c/d.md", true, "5 levels — the ceiling"},
		{"acme/shared/resources/dbml/schema.dbml", true, "dbml is a text document too"},
		{"acme/webapp/areas/a/b/c/d/e.md", false, "6 levels — over the ceiling"},
		{"acme/webapp/notes/x.md", false, "notes is not a PARA area"},
		{"acme/webapp/README.txt", false, "not a text document the store takes"},
		{"acme/README.md", false, "half a document root — repo missing"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		err := ValidatePath(c.path)
		if c.ok && err != nil {
			t.Errorf("ValidatePath(%q) rejected but should pass (%s): %v", c.path, c.why, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidatePath(%q) passed but should be rejected (%s)", c.path, c.why)
		}
	}
}

// Depth counts what is below the document root — owner/repo are coordinates
// (columns in the store), not directory levels.
func TestSplitPathDepth(t *testing.T) {
	for _, c := range []struct {
		path  string
		depth int
	}{
		{"acme/webapp/README.md", 1},
		{"acme/shared/resources/x.md", 2},
		{"acme/webapp/areas/backend/architecture/schema-design.md", 4},
	} {
		if _, rest := SplitPath(c.path); len(rest) != c.depth {
			t.Errorf("SplitPath(%q) depth = %d, want %d", c.path, len(rest), c.depth)
		}
	}
}
