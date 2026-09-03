// Package identity answers one question: who is writing?
//
// The store records an `author` on every revision, but its token is a
// single SHARED credential — so `author` is a CLAIM the client makes, not
// something the server can verify. That distinction decides the whole design
// here: this package exists to make the claim HONEST BY DEFAULT, not to make it
// provable. Proving it would mean per-person tokens, which means accounts,
// issuing, revocation — a different system entirely.
//
// The default it replaces is $USER, which on a shared or containerized box is a
// machine account ("dev", "root", "ubuntu") and names nobody. The good default
// is the name on that person's commits, because brain history and git history
// then line up on one identity instead of two.
//
// It sits ABOVE the transport and is used by BOTH surfaces — the MCP tools and
// the CLI. That placement is the point: resolve identity once, at the edge, so
// every path that can write to the store stamps the same name. A resolver
// duplicated per surface drifts, and the drift is invisible because both
// answers look plausible.
package identity

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Fallback is the last-resort author: a name that says WHAT wrote, since by
// then we have failed to learn WHO.
const Fallback = "engram"

// lookupTimeout bounds an external lookup. Attribution must never be the reason
// a write is slow, let alone the reason it fails.
const lookupTimeout = 5 * time.Second

// Lookup asks some external system who this machine's user is, returning "" (not
// an error) when it cannot say. It is the extension point: a deployment that
// wants bylines to match its forge's usernames injects a resolver here instead
// of patching the order below.
type Lookup func(ctx context.Context) string

// Resolver produces the author name for a store write.
//
// It is lazy and memoized: most sessions never write to the brain, so the
// lookup must not happen at startup, and a session that writes ten times must
// not repeat it ten times. Zero value is not usable — build with New.
type Resolver struct {
	configured string // ENGRAM_AUTHOR or the config file, already resolved
	lookup     Lookup

	once   sync.Once
	cached string
}

// New builds a Resolver. configured is the deliberate, offline answer (empty
// when none was set); extra is an optional external lookup tried before the OS
// user — pass nil for the default behaviour.
func New(configured string, extra Lookup) *Resolver {
	return &Resolver{configured: strings.TrimSpace(configured), lookup: extra}
}

// Author resolves the name to stamp on a revision, in this order:
//
//	explicit argument  — the caller named someone; never overridden
//	ENGRAM_AUTHOR      — env or config file; the cheap, offline, deliberate answer
//	an injected Lookup — optional, for deployments with a forge to ask
//	git config user.name — automatic, and the same name as this person's commits
//	$USER / $LOGNAME   — a machine account, but better than nothing
//	Fallback           — names the tool, admitting we do not know the person
//
// Every failure below the explicit argument is SILENT and falls through. A
// write must never fail, or even warn, because attribution could not be
// resolved: the document is the point, the byline is not.
func (r *Resolver) Author(ctx context.Context, explicit string) string {
	if a := strings.TrimSpace(explicit); a != "" {
		return a
	}
	if r.configured != "" {
		return r.configured
	}
	r.once.Do(func() { r.cached = r.resolve(ctx) })
	if r.cached != "" {
		return r.cached
	}
	return osUser()
}

func (r *Resolver) resolve(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	if r.lookup != nil {
		if name := strings.TrimSpace(r.lookup(ctx)); name != "" {
			return name
		}
	}
	return gitUserName(ctx)
}

// gitUserName reads the name git would put on a commit made here. It is the
// closest thing to a verified identity that costs nothing: the person already
// set it, and it is the name their teammates recognize in `git log`.
func gitUserName(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "user.name").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// osUser is the step below git: on a personal machine it is often the right
// name, and on a shared box it is at least a real account.
func osUser() string {
	for _, key := range []string{"USER", "LOGNAME", "USERNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return Fallback
}
