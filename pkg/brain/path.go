package brain

import (
	"fmt"
	"net/url"
	"strings"
)

// Areas are the PARA four — the first segment below the document root.
var Areas = map[string]bool{"projects": true, "areas": true, "resources": true, "archives": true}

// TextSuffixes mirrors the store's accepted extensions. The store holds TEXT
// documents, not only markdown: a .dbml schema is canonical prose about a
// database, and leaving it outside means it is neither searched nor backed up
// with everything else. Binaries are not accepted.
var TextSuffixes = []string{".md", ".dbml"}

// MaxDepth mirrors the store's MAX_DEPTH: how deep a document may sit BELOW the
// document root (<owner>/<repo>), which is a coordinate, not a directory level.
// The ceiling exists to stop the deep tree of a file vault from growing back —
// depth is replaced by MOCs and links.
const MaxDepth = 5

// SplitPath cuts a path into its document root and the segments below it. The
// depth is the length of the second half — <owner>/<repo> are columns in the
// store, so they do not count as levels.
//
//	acme/webapp | areas/backend/architecture/schema-design.md
//	└ doc root ─┘  1      2       3            4
func SplitPath(path string) (root, rest []string) {
	parts := make([]string, 0, 6)
	for _, p := range strings.Split(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		return parts, nil
	}
	return parts[:2], parts[2:]
}

// ValidatePath mirrors the store's core.validate_path so a malformed path comes
// back with the rule instead of a server 4xx. The store is the authority; this
// is the copy that gives a good error message.
//
// The rule is a depth CEILING below the document root, not a minimum segment
// count. A "at least 4 segments" form would refuse acme/webapp/README.md — the
// repo hub MOC, which the store indexes and serves happily.
func ValidatePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("the path is empty")
	}
	ok := false
	for _, s := range TextSuffixes {
		if strings.HasSuffix(path, s) {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("unsupported extension (allowed: %s): %q", strings.Join(TextSuffixes, ", "), path)
	}
	root, rest := SplitPath(path)
	if len(root) < 2 {
		return fmt.Errorf("no document root in %q — a path starts with <owner>/<repo>/ "+
			"(e.g. acme/webapp/README.md, acme/shared/resources/git-conventions.md)", path)
	}
	if len(rest) == 0 {
		return fmt.Errorf("the document root is only half there: %q — the form is "+
			"<owner>/<repo>/<name>.md (e.g. acme/webapp/README.md)", path)
	}
	if len(rest) > MaxDepth {
		return fmt.Errorf("too deep (%d levels, ceiling %d): %q — at most %d levels below the "+
			"document root (<owner>/<repo>). Use links instead of depth", len(rest), MaxDepth, path, MaxDepth)
	}
	if len(rest) > 1 && !Areas[rest[0]] {
		return fmt.Errorf("the area (first segment below the document root) must be one of "+
			"projects|areas|resources|archives: %q — only a repo hub README comes with no area", rest[0])
	}
	return nil
}

// escapePath encodes each segment while keeping the slashes that are the
// store's path structure.
func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
