package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poorants/engram/pkg/brain"
	"github.com/poorants/engram/pkg/config"
)

// A store that answers the owner list only to an authenticated caller — the
// same 401 a real store gives, which is what makes the regression visible.
func scopeStore(t *testing.T, token string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(brain.TokenHeader) != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed_owners":["poorants"],"present":[]}`))
	}))
	t.Cleanup(s.Close)
	return s
}

// storeSet reports to stdout, and the report IS the behaviour under test here —
// the token file is never in danger, only the account of it.
func captureOut(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	rc := fn()
	os.Stdout = saved
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String(), rc
}

func newConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENGRAM_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ENGRAM_TOKEN", "")
	t.Setenv("ENGRAM_STORE_URL", "")
	return dir
}

// Re-running `store set` to change only the address or the byline is the
// ordinary case, and it must not describe this machine as having no token.
//
// The token file is never actually touched by that command — which is exactly
// what made the bug dangerous rather than merely wrong. The scope probe went
// out unauthenticated, took a 401, and the command then printed
//
//	note:   could not read the allowed owner groups yet (... HTTP 401 ...)
//	token:  not set — ... Re-run with --token.
//
// So a command that had fully succeeded read as one that had just discarded the
// credential. Setup is where someone is least able to tell a false report from
// a real failure, so the report has to be true.
func TestStoreSetReportsTheTokenItAlreadyHas(t *testing.T) {
	dir := newConfigDir(t)
	srv := scopeStore(t, "t0k")

	if _, rc := captureOut(t, func() int {
		return storeSet([]string{srv.URL, "--token", "t0k"})
	}); rc != exitOK {
		t.Fatalf("first store set: rc=%d", rc)
	}

	// Only the byline changes. No --token.
	out, rc := captureOut(t, func() int {
		return storeSet([]string{srv.URL, "--author", "poorants"})
	})
	if rc != exitOK {
		t.Fatalf("second store set: rc=%d\n%s", rc, out)
	}

	if strings.Contains(out, "not set") || strings.Contains(out, "Re-run with --token") {
		t.Errorf("reported the machine as having no token:\n%s", out)
	}
	if !strings.Contains(out, "token:  kept") {
		t.Errorf("did not report the token it kept:\n%s", out)
	}
	// The probe went out authenticated, so the owner list was read rather than
	// refused — no 401, and the cache is refreshed instead of quietly stale.
	if strings.Contains(out, "could not read the allowed owner groups") {
		t.Errorf("the scope probe went out unauthenticated:\n%s", out)
	}
	if !strings.Contains(out, "owners: poorants") {
		t.Errorf("owners not reported:\n%s", out)
	}

	b, err := os.ReadFile(filepath.Join(dir, "engram", config.TokenName))
	if err != nil || strings.TrimSpace(string(b)) != "t0k" {
		t.Fatalf("token on disk: %q %v", b, err)
	}
	if cfg := config.Load(); cfg.Author != "poorants" {
		t.Errorf("Author = %q, want poorants", cfg.Author)
	}
}

// With nothing on disk and nothing passed, the honest report is still "not set".
func TestStoreSetReportsNoTokenWhenThereIsNone(t *testing.T) {
	dir := newConfigDir(t)
	srv := scopeStore(t, "t0k")

	out, rc := captureOut(t, func() int { return storeSet([]string{srv.URL}) })
	if rc != exitOK {
		t.Fatalf("store set: rc=%d", rc)
	}
	if !strings.Contains(out, "not set") {
		t.Errorf("a machine with no token must be told so:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "engram", config.TokenName)); !os.IsNotExist(err) {
		t.Errorf("a token file was created out of nothing: %v", err)
	}
}

// ENGRAM_TOKEN is a legitimate way to hold the credential, and `store set` must
// neither ignore it when probing nor claim the file is the source.
func TestStoreSetHonoursTokenFromEnvironment(t *testing.T) {
	dir := newConfigDir(t)
	srv := scopeStore(t, "env0k")
	t.Setenv("ENGRAM_TOKEN", "env0k")

	out, rc := captureOut(t, func() int { return storeSet([]string{srv.URL}) })
	if rc != exitOK {
		t.Fatalf("store set: rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "ENGRAM_TOKEN") {
		t.Errorf("did not name the environment as the source:\n%s", out)
	}
	if !strings.Contains(out, "owners: poorants") {
		t.Errorf("the probe ignored ENGRAM_TOKEN:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "engram", config.TokenName)); !os.IsNotExist(err) {
		t.Errorf("a token from the environment was written to disk: %v", err)
	}
}
