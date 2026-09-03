package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/poorants/engram/internal/mcpserver"
	"github.com/poorants/engram/pkg/config"
	"github.com/poorants/engram/pkg/identity"
)

// instructions is the server-level note the client shows the model once. It
// says the two things that change behaviour and are not obvious from any single
// tool's description: search before grep, and an unreachable store is an answer
// rather than a reason to go looking somewhere older.
const instructions = `engram is a networked PARA knowledge brain shared across repos.

Search it BEFORE grepping the working tree for anything that is knowledge rather
than code — decisions, conventions, traps, runbooks, past investigations. It
returns chunks with their heading path, so an answer costs a fraction of what a
file sweep does.

Document addresses are <owner>/<repo>/<area>/<name>.md, where area is one of
projects|areas|resources|archives; a repo hub MOC is <owner>/<repo>/README.md.
Take owner and repo from the working repo's git origin — never invent them.
Knowledge that goes stale when one repo's code changes takes that repo's
coordinate; knowledge that must outlive any one repo (contracts between repos,
manuals, conventions) goes to <owner>/shared/.

If the store is unreachable, reads and writes both fail on the spot. There is no
cache and no queue. Say the store is down rather than answering from something
older than the question.`

func runMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	storeURL := fs.String("store", "", "store address (default: the configured one)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	// The MCP transport owns stdout — a stray line there corrupts the protocol
	// framing — so diagnostics go to stderr, which the client surfaces as
	// server logs.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("engram: ")

	cfg := config.Load()
	if u := strings.TrimSpace(*storeURL); u != "" {
		normalized, err := config.NormalizeURL(u)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return exitError
		}
		cfg.StoreURL = normalized
	}

	// A missing address does NOT stop the server. The tools register either way
	// and each call fails with the remedy, because a server that refuses to
	// start shows up in the client as "engram failed" with no hint of which of
	// the dozen possible causes it was.
	if cfg.StoreURL == "" {
		log.Printf("WARNING: no store address configured — every tool call will fail until `engram store set <url>` is run")
	} else {
		log.Printf("store %s (%s)", cfg.StoreURL, map[bool]string{true: "read/write", false: "read-only"}[cfg.Token != ""])
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: "engram", Version: resolveVersion()},
		&mcp.ServerOptions{Instructions: instructions},
	)
	// Identity is resolved ONCE, here, and shared by every tool — the same
	// resolver the CLI builds, so a document written from a session and one
	// written from a hook carry the same byline.
	mcpserver.Register(server, cfg.Brain(), identity.New(cfg.Author, nil).Author)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("server stopped: %v", err)
		return exitError
	}
	return exitOK
}
