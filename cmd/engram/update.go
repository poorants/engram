package main

import (
	"context"
	"fmt"
	"log"
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
		"engram %s is running and %s is out. Update: run `engram update` "+
			"(the settings, the token and the store are untouched). "+
			"A new session has to be started before the new binary is in use.",
		c.Current, latest)
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
