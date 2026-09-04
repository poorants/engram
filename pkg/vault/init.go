package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NestedDir picks the folder the PARA categories nest under: an explicit request
// wins, then an existing brain/, then a legacy para/, else brain.
//
// brain/ is the default for standalone vaults AND code projects alike, so the
// repo root stays free for repo meta and any published output. A legacy para/ is
// reused rather than migrated, because moving somebody's notes is not something
// an init command gets to decide.
func NestedDir(base, requested string) string {
	if r := strings.TrimSpace(requested); r != "" {
		return r
	}
	if st, err := os.Stat(filepath.Join(base, "brain")); err == nil && st.IsDir() {
		return "brain"
	}
	if st, err := os.Stat(filepath.Join(base, "para")); err == nil && st.IsDir() {
		return "para"
	}
	return "brain"
}

// InitPARA creates the four category folders with a .gitkeep in each, and
// reports what it did per folder. Existing folders are left alone — running it
// twice is a no-op, not a reset.
func InitPARA(base string, flat bool, nestedDir string) ([]string, error) {
	base = canon(base)
	prefix := ""
	if !flat {
		prefix = NestedDir(base, nestedDir)
	}
	var created []string
	for _, name := range Categories {
		dir := filepath.Join(base, name)
		if prefix != "" {
			dir = filepath.Join(base, prefix, name)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return created, err
		}
		keep := filepath.Join(dir, ".gitkeep")
		if _, err := os.Stat(keep); err == nil {
			created = append(created, fmt.Sprintf("%s/ (already exists)", filepath.ToSlash(dir)))
			continue
		}
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return created, err
		}
		created = append(created, filepath.ToSlash(keep))
	}
	return created, nil
}

// WriteRefused saves a document the store declined into the file brain — where
// it belonged. The <owner>/<repo> coordinates are dropped: in a file brain the
// directory IS the scope, so keeping them would nest the vault one repo deep
// inside itself.
func WriteRefused(base, docPath, body string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("no local file brain is designated")
	}
	var parts []string
	for _, p := range strings.Split(filepath.ToSlash(docPath), "/") {
		if p != "" && p != "." && p != ".." {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("the document path is empty")
	}
	rel := parts[len(parts)-1]
	if len(parts) > 2 {
		rel = strings.Join(parts[2:], "/")
	}
	f := filepath.Join(canon(base), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(f), nil
}
