// Package brain is the HTTP client for an engram store — the Postgres-backed
// document service that holds the knowledge base (see the server/ directory).
//
// The store's contract, mirrored here rather than reinterpreted:
//
//   - Reads (search/get/revisions/integrity) need no token. Writes (put/move)
//     send X-Engram-Token and fail loudly without it — there is no queue and no
//     fallback; an unreachable store is an error, never absorbed.
//   - A document address is <owner>/<repo>/ (the document root — two coordinates
//     the store keeps as columns) plus up to maxDepth segments below it, the
//     first of which is a PARA area — or the file itself, which is how a repo hub
//     MOC (acme/webapp/README.md) is addressed. The store refuses owners outside
//     its allow-list with 403 (see Refused), so knowledge from a repo the store
//     does not admit structurally cannot enter; that refusal is routed back to
//     the caller, who may have a local file brain to use instead (the engram
//     skill's job, not this package's).
//   - Delete is deliberately NOT exposed. The engram contract is "never delete,
//     move to archives"; the store's soft delete stays reachable for operators
//     via curl, not via an agent tool.
//
// The search endpoint is the same /api/search the human viewer uses — one
// ranking for people and agents is the store's core design decision, so this
// client adds no re-ranking of its own.
//
// This package deliberately depends on nothing but the standard library. It
// takes a plain Config rather than reaching for a credential loader: who reads
// the config file is the caller's business, and a transport that knows about
// config formats cannot be reused by anything that stores its settings
// elsewhere.
package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config is everything the client needs. A zero Token is a legitimate read-only
// configuration; a zero BaseURL is not usable and every call says so.
type Config struct {
	// BaseURL is the store origin (scheme://host[:port]). There is deliberately
	// no default: a built-in address is a machine that exists on one network and
	// nowhere else, and a client that silently points at it fails in a way that
	// looks like an outage rather than like a missing setting.
	BaseURL string
	// Token is the store's shared write token (header X-Engram-Token). Empty
	// means read-only: search/get/revisions/integrity work, put/move refuse with
	// the remedy.
	Token string
	// Timeout bounds one request. Zero means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout is the per-request ceiling. The store answers searches in
// milliseconds on a LAN; a longer timeout would only stretch out the verdict
// that the store is down.
const DefaultTimeout = 10 * time.Second

// TokenHeader carries the write token. Reads never send it.
const TokenHeader = "X-Engram-Token"

// ErrNoStore means no store address is configured. It is a setup error, not an
// outage: reporting it as one sends people to look at the network instead of at
// their configuration.
var ErrNoStore = errors.New("no store address configured — run `engram store set <url>` (or set ENGRAM_STORE_URL)")

// Client is a thin wrapper over the store's REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a store client from the resolved configuration. A client with no
// token is a legitimate read-only client; writes then fail with the remedy.
func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		token:   strings.TrimSpace(cfg.Token),
		http:    &http.Client{Timeout: timeout},
	}
}

// BaseURL is the store origin this client talks to (quoted in reports).
func (c *Client) BaseURL() string { return c.baseURL }

// CanWrite reports whether this client carries the write token.
func (c *Client) CanWrite() bool { return c.token != "" }

// Configured reports whether a store address is set at all.
func (c *Client) Configured() bool { return c.baseURL != "" }

// APIError turns a store error into an actionable message.
type APIError struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *APIError) Error() string {
	base := fmt.Sprintf("[store %s %s] HTTP %d", e.Method, e.Path, e.Status)
	switch e.Status {
	case http.StatusUnauthorized:
		return base + " — write token rejected; check ENGRAM_TOKEN / `engram store set --token`. (" + e.Message + ")"
	case http.StatusForbidden:
		return base + " — the store does not admit this path's owner group; knowledge from this repo belongs in a local file brain. (" + e.Message + ")"
	case http.StatusNotFound:
		return base + " — no such document. Paths are <owner>/<repo>/<area>/<name>.md; a repo hub is <owner>/<repo>/README.md. (" + e.Message + ")"
	case http.StatusServiceUnavailable:
		return base + " — the store cannot accept writes (its ingest token is unset, or its database is unreachable). (" + e.Message + ")"
	default:
		return base + " — " + e.Message
	}
}

// TransportError means the store could not be TALKED TO — DNS, TCP, TLS, a
// timeout. It is deliberately a distinct type from APIError, which means the
// store answered (even to decline).
//
// A caller has to tell those apart to behave correctly: a 403 routes a document
// to the local file vault, while an unreachable store must fail loudly and
// divert nothing. If the difference lived only in the message text, a caller
// matching on prose would one day file a network outage as a scope refusal —
// and quietly stop recording what the team writes.
type TransportError struct {
	BaseURL string
	Err     error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("could not reach the store at %s — check the address and that the service is up: %v", e.BaseURL, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// do calls a store API path and decodes the JSON response into out. body is
// sent as JSON when non-nil; withToken attaches the write token.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, withToken bool, out any) error {
	if c.baseURL == "" {
		return ErrNoStore
	}
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	full := c.baseURL + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withToken {
		req.Header.Set(TokenHeader, c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Fail loud: an unreachable store is the answer, not a condition to
		// paper over (there is deliberately no fallback and no queue).
		return &TransportError{BaseURL: c.baseURL, Err: err}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Method: method, Path: path, Message: errMessage(raw)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("could not parse the store's response: %w", err)
	}
	return nil
}

// errMessage digs the human-readable part out of a FastAPI error body
// ({"detail": ...}) or returns the body verbatim.
func errMessage(raw []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err == nil {
		if s, ok := parsed["detail"].(string); ok && s != "" {
			return s
		}
	}
	return strings.TrimSpace(string(raw))
}

// SearchOpts are the /api/search parameters. BoostRepo lifts one repo's
// documents without excluding the rest; OnlyRepos/OnlyOwners restrict — the
// store keeps those two ideas separate on purpose and so does this client.
type SearchOpts struct {
	Query      string
	Limit      int // 1..50; 0 means the store default (6)
	Archives   bool
	BoostRepo  string
	OnlyRepos  []string
	OnlyOwners []string
}

// Search runs the store's one ranking. The result is returned as the store
// shaped it (hits are chunks with heading_path, plus index freshness).
func (c *Client) Search(ctx context.Context, opts SearchOpts) (map[string]any, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("the query is empty")
	}
	q := url.Values{"q": {opts.Query}}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Archives {
		q.Set("archives", "true")
	}
	if r := strings.TrimSpace(opts.BoostRepo); r != "" {
		q.Set("repo", r)
	}
	for _, r := range opts.OnlyRepos {
		if r = strings.TrimSpace(r); r != "" {
			q.Add("only_repo", r)
		}
	}
	for _, o := range opts.OnlyOwners {
		if o = strings.TrimSpace(o); o != "" {
			q.Add("only_owner", o)
		}
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/search", q, nil, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Doc fetches one document — body, outgoing links, backlinks, revisions.
func (c *Client) Doc(ctx context.Context, path string) (map[string]any, error) {
	if err := ValidatePath(path); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/doc/"+escapePath(path), nil, nil, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Revisions lists a document's change history (the store's git log).
func (c *Client) Revisions(ctx context.Context, path string, limit int) (map[string]any, error) {
	if err := ValidatePath(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/revisions/"+escapePath(path), q, nil, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Integrity reports the link graph's health: broken links, orphans, weak nodes.
func (c *Client) Integrity(ctx context.Context, limit int) (map[string]any, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/integrity", q, nil, false, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PutTarget is what a put would do — the validated address and payload facts a
// dry run reports and a real put sends. One builder for both, so the preview
// cannot pass on a write the store-bound path would refuse.
type PutTarget struct {
	Path   string
	Bytes  int
	Note   string
	Author string
}

// PreparePut validates a write without performing it.
func (c *Client) PreparePut(path, body, note, author string) (PutTarget, error) {
	if err := ValidatePath(path); err != nil {
		return PutTarget{}, err
	}
	if strings.TrimSpace(body) == "" {
		return PutTarget{}, fmt.Errorf("the body is empty — an empty body never overwrites a document")
	}
	if strings.TrimSpace(note) == "" {
		return PutTarget{}, fmt.Errorf("note is empty — say in one line why this revision exists (it is the commit message of the history)")
	}
	if !c.Configured() {
		return PutTarget{}, ErrNoStore
	}
	if !c.CanWrite() {
		return PutTarget{}, fmt.Errorf("no write token (read-only) — run `engram store set <url> --token <token>`")
	}
	return PutTarget{Path: path, Bytes: len(body), Note: note, Author: author}, nil
}

// Put writes one document (upsert: the store creates or replaces, keeping the
// previous body in revisions). Returns the store's answer — status is
// "created", "updated", "unchanged" or "skipped_newer".
func (c *Client) Put(ctx context.Context, path, body, note, author string) (map[string]any, error) {
	target, err := c.PreparePut(path, body, note, author)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	payload := map[string]string{"body": body, "note": target.Note, "author": target.Author}
	if err := c.do(ctx, http.MethodPut, "/api/doc/"+escapePath(path), nil, payload, true, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PrepareMove validates a move without performing it.
func (c *Client) PrepareMove(path, to, author string) (PutTarget, error) {
	if err := ValidatePath(path); err != nil {
		return PutTarget{}, err
	}
	if err := ValidatePath(to); err != nil {
		return PutTarget{}, fmt.Errorf("destination path: %w", err)
	}
	if !c.Configured() {
		return PutTarget{}, ErrNoStore
	}
	if !c.CanWrite() {
		return PutTarget{}, fmt.Errorf("no write token (read-only) — run `engram store set <url> --token <token>`")
	}
	return PutTarget{Path: to, Author: author}, nil
}

// Move relocates a document; the old path stays behind as an alias so existing
// links keep resolving. Archiving IS a move — the target's area becomes
// "archives" — which is why no delete exists in this package.
func (c *Client) Move(ctx context.Context, path, to, author string) (map[string]any, error) {
	target, err := c.PrepareMove(path, to, author)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	payload := map[string]string{"to": to, "author": target.Author}
	if err := c.do(ctx, http.MethodPost, "/api/doc/"+escapePath(path)+"/move", nil, payload, true, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Health is the store's liveness answer.
type Health struct {
	OK      bool `json:"ok"`
	Indexed bool `json:"indexed"`
	Docs    int  `json:"docs"`
}

// Healthz asks the store whether it can reach its database.
func (c *Client) Healthz(ctx context.Context) (Health, error) {
	var out Health
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, nil, false, &out); err != nil {
		return Health{}, err
	}
	return out, nil
}

// Scope is one owner/repo coordinate the store holds documents for.
type Scope struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Docs  int    `json:"docs"`
}

// Scopes reports which owner groups the store accepts and which coordinates it
// already holds.
//
// AllowedOwners is the confidentiality boundary, and it is the store's answer,
// not a guess: a caller standing in a repo the store does not admit needs to
// know BEFORE writing that its documents belong in a local file vault instead.
// Deriving that from a 403 works too, but only after the attempt.
type Scopes struct {
	AllowedOwners []string `json:"allowed_owners"`
	Present       []Scope  `json:"present"`
}

// StoreScopes fetches the store's accepted owners and present coordinates.
func (c *Client) StoreScopes(ctx context.Context) (Scopes, error) {
	var out Scopes
	if err := c.do(ctx, http.MethodGet, "/api/scopes", nil, nil, false, &out); err != nil {
		return Scopes{}, err
	}
	return out, nil
}

// Refused reports whether an error is the store declining a path's owner group
// — the signal a caller turns into "write this to the local file brain", as
// opposed to a failure it should surface. Kept here so no caller has to match
// on a status code or, worse, on message text.
func Refused(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden
}

// Unreachable reports whether an error means the store could not be talked to,
// or answered that it is broken. Deliberately NOT the same as Refused: an
// unreachable store is a failure to report loudly, never a reason to write
// somewhere else — a queued or diverted write is how a team ends up believing a
// fact is recorded when it is not.
//
// It is also deliberately NOT "any error that is not a refusal". A validation
// error (an empty note, a malformed path) never touched the network, and
// reporting it as an outage sends the caller looking at the VPN instead of at
// the argument they got wrong. ErrNoStore is likewise a setup error, not an
// outage.
func Unreachable(err error) bool {
	var transport *TransportError
	if errors.As(err, &transport) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status >= 500
}

// tokenProbePath is deliberately not a legal document address. The store checks
// the write token BEFORE it looks at the address, so this request comes back
// 401 when the token is wrong and 400 when the token was fine and only the path
// was not — which is exactly the question, answered without writing anything.
const tokenProbePath = "engram-token-probe"

// VerifyToken proves the write token is accepted, without creating a document.
//
// It exists because "the store is up" and "I can write to it" are different
// facts, and a setup check that only proves the first one lets someone finish
// with a read-only install believing they are done. The first thing they then
// notice is a save that fails at the end of a session, which is the worst
// possible moment to find out.
func (c *Client) VerifyToken(ctx context.Context) error {
	if !c.Configured() {
		return ErrNoStore
	}
	if !c.CanWrite() {
		return fmt.Errorf("no write token — run `engram store set <url> --token <token>`")
	}
	payload := map[string]string{"body": "engram token probe", "note": "token probe", "author": "engram"}
	err := c.do(ctx, http.MethodPut, "/api/doc/"+tokenProbePath, nil, payload, true, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// The address was refused, which means the token was not: the store
		// only reaches its address rules after the credential check.
		return nil
	}
	if err != nil {
		return err
	}
	// A success here would mean the store accepted a document at an address its
	// own rules forbid. Report it rather than call the check passed.
	return fmt.Errorf("the store accepted a write at an invalid address (%q) — it is not enforcing its own path rules", tokenProbePath)
}
