//go:build noupdate

package main

// The frozen build: engram that cannot replace itself.
//
// `-tags noupdate` compiles out every path that reaches for a release —
// the version check, the download, and the swap in pkg/selfupdate/apply.go.
// What is left cannot be talked into fetching an executable, because the code
// to do it is not in the binary.
//
// The reason is endpoint security, not taste. A self-updating binary rewrites
// itself on disk, so its hash changes under a scanner that has already made up
// its mind about the old one; an unsigned binary that downloads an executable,
// renames itself aside and runs what it just wrote is also, step for step, what
// a dropper does. On a managed machine where an allowlist entry is not the
// user's to grant, the way out is a binary whose hash stops moving and whose
// behaviour never resembles the thing being scanned for.
//
// Updating such a build is a deliberate act performed from outside: build from
// source, or unpack a release, and copy it over.

import (
	"fmt"
	"os"

	"github.com/poorants/engram/pkg/selfupdate"
)

// newUpdateChecker returns a checker that can never answer.
//
// No Fetch, so the two callers that guard on it (`engram mcp`, `engram status`)
// make no network call; no Path, so the cache file is neither read nor written.
// Available() is therefore always "" and no notice is ever composed — the same
// silence a current binary produces, reached without asking anyone.
func newUpdateChecker() selfupdate.Checker {
	return selfupdate.Checker{Current: resolveVersion()}
}

// cmdUpdate says what this build cannot do, and how to do it anyway.
//
// The verb is kept rather than dropped: `engram update` is what the docs, the
// habit and a model in a session all reach for, and an "unknown command" would
// send the reader looking for a typo instead of telling them the truth.
func cmdUpdate(args []string) int {
	fmt.Fprintf(os.Stderr,
		"engram %s is a frozen build: it has no self-update.\n\n"+
			"The download-and-replace code is compiled out (-tags noupdate), so this\n"+
			"binary cannot rewrite itself. That is deliberate — a binary whose hash\n"+
			"changes under an endpoint scanner is what caused this build to exist.\n\n"+
			"To move to another version, replace the file from outside:\n"+
			"  git -C <engram checkout> pull\n"+
			"  make build-frozen        # writes ./engram\n"+
			"  # then copy it over %s\n",
		resolveVersion(), exePath())
	return exitError
}

// exePath is this binary's own path, for the message above. A failure is not
// worth reporting here — the sentence still reads with a placeholder.
func exePath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "this binary"
}
