package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/poorants/engram/pkg/selfupdate"
)

// The update notice — this binary's answer to "how does anyone learn a new
// release exists".
//
// engram ships as a GitHub release, so a fix merged to main reaches a machine
// only when someone re-runs the installer. The signal to do that was, until
// now, a person saying so, and the failure is silent: the machine keeps working
// and simply lacks whatever was added. It surfaces as a tool that is not there
// in a session, which reads as a broken server rather than an old binary.
//
// Where the notice can go is decided by the transport. stdout is protocol
// framing — a stray line there corrupts the session — and stderr lands in a log
// nobody opens. So it goes where it is actually read:
//
//	initialize.instructions   the MODEL reads this, so it can relay the notice
//	                          and offer to run the update. That is better than
//	                          the [Y/n] prompt a TTY tool would print: the agent
//	                          can also perform the update.
//	first tool result         a belt-and-braces line for clients that ignore
//	                          instructions. Once per session, never per call.
//	stderr                    for whoever does read logs.
//	engram status             for a person who asks directly.

// updateRepo is where this binary's own releases live — the same repo
// install.sh downloads from. Kept as a constant rather than a flag: a binary
// that checked a DIFFERENT project for its own updates would be a footgun, not
// a feature.
const updateRepo = "poorants/engram"

var tagFromURL = regexp.MustCompile(`/releases/tag/([^/]+)/?$`)

// latestReleaseTag follows the /releases/latest redirect and reads the tag out
// of where it lands.
//
// That is deliberate, and it is the same choice install.sh makes: the obvious
// alternative, the GitHub API, is rate-limited per IP for unauthenticated
// callers — 60 requests an hour shared by everyone behind one office NAT. A
// version check that starts failing once a few people are working is worse than
// no check, because the silence is indistinguishable from "you are current".
func latestReleaseTag(ctx context.Context) (string, error) {
	url := "https://github.com/" + updateRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// resp.Request is the LAST request made, so its URL is where the redirect
	// chain landed: .../releases/tag/v0.4.0
	m := tagFromURL.FindStringSubmatch(resp.Request.URL.Path)
	if m == nil {
		return "", fmt.Errorf("no release tag in %s", resp.Request.URL)
	}
	return m[1], nil
}

// newUpdateChecker wires the cache-backed checker to GitHub. Unlike the store,
// this needs no credential — the releases are public — so the check works on a
// machine that has not been configured yet.
func newUpdateChecker() selfupdate.Checker {
	return selfupdate.Checker{
		Current: resolveVersion(),
		Path:    selfupdate.CachePath(),
		Fetch:   latestReleaseTag,
	}
}

// updateNotice is the one sentence every surface shares. Empty when this build
// is current, unknown, or the check is switched off.
//
// It names the remedy, because "a new version exists" without the command to
// get it only moves the work to the reader.
func updateNotice(c selfupdate.Checker) string {
	latest := c.Available()
	if latest == "" {
		return ""
	}
	return fmt.Sprintf(
		"engram %s is running and %s is out. Update: curl -fsSL "+
			"https://raw.githubusercontent.com/%s/main/install.sh | sh "+
			"(the settings, the token and the store are untouched). "+
			"A new session has to be started before the new binary is in use.",
		c.Current, latest, updateRepo)
}

// serverInstructions puts the notice in front of the model, ahead of the
// standing instructions.
//
// It returns the instructions unchanged in the normal case. A server that
// always says something about itself trains the reader to skip the part that
// matters.
func serverInstructions(base, notice string) string {
	if notice == "" {
		return base
	}
	return "UPDATE AVAILABLE: " + notice +
		" Tell the user, and offer to run the update for them. Tool behaviour is unaffected.\n\n" + base
}

// noticeMiddleware appends the notice to the FIRST tool result of a session.
//
// Once, not per call: a tag on every result is noise, and noise is how a real
// warning gets ignored. A failed call is left alone — an update notice stapled
// to an error only obscures the error.
func noticeMiddleware(notice string) mcp.Middleware {
	var once sync.Once
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/call" {
				return res, err
			}
			call, ok := res.(*mcp.CallToolResult)
			if !ok || call.IsError {
				return res, err
			}
			once.Do(func() {
				call.Content = append(call.Content,
					&mcp.TextContent{Text: "[engram] " + notice})
			})
			return call, nil
		}
	}
}

// startUpdateCheck refreshes the cache in the background and returns the notice
// derived from what the cache ALREADY held.
//
// The asymmetry is the point: the read is instant and offline, and the network
// call is nobody's dependency. A refresh started now is read by the next
// session.
func startUpdateCheck(c selfupdate.Checker) string {
	notice := updateNotice(c)
	if c.Fetch != nil {
		go c.Refresh(context.Background())
	}
	if notice != "" {
		log.Printf("UPDATE: %s", notice)
	}
	return strings.TrimSpace(notice)
}
