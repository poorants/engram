package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/poorants/engram/internal/mcpserver"
	"github.com/poorants/engram/pkg/brain"
)

// `engram serve` is the brain_* tools over HTTP — the remote MCP server that
// runs next to the store, so a person's machine needs no store address and no
// store credential:
//
//	claude mcp add --transport http --scope user engram https://<host>/mcp \
//	  --header "Authorization: Bearer <token>"
//
// The server holds the store's one credential (ENGRAM_TOKEN) and writes with
// it. Callers authenticate to the server, not to the store, and every revision
// is stamped with the server's author — on this surface the author is not
// something the caller chooses.
//
// Settings (environment — a systemd EnvironmentFile):
//
//	ENGRAM_TOKEN               the store credential
//	ENGRAM_SERVE_TOKEN_SHA256  accepted bearer tokens, as SHA-256 hex, comma-separated.
//	                           Only hashes: the server never holds a caller's token.
//	ENGRAM_SERVE_AUTHOR        the byline every revision written through this server carries
//
// Issue a token: t=$(openssl rand -base64 32); printf %s "$t" | sha256sum

const serveUsage = `usage: engram serve [--addr 127.0.0.1:8682] [--path /mcp] [--store http://127.0.0.1:8081]

The remote MCP server. Plain HTTP on a loopback address — TLS belongs to the
proxy in front of it (tailscale serve, Caddy). Settings come from the
environment: ENGRAM_TOKEN, ENGRAM_SERVE_TOKEN_SHA256, ENGRAM_SERVE_AUTHOR.
`

// serveTokenTTL is how long a verified token is trusted by the SDK before it
// is checked again. Static hashes cannot be revoked without a restart anyway,
// so this only bounds how stale a TokenInfo may get.
const serveTokenTTL = time.Minute

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, serveUsage) }
	addr := fs.String("addr", "127.0.0.1:8682", "listen address (plain HTTP; TLS is the proxy's)")
	path := fs.String("path", "/mcp", "MCP endpoint path as the proxy forwards it")
	store := fs.String("store", "http://127.0.0.1:8081", "the store (the app container on this host)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	log.SetFlags(0)
	log.SetPrefix("engram serve: ")

	s, err := newServeConfig(*store, os.Getenv("ENGRAM_TOKEN"), os.Getenv("ENGRAM_SERVE_TOKEN_SHA256"), os.Getenv("ENGRAM_SERVE_AUTHOR"))
	if err != nil {
		log.Print(err)
		return exitError
	}
	srv := &http.Server{Addr: *addr, Handler: serveHandler(*path, s), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on %s%s (store %s, %d token(s), author %s)", resolveVersion(), *addr, *path, s.store.BaseURL, len(s.hashes), s.author)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		return exitError
	}
	return exitOK
}

type serveConfig struct {
	store  brain.Config
	hashes [][32]byte
	author string
}

// newServeConfig refuses to start without an accepted token or an author. A
// server with neither would either admit nobody (a silent outage) or stamp
// revisions with whatever a caller claims — both look like working until the
// day someone reads the history.
func newServeConfig(store, storeToken, tokenHashes, author string) (*serveConfig, error) {
	s := &serveConfig{
		store:  brain.Config{BaseURL: strings.TrimRight(strings.TrimSpace(store), "/"), Token: strings.TrimSpace(storeToken)},
		author: strings.TrimSpace(author),
	}
	for _, h := range strings.Split(tokenHashes, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("ENGRAM_SERVE_TOKEN_SHA256: %q is not a SHA-256 hex digest", h)
		}
		s.hashes = append(s.hashes, [32]byte(b))
	}
	if len(s.hashes) == 0 {
		return nil, errors.New("ENGRAM_SERVE_TOKEN_SHA256 is empty — no caller could connect")
	}
	if s.author == "" {
		return nil, errors.New("ENGRAM_SERVE_AUTHOR is empty — revisions need a byline the caller does not choose")
	}
	if s.store.Token == "" {
		log.Printf("WARNING: ENGRAM_TOKEN is empty — writes will be refused")
	}
	return s, nil
}

func serveHandler(path string, s *serveConfig) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(path, auth.RequireBearerToken(s.verify, nil)(mcp.NewStreamableHTTPHandler(s.newServer, nil)))
	mux.HandleFunc("GET "+strings.TrimRight(path, "/")+"/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	return mux
}

// verify compares the token's hash against every accepted hash without an
// early exit, so the time taken says nothing about which one came close.
func (s *serveConfig) verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	h := sha256.Sum256([]byte(token))
	ok := 0
	for _, want := range s.hashes {
		ok |= subtle.ConstantTimeCompare(h[:], want[:])
	}
	if ok != 1 {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: s.author, Expiration: time.Now().Add(serveTokenTTL)}, nil
}

// newServer builds one *mcp.Server per MCP session. The SDK calls it only for
// a request that opens a session, so each editor session gets its own session
// id and the store's usage log keeps them apart, as it does for stdio.
func (s *serveConfig) newServer(*http.Request) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "engram", Version: resolveVersion()},
		&mcp.ServerOptions{Instructions: instructions + "\n\nRemote server: revisions are stamped with " + s.author +
			" whatever author a call passes."},
	)
	bc := s.store
	bc.Session = newSessionID()
	author := s.author
	mcpserver.Register(server, bc, func(context.Context, string) string { return author })
	return server
}
