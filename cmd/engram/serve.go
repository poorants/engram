package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
//	claude mcp add --transport http --scope user engram https://<host>/mcp
//
// No header: the server is its own OAuth authorization server (oauth.go), so
// Claude Code discovers it, registers, sends the person through a Google
// login once per machine, and keeps and refreshes the token itself.
//
// The server holds the store's one credential (ENGRAM_TOKEN) and writes with
// it. Every revision is stamped with the logged-in person's byline — on this
// surface the author is not something the caller chooses.
//
// Settings (environment — a systemd EnvironmentFile, 600):
//
//	ENGRAM_TOKEN                       the store credential
//	ENGRAM_SERVE_ISSUER                this server's public origin, e.g. https://host.ts.net:8443
//	ENGRAM_SERVE_KEY                   base64 of ≥32 random bytes; signs everything issued.
//	                                   Rotating it signs every client out
//	ENGRAM_SERVE_GOOGLE_CLIENT_ID      a Google OAuth "Web application" client whose
//	ENGRAM_SERVE_GOOGLE_CLIENT_SECRET  redirect URI is <issuer>/oauth/google/callback
//	ENGRAM_SERVE_ALLOW                 who may in: emails and/or Google subject ids, comma-separated
//	ENGRAM_SERVE_AUTHOR                optional: the byline; default the email's local part

const serveUsage = `usage: engram serve [--addr 127.0.0.1:8682] [--store http://127.0.0.1:8081]

The remote MCP server at <issuer>/mcp, with its own OAuth (Google login).
Plain HTTP on a loopback address — TLS belongs to the proxy in front of it
(tailscale serve, Caddy), which must hand it the whole origin: the OAuth
metadata lives at /.well-known/.

Settings come from the environment: ENGRAM_TOKEN, ENGRAM_SERVE_ISSUER,
ENGRAM_SERVE_KEY, ENGRAM_SERVE_GOOGLE_CLIENT_ID, ENGRAM_SERVE_GOOGLE_CLIENT_SECRET,
ENGRAM_SERVE_ALLOW, ENGRAM_SERVE_AUTHOR (optional).
`

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, serveUsage) }
	addr := fs.String("addr", "127.0.0.1:8682", "listen address (plain HTTP; TLS is the proxy's)")
	store := fs.String("store", "http://127.0.0.1:8081", "the store (the app container on this host)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	log.SetFlags(0)
	log.SetPrefix("engram serve: ")

	s, err := newServeConfig(*store, os.Getenv)
	if err != nil {
		log.Print(err)
		return exitError
	}
	srv := &http.Server{Addr: *addr, Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("%s listening on %s for %s/mcp (store %s, %d allowed)", resolveVersion(), *addr, s.oauth.issuer, s.store.BaseURL, len(s.oauth.allow))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		return exitError
	}
	return exitOK
}

type serveConfig struct {
	store  brain.Config
	oauth  *oauthServer
	author string // fixed byline, or "" for the email's local part
}

// newServeConfig refuses to start on anything missing. A server that starts
// without a key, a Google client or an allow-list either admits nobody (a
// silent outage) or — worse — admits on a guess.
func newServeConfig(store string, env func(string) string) (*serveConfig, error) {
	get := func(k string) string { return strings.TrimSpace(env(k)) }
	issuer := strings.TrimRight(get("ENGRAM_SERVE_ISSUER"), "/")
	if u, err := url.Parse(issuer); err != nil || (u.Scheme != "https" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Host == "" || u.Path != "" {
		return nil, fmt.Errorf("ENGRAM_SERVE_ISSUER must be this server's https origin with no path, got %q", issuer)
	}
	key, err := base64.StdEncoding.DecodeString(get("ENGRAM_SERVE_KEY"))
	if err != nil || len(key) < 32 {
		return nil, errors.New("ENGRAM_SERVE_KEY must be base64 of at least 32 random bytes (openssl rand -base64 32)")
	}
	o := &oauthServer{
		issuer:       issuer,
		key:          key,
		googleID:     get("ENGRAM_SERVE_GOOGLE_CLIENT_ID"),
		googleSecret: get("ENGRAM_SERVE_GOOGLE_CLIENT_SECRET"),
		http:         &http.Client{Timeout: 10 * time.Second},
		used:         map[string]time.Time{},
	}
	if o.googleID == "" || o.googleSecret == "" {
		return nil, errors.New("ENGRAM_SERVE_GOOGLE_CLIENT_ID and ENGRAM_SERVE_GOOGLE_CLIENT_SECRET are required")
	}
	for _, a := range strings.Split(get("ENGRAM_SERVE_ALLOW"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			o.allow = append(o.allow, a)
		}
	}
	if len(o.allow) == 0 {
		return nil, errors.New("ENGRAM_SERVE_ALLOW is empty — nobody could log in")
	}
	s := &serveConfig{
		store:  brain.Config{BaseURL: strings.TrimRight(strings.TrimSpace(store), "/"), Token: get("ENGRAM_TOKEN")},
		oauth:  o,
		author: get("ENGRAM_SERVE_AUTHOR"),
	}
	if s.store.Token == "" {
		log.Printf("WARNING: ENGRAM_TOKEN is empty — writes will be refused")
	}
	return s, nil
}

func (s *serveConfig) handler() http.Handler {
	mux := http.NewServeMux()
	s.oauth.register(mux)
	gate := auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.oauth.issuer + "/.well-known/oauth-protected-resource/mcp",
	})
	// The SDK's own DNS-rebinding guard refuses any Host but loopback on a
	// loopback listener, and a proxy that cannot rewrite Host (tailscale
	// serve) then gets 403 on every call. The same guard is kept here, aimed
	// at the one name this server answers to: the issuer's.
	mcpHandler := mcp.NewStreamableHTTPHandler(s.newServer, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	mux.Handle("/mcp", s.onlyIssuerHost(gate(mcpHandler)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	return mux
}

func (s *serveConfig) onlyIssuerHost(next http.Handler) http.Handler {
	u, _ := url.Parse(s.oauth.issuer)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, u.Host) {
			http.Error(w, fmt.Sprintf("Forbidden: this server answers to %s, not %q", u.Host, r.Host), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *serveConfig) verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	sub, email, expires, err := s.oauth.verifyAccess(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}
	return &auth.TokenInfo{UserID: sub, Expiration: expires, Extra: map[string]any{"email": email}}, nil
}

// byline is the fixed author when one is set, else the part of the email
// before @.
func (s *serveConfig) byline(email string) string {
	if s.author != "" {
		return s.author
	}
	e := strings.ToLower(email)
	if i := strings.IndexByte(e, '@'); i >= 0 {
		e = e[:i]
	}
	return e
}

// newServer builds one *mcp.Server per MCP session. The SDK calls it only for
// a request that opens a session, so each editor session gets its own session
// id and the store's usage log keeps them apart, as it does for stdio.
func (s *serveConfig) newServer(r *http.Request) *mcp.Server {
	ti := auth.TokenInfoFromContext(r.Context())
	if ti == nil {
		return nil
	}
	email, _ := ti.Extra["email"].(string)
	author := s.byline(email)
	server := mcp.NewServer(
		&mcp.Implementation{Name: "engram", Version: resolveVersion()},
		&mcp.ServerOptions{Instructions: instructions + "\n\nRemote server: revisions are stamped with " + author +
			" whatever author a call passes."},
	)
	bc := s.store
	bc.Session = newSessionID()
	mcpserver.Register(server, bc, func(context.Context, string) string { return author })
	log.Printf("%s: session %s", email, bc.Session)
	return server
}
