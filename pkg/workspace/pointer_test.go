package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-running link must replace the block in place. If it appended instead, a
// repo would collect one stale pointer per run and a session would read the
// oldest one first.
func TestPointerIsIdempotentAndPreservesHandWrittenText(t *testing.T) {
	repo := t.TempDir()
	claude := filepath.Join(repo, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("# House rules\n\nRun the tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolution{Source: SourceStore, Store: "https://store.example", Owner: "acme", Repo: "webapp"}

	if state, _, err := WritePointer(repo, r); err != nil || state != "updated" {
		t.Fatalf("first write: state=%q err=%v", state, err)
	}
	after, _ := os.ReadFile(claude)
	if !strings.Contains(string(after), "Run the tests.") {
		t.Fatal("the hand-written part of CLAUDE.md must survive")
	}

	state, _, err := WritePointer(repo, r)
	if err != nil || state != "unchanged" {
		t.Fatalf("second write: state=%q err=%v — an unchanged block must not rewrite the file", state, err)
	}
	if strings.Count(string(after), pointerBegin) != 1 {
		t.Fatal("exactly one pointer block, always")
	}

	state, _, err = RemovePointer(repo)
	if err != nil || state != "removed" {
		t.Fatalf("remove: state=%q err=%v", state, err)
	}
	final, _ := os.ReadFile(claude)
	if strings.Contains(string(final), "engram:brain-pointer") {
		t.Error("the block is gone")
	}
	if !strings.Contains(string(final), "Run the tests.") {
		t.Error("removing the block must not take the rest of the file with it")
	}
}

// A CLAUDE.md that is nothing but the block was created by link, so removing it
// leaves an empty file behind unless the file goes too.
func TestRemovingAPointerOnlyFileDeletesIt(t *testing.T) {
	repo := t.TempDir()
	r := Resolution{Source: SourceLocal, Base: filepath.Join(repo, "brain"), Label: "brain"}
	if state, _, err := WritePointer(repo, r); err != nil || state != "created" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if state, _, err := RemovePointer(repo); err != nil || state != "removed-empty" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("a file that held only the block should be gone")
	}
}
