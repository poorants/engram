// Package selfupdate tells a running binary that a newer release exists.
//
// The shape is oh-my-zsh's, and it is here for the same reason: nobody upgrades
// a tool they never hear about. engram is distributed as a GitHub release, so a
// fix merged to main reaches a machine only when someone re-runs the installer
// — and until now the only signal to do that was a person saying so. The
// failure is silent by construction: every machine keeps running whatever it
// was first given, and the way it surfaces is a tool simply not being there in
// a session, where the binary's version is the last thing anyone suspects.
//
// Three constraints decide the design:
//
//   - **It must never delay startup.** The MCP server is spawned once per
//     session; a network call on that path costs every session, and on a
//     machine that cannot reach GitHub it costs the timeout. So the read path
//     is a CACHE READ (instant, offline) and the network call is a background
//     refresh whose answer is seen by the NEXT session — the same deal
//     oh-my-zsh's stamp file makes.
//   - **It must be silent when it cannot know.** No network, a repo with no
//     releases, a `dev` build, an unparsable tag — all of those mean "say
//     nothing", never "warn about something uncertain".
//   - **The notice must carry the remedy.** "A new version exists" without the
//     command to get it only moves the work to the reader.
//
// The package is transport-agnostic on purpose: it takes a Fetch function
// rather than reaching for an HTTP client, so the cache, the TTL and the
// comparison are testable without a network, and where releases live stays the
// caller's business.
package selfupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DisableEnv turns the check off entirely. Set it to 0 on a machine that
	// deliberately runs a pinned build, or one where the refresh can only ever
	// fail.
	DisableEnv = "ENGRAM_UPDATE_CHECK"

	// DefaultTTL is how stale a cached answer may be before a refresh is made.
	// A day: releases are not frequent enough to justify more, and every check
	// is a request someone else's infrastructure serves.
	DefaultTTL = 24 * time.Hour

	// DefaultTimeout bounds the background fetch. It is short because nothing
	// waits for it — a slow answer is a missed cycle, not a failure.
	DefaultTimeout = 3 * time.Second

	// DevVersion is the version of an un-tagged local build. Such a build is
	// deliberately ahead of any release, so it is never nagged.
	DevVersion = "dev"
)

// State is the cache file's content — the last answer and when it was taken.
type State struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// Checker holds one binary's update question.
type Checker struct {
	Current string                                // this build's version
	Path    string                                // cache file; "" disables persistence
	TTL     time.Duration                         // refresh interval (0 => DefaultTTL)
	Timeout time.Duration                         // fetch bound (0 => DefaultTimeout)
	Fetch   func(context.Context) (string, error) // returns the latest release tag
}

// CachePath is where the answer is remembered: $XDG_CACHE_HOME/engram, else
// ~/.cache/engram. Returns "" when no home can be resolved — the caller then
// runs without persistence rather than failing, because an update check is
// never worth failing over.
func CachePath() string {
	dir := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "engram", "update.json")
}

// Enabled reports whether the check should run at all.
func (c Checker) Enabled() bool {
	if strings.TrimSpace(os.Getenv(DisableEnv)) == "0" {
		return false
	}
	v := strings.TrimSpace(c.Current)
	return v != "" && v != DevVersion
}

// Load reads the cached answer. A missing, unreadable or corrupt cache is an
// empty state, never an error — this runs on the startup path.
func (c Checker) Load() State {
	if c.Path == "" {
		return State{}
	}
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		return State{}
	}
	var s State
	if json.Unmarshal(raw, &s) != nil {
		return State{}
	}
	return s
}

// Available returns the newer version the cache knows about, or "" when this
// build is current (or nothing is known). This is the whole read path: no
// network, no blocking, safe to call at startup.
func (c Checker) Available() string {
	if !c.Enabled() {
		return ""
	}
	latest := strings.TrimSpace(c.Load().Latest)
	if Newer(c.Current, latest) {
		return latest
	}
	return ""
}

// Stale reports whether the cached answer is old enough to refresh.
func (c Checker) Stale() bool {
	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	at := c.Load().CheckedAt
	return at.IsZero() || time.Since(at) >= ttl
}

// Refresh fetches the latest tag and caches it, but only once the cached answer
// has aged past the TTL. Every failure is silent: an update check that reports
// its own errors is noisier than the thing it is checking for.
//
// Call it in a goroutine — nothing waits for the result, and the point is that
// the next session reads it from the cache instantly.
func (c Checker) Refresh(ctx context.Context) {
	if !c.Stale() {
		return
	}
	c.RefreshNow(ctx)
}

// RefreshNow fetches regardless of the TTL — for `engram status --live`, where
// a person is waiting for a fresh answer and the cached one is not the
// question. Failures are silent here too; the caller reads the result through
// Available.
func (c Checker) RefreshNow(ctx context.Context) {
	if !c.Enabled() || c.Fetch == nil {
		return
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	latest, err := c.Fetch(ctx)
	if err != nil || strings.TrimSpace(latest) == "" {
		return
	}
	c.store(State{Latest: strings.TrimSpace(latest), CheckedAt: time.Now()})
}

// store writes the cache through a temp file, so a process killed mid-write
// cannot leave half a JSON document that every later run reads as "nothing
// known" — a cache that silently stops working looks exactly like a project
// that stopped releasing.
func (c Checker) store(s State) {
	if c.Path == "" {
		return
	}
	if os.MkdirAll(filepath.Dir(c.Path), 0o755) != nil {
		return
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := c.Path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) != nil {
		return
	}
	if os.Rename(tmp, c.Path) != nil {
		os.Remove(tmp)
	}
}

// Newer reports whether latest is a strictly higher release than current.
//
// Anything it cannot parse answers false. That asymmetry is deliberate: a wrong
// "you are current" is invisible, while a wrong "update available" sends
// someone chasing a version that does not exist.
//
// A build stamped by `git describe` — v0.4.0-2-gbde191b, two commits past the
// tag — compares as its tag, so a source build of the release is not nagged
// about the release it already contains.
func Newer(current, latest string) bool {
	cur, ok1 := parse(current)
	lat, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parse reads vX.Y.Z into its three numbers. A pre-release or build suffix is
// dropped — this project tags plain vX.Y.Z, and comparing suffixes correctly is
// more machinery than the question deserves.
func parse(v string) ([3]int, bool) {
	var out [3]int
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
