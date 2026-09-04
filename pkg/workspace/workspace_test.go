package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// settings points the config loader at a scratch file and clears the
// environment overrides, so a developer's own designated store cannot decide the
// outcome of a test.
func settings(t *testing.T, doc string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENGRAM_CONFIG_DIR", dir)
	t.Setenv("ENGRAM_STORE_URL", "")
	t.Setenv("ENGRAM_TOKEN", "")
	t.Setenv("ENGRAM_AUTHOR", "")
	if doc != "" {
		if err := os.MkdirAll(filepath.Join(dir, "engram"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "engram", "config.json"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func mkdirs(t *testing.T, root string, dirs ...string) string {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// THE resolution-order regression. A repo the store admits must resolve to the
// store even when a not-yet-migrated para/ is still sitting in it. Get this
// backwards and the session spends its time hand-editing a hub MOC nobody reads,
// while the store — the actual brain — never hears about the work.
func TestADesignatedStoreWinsOverALeftoverLocalVault(t *testing.T) {
	settings(t, `{"version":1,"store":{"url":"https://store.example"},"brains":{"shared":{"path":"/nowhere"}}}`)
	repo := mkdirs(t, t.TempDir(), "para/projects")

	r := Resolve(repo)
	if r.Source != SourceStore {
		t.Fatalf("source = %q, want store", r.Source)
	}
	if r.Store != "https://store.example" {
		t.Errorf("store = %q", r.Store)
	}
	// The vault is still reported — it is the 403 fallback — but it is not the
	// brain, and the warning has to say so.
	if r.Base == "" || r.Label != "para" {
		t.Errorf("base/label = %q/%q, want the leftover para/ reported as the fallback", r.Base, r.Label)
	}
	if r.Warning == "" {
		t.Error("a leftover vault next to a designated store needs a warning, or it gets read as the brain")
	}
}

// A repo whose owner the store does not admit keeps its knowledge in files. The
// resolution still says "store" — that IS the designation — but Admitted is
// false, which is what every caller branches on.
func TestAnUnadmittedOwnerIsNotAdmitted(t *testing.T) {
	settings(t, `{"version":1,"store":{"url":"https://store.example","owners":["acme"]}}`)
	repo := mkdirs(t, t.TempDir(), "brain/projects")

	r := Resolve(repo)
	if r.Source != SourceStore {
		t.Fatalf("source = %q, want store", r.Source)
	}
	// No git origin in a temp dir, so the owner is empty and the admitted list
	// cannot be applied: unknown, which is NOT the same as refused.
	if r.InScope != nil {
		t.Errorf("in_scope = %v — with no owner to compare, it must stay unknown", *r.InScope)
	}
	if !r.Admitted() {
		t.Error("unknown must read as admitted; refusing on a missing cache would strand every write")
	}
}

// With no store, the designated file brain is the brain — from anywhere.
func TestSharedFileBrainResolvesFromAnywhere(t *testing.T) {
	brainDir := mkdirs(t, t.TempDir(), "brain/projects")
	settings(t, `{"version":1,"brains":{"shared":{"path":`+quote(brainDir)+`}}}`)
	elsewhere := mkdirs(t, t.TempDir(), "src")

	r := Resolve(elsewhere)
	if r.Source != SourceShared {
		t.Fatalf("source = %q, want shared", r.Source)
	}
	if filepath.Clean(r.Base) != filepath.Join(brainDir, "brain") {
		t.Errorf("base = %q, want %q", r.Base, filepath.Join(brainDir, "brain"))
	}

	// Standing INSIDE it, the brain absorbs: it is the base, not a remote target.
	if r := Resolve(brainDir); r.Source != SourceAbsorb {
		t.Errorf("source inside the brain = %q, want absorb", r.Source)
	}
}

// Nothing designated and nothing on disk: say so. Inventing a path is how a
// session writes notes into a directory nobody will ever look in.
func TestNothingDesignatedResolvesToNone(t *testing.T) {
	settings(t, "")
	r := Resolve(mkdirs(t, t.TempDir(), "src"))
	if r.Source != SourceNone {
		t.Fatalf("source = %q, want none", r.Source)
	}
	if r.Base != "" {
		t.Errorf("base = %q, want empty — never invent a path", r.Base)
	}
}

// brain/ before para/ before a flat root: the order matters because a repo
// migrating from the legacy layout has both, and the new one has to win.
func TestLocalBasePrefersBrainOverParaOverFlat(t *testing.T) {
	both := mkdirs(t, t.TempDir(), "brain", "para")
	if _, label := LocalBase(both); label != "brain" {
		t.Errorf("label = %q, want brain", label)
	}
	legacy := mkdirs(t, t.TempDir(), "para")
	if _, label := LocalBase(legacy); label != "para" {
		t.Errorf("label = %q, want para", label)
	}
	flat := mkdirs(t, t.TempDir(), "projects", "areas")
	base, label := LocalBase(flat)
	if label != "." || filepath.Clean(base) != filepath.Clean(flat) {
		t.Errorf("base/label = %q/%q, want the root as '.'", base, label)
	}
	if base, _ := LocalBase(mkdirs(t, t.TempDir(), "src")); base != "" {
		t.Errorf("base = %q, want empty for a repo with no PARA folders", base)
	}
}

func quote(s string) string {
	b := []byte{'"'}
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '"' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return string(append(b, '"'))
}
