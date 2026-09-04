package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
		why             string
	}{
		{"v0.3.0", "v0.4.0", true, "a minor bump is newer"},
		{"v0.4.0", "v0.4.1", true, "so is a patch"},
		{"v0.9.0", "v0.10.0", true, "10 > 9 as a number, not as a string"},
		{"v0.4.0", "v0.4.0", false, "the same release is not newer"},
		{"v0.4.0", "v0.3.0", false, "and older is never newer"},

		// The case this project actually produces. `git describe` stamps a
		// build two commits past the tag as v0.4.0-2-gbde191b; it CONTAINS
		// v0.4.0, so nagging it to install v0.4.0 would be a downgrade.
		{"v0.4.0-2-gbde191b", "v0.4.0", false, "a source build of the release is current"},
		{"v0.3.0-2-gbde191b", "v0.4.0", true, "but a source build behind a release is not"},
		{"v0.4.0-2-gbde191b-dirty", "v0.4.0", false, "a dirty tree changes nothing"},

		// Everything unparsable answers false. A wrong "you are current" is
		// invisible; a wrong "update available" sends someone chasing a version
		// that does not exist.
		{"dev", "v0.4.0", false, "an untagged build is never nagged"},
		{"v0.4.0", "", false, "an empty answer says nothing"},
		{"", "v0.4.0", false, "and so does an empty current"},
		{"v0.4", "v0.5", false, "two components is not a version this parses"},
		{"vX.Y.Z", "v0.4.0", false, "nor is a non-numeric one"},
	}
	for _, tc := range cases {
		if got := Newer(tc.current, tc.latest); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v — %s",
				tc.current, tc.latest, got, tc.want, tc.why)
		}
	}
}

func cacheAt(t *testing.T, s State) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update.json")
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAvailableReadsTheCacheOnly(t *testing.T) {
	c := Checker{
		Current: "v0.3.0",
		Path:    cacheAt(t, State{Latest: "v0.4.0", CheckedAt: time.Now()}),
		Fetch: func(context.Context) (string, error) {
			t.Fatal("Available must not touch the network — it runs on the startup path")
			return "", nil
		},
	}
	if got := c.Available(); got != "v0.4.0" {
		t.Fatalf("Available() = %q, want v0.4.0", got)
	}
}

func TestAvailableIsSilentWhenNothingIsKnown(t *testing.T) {
	// A missing cache file, which is every first run.
	c := Checker{Current: "v0.3.0", Path: filepath.Join(t.TempDir(), "absent.json")}
	if got := c.Available(); got != "" {
		t.Fatalf("Available() = %q, want silence on a cold cache", got)
	}
	// A corrupt one, which is what a killed process used to leave behind.
	bad := filepath.Join(t.TempDir(), "update.json")
	if err := os.WriteFile(bad, []byte(`{"latest": "v0.4`), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Path = bad
	if got := c.Available(); got != "" {
		t.Fatalf("Available() = %q, want silence on a corrupt cache", got)
	}
}

func TestDisableEnvSilencesEverything(t *testing.T) {
	t.Setenv(DisableEnv, "0")
	c := Checker{Current: "v0.3.0", Path: cacheAt(t, State{Latest: "v0.4.0", CheckedAt: time.Now()})}
	if c.Enabled() {
		t.Fatal("Enabled() is true with the check switched off")
	}
	if got := c.Available(); got != "" {
		t.Fatalf("Available() = %q with the check switched off", got)
	}
}

func TestRefreshHonoursTheTTL(t *testing.T) {
	calls := 0
	c := Checker{
		Current: "v0.3.0",
		Path:    cacheAt(t, State{Latest: "v0.3.0", CheckedAt: time.Now()}),
		TTL:     time.Hour,
		Fetch:   func(context.Context) (string, error) { calls++; return "v0.4.0", nil },
	}
	c.Refresh(context.Background())
	if calls != 0 {
		t.Fatalf("a fresh cache was refetched (%d calls) — every check costs someone a request", calls)
	}

	c.Path = cacheAt(t, State{Latest: "v0.3.0", CheckedAt: time.Now().Add(-2 * time.Hour)})
	c.Refresh(context.Background())
	if calls != 1 {
		t.Fatalf("a stale cache was not refreshed (%d calls)", calls)
	}
	if got := c.Available(); got != "v0.4.0" {
		t.Fatalf("the refreshed answer did not land in the cache: Available() = %q", got)
	}
}

func TestRefreshNowIgnoresTheTTL(t *testing.T) {
	calls := 0
	c := Checker{
		Current: "v0.3.0",
		Path:    cacheAt(t, State{Latest: "v0.3.0", CheckedAt: time.Now()}),
		Fetch:   func(context.Context) (string, error) { calls++; return "v0.4.0", nil },
	}
	c.RefreshNow(context.Background())
	if calls != 1 || c.Available() != "v0.4.0" {
		t.Fatalf("RefreshNow did not fetch (calls=%d, available=%q)", calls, c.Available())
	}
}

// A failing fetch must leave the cache exactly as it was. Writing an empty
// answer would turn one network blip into a permanent "you are current".
func TestAFailedFetchChangesNothing(t *testing.T) {
	before := State{Latest: "v0.4.0", CheckedAt: time.Now().Add(-48 * time.Hour)}
	c := Checker{
		Current: "v0.3.0",
		Path:    cacheAt(t, before),
		Fetch:   func(context.Context) (string, error) { return "", errors.New("network is down") },
	}
	c.Refresh(context.Background())
	if got := c.Load().Latest; got != "v0.4.0" {
		t.Fatalf("a failed fetch overwrote the cache: latest = %q", got)
	}
	if got := c.Available(); got != "v0.4.0" {
		t.Fatalf("the notice was lost on a failed refresh: Available() = %q", got)
	}
}

func TestAnEmptyAnswerIsNotCached(t *testing.T) {
	c := Checker{
		Current: "v0.3.0",
		Path:    filepath.Join(t.TempDir(), "update.json"),
		Fetch:   func(context.Context) (string, error) { return "   ", nil },
	}
	c.RefreshNow(context.Background())
	if _, err := os.Stat(c.Path); err == nil {
		t.Fatal("an empty tag was written to the cache")
	}
}

// No cache path at all (no resolvable home) must still work — without
// persistence, never with a failure.
func TestNoCachePathIsNotAnError(t *testing.T) {
	c := Checker{
		Current: "v0.3.0",
		Fetch:   func(context.Context) (string, error) { return "v0.4.0", nil },
	}
	c.RefreshNow(context.Background())
	if got := c.Available(); got != "" {
		t.Fatalf("Available() = %q without a cache to read", got)
	}
}

func TestStoreSurvivesRepeatedWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "update.json")
	c := Checker{Current: "v0.3.0", Path: path}
	c.store(State{Latest: "v0.4.0", CheckedAt: time.Now()})
	c.store(State{Latest: "v0.5.0", CheckedAt: time.Now()})
	if got := c.Load().Latest; got != "v0.5.0" {
		t.Fatalf("Load() = %q, want v0.5.0", got)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("the temp file was left behind")
	}
}
