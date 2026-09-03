package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENGRAM_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ENGRAM_STORE_URL", "")
	t.Setenv("ENGRAM_TOKEN", "")
	t.Setenv("ENGRAM_AUTHOR", "")
	return filepath.Join(dir, "engram")
}

// The settings file is shared with the engram skill, which keeps its own keys in
// it (the file-brain registry it falls back to when the store refuses a repo).
// Writing through a struct would silently drop them, and the first symptom
// would be a designation that vanished for no visible reason.
func TestSetStorePreservesForeignKeys(t *testing.T) {
	dir := withConfigDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"version":1,"brains":{"shared":{"path":"/home/me/brain","autopush":true}},"store":{"url":"http://old:8081"}}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := SetStore("http://new:8081", []string{"acme"}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["brains"]; !ok {
		t.Fatal("the skill's `brains` key was dropped by a store write")
	}
	store, _ := doc["store"].(map[string]any)
	if store["url"] != "http://new:8081" {
		t.Fatalf("store.url = %v", store["url"])
	}
}

// Environment beats file for every value, and says so — a surprising address
// must be traceable without guessing which of the two won.
func TestEnvironmentOverridesFileAndIsReported(t *testing.T) {
	dir := withConfigDir(t)
	if _, err := SetStore("http://from-file:8081", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteToken("file-token"); err != nil {
		t.Fatal(err)
	}
	_ = dir

	if got := Load(); got.StoreURL != "http://from-file:8081" || got.Token != "file-token" {
		t.Fatalf("file load = %+v", got)
	}

	t.Setenv("ENGRAM_STORE_URL", "http://from-env:9000/")
	t.Setenv("ENGRAM_TOKEN", "env-token")
	cfg := Load()
	if cfg.StoreURL != "http://from-env:9000" {
		t.Fatalf("StoreURL = %q — the environment must win, with the trailing slash trimmed", cfg.StoreURL)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if len(cfg.FromEnv) != 2 {
		t.Fatalf("FromEnv = %v — every overridden value must be named", cfg.FromEnv)
	}
}

// A missing config file is the normal state before setup, not an error, and it
// must not be reported as an outage later.
func TestLoadWithNoFileIsEmptyNotAnError(t *testing.T) {
	withConfigDir(t)
	cfg := Load()
	if cfg.StoreURL != "" || cfg.Token != "" {
		t.Fatalf("expected an empty resolution, got %+v", cfg)
	}
	if cfg.Path == "" {
		t.Fatal("Path must still name where the file WOULD be, so `store show` can say it")
	}
}

// The token never lands in config.json — that file is one people open and paste
// from, and a secret in it eventually gets copied somewhere it should not be.
func TestTokenIsNotWrittenIntoTheConfigFile(t *testing.T) {
	dir := withConfigDir(t)
	if _, err := SetStore("http://x:8081", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteToken("s3cret"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "" && contains(string(b), "s3cret") {
		t.Fatal("the write token was written into config.json")
	}
	info, err := os.Stat(filepath.Join(dir, TokenName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// A pasted address is usually a deep link from the viewer people had open.
// Without normalization it becomes a base URL that appends to itself.
func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://brain.example:8081", "http://brain.example:8081"},
		{"http://brain.example:8081/", "http://brain.example:8081"},
		{"http://brain.example:8081/doc/acme/webapp/README.md", "http://brain.example:8081"},
		{"brain.example:8081", "http://brain.example:8081"},
		{"https://brain.example", "https://brain.example"},
	} {
		got, err := NormalizeURL(tc.in)
		if err != nil {
			t.Errorf("NormalizeURL(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://brain.example"} {
		if _, err := NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) should fail", bad)
		}
	}
}

// UnsetStore removes only what this binary owns.
func TestUnsetStoreLeavesTheRestAlone(t *testing.T) {
	dir := withConfigDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"version":1,"brains":{"shared":{"path":"/b"}},"store":{"url":"http://x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnsetStore(false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, FileName))
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["store"]; ok {
		t.Fatal("store should be gone")
	}
	if _, ok := doc["brains"]; !ok {
		t.Fatal("brains must survive")
	}
}
