// Package brain is the HTTP client for an engram store — the Postgres-backed
// document service that holds the knowledge base (see the server/ directory).
//
// The store's contract, mirrored here rather than reinterpreted:
//
//   - The store has ONE credential, sent as X-Engram-Token. It answers "may
//     this caller use this store at all" and is not a permission system: it
//     grants reads and writes alike. So the token goes on every request this
//     client makes, not only on writes — a store with reads closed (the
//     default) would otherwise reject a search from a client holding the very
//     credential being asked for. Writes additionally refuse locally when this
//     machine has none, which names the missing setting instead of relaying a
//     401 that reads as a wrong token.
//   - There is no queue and no fallback; an unreachable store is an error,
//     never absorbed.
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

// Config is everything the client needs. A zero BaseURL is not usable and every
// call says so.
type Config struct {
	// BaseURL is the store origin (scheme://host[:port]). There is deliberately
	// no default: a built-in address is a machine that exists on one network and
	// nowhere else, and a client that silently points at it fails in a way that
	// looks like an outage rather than like a missing setting.
	BaseURL string
	// Token is the store's one credential (header X-Engram-Token). Empty is a
	// legitimate configuration but a limited one: writes refuse locally, and
	// reads reach only a store that was deliberately configured to serve them
	// unauthenticated (ENGRAM_PUBLIC_READS).
	Token string
	// Timeout bounds one request. Zero means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout is the per-request ceiling. The store answers searches in
// milliseconds on a LAN; a longer timeout would only stretch out the verdict
// that the store is down.
const DefaultTimeout = 10 * time.Second

// TokenHeader carries the store's credential. Every request sends it when this
// machine has one, because the store may require it for reads as well.
const TokenHeader = "X-Engram-Token"

// ErrNoStore means no store address is configured. It is a setup error, not an
// outage: reporting it as one sends people to look at the network instead of at
// their configuration.
var ErrNoStore = errors.New("no store address configured — run `engram store set <url>` (or set ENGRAM_STORE_URL)")

// ErrNoToken means this machine has no token and the call cannot proceed
// without one. Like ErrNoStore it is a setup error, reported here rather than
// as the store's 401 — a rejection from the store reads as a wrong token and
// sends people to rotate a credential that was never set in the first place.
var ErrNoToken = errors.New("no store token on this machine — run `engram store set <url> --token <t>` (or set ENGRAM_TOKEN)")

// Client is a thin wrapper over the store's REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a store client from the resolved configuration. A client with no
// token can still be constructed; calls that need one fail with the remedy.
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

// CanWrite reports whether this client carries the store's token. It is named
// for what the caller uses it to decide; the token is not write-specific.
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
		return base + " — the store did not accept this machine's token; check ENGRAM_TOKEN / `engram store set --token`. (" + e.Message + ")"
	case http.StatusForbidden:
		return base + " — the store does not admit this path's owner group; knowledge from this repo belongs in a local file brain. (" + e.Message + ")"
	case http.StatusNotFound:
		return base + " — no such document. Paths are <owner>/<repo>/<area>/<name>.md; a repo hub is <owner>/<repo>/README.md. (" + e.Message + ")"
	case http.StatusConflict:
		return base + " — the document does not agree with this patch; nothing was written. Re-read it and re-aim the edit rather than retrying. (" + e.Message + ")"
	case http.StatusServiceUnavailable:
		return base + " — the store is up but its database is not. (" + e.Message + ")"
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
// sent as JSON when non-nil.
//
// requiresToken says the call CANNOT succeed without one, which is what makes
// "this machine has no token" a better error than relaying the store's 401. It
// does not decide whether the header is sent: the token goes on every request
// whenever this machine has one, because the store requires it for reads too
// unless that deployment set ENGRAM_PUBLIC_READS.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, requiresToken bool, out any) error {
	if c.baseURL == "" {
		return ErrNoStore
	}
	// Answer locally rather than relaying the store's 401. "this machine has no
	// token" names what to fix; a rejection from the server reads as the token
	// being wrong, which sends people to rotate one that was never set.
	if requiresToken && c.token == "" {
		return ErrNoToken
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
	if c.token != "" {
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
		return PutTarget{}, ErrNoToken
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

// Edit is ONE addressed change inside a document. Exactly one address form is
// given — a line range, a section, or an anchor — and the store refuses an Edit
// that carries two, rather than deciding which one the caller meant.
//
// Expect is a pointer because "" is a legitimate expectation (the addressed
// range is empty, which is how an insertion verifies) and absent is not the
// same thing. EndLine is a pointer for the mirror-image reason: 0 is a real
// line-range bound to reject, while absent means "no line address here".
type Edit struct {
	// StartLine/EndLine are 1-indexed and EndLine is EXCLUSIVE, so one line n
	// is EndLine n+1 and EndLine == StartLine inserts before line n without
	// replacing anything. A line range MUST carry Expect: a line number is pure
	// position and proves nothing about what is there.
	StartLine int  `json:"start_line,omitempty"`
	EndLine   *int `json:"end_line,omitempty"`
	// Section addresses a heading and everything under it, up to the next
	// heading of the same or shallower depth. It matches the heading text, the
	// raw heading line, or the full heading path search prints ("A > B > C").
	Section string `json:"section,omitempty"`
	// IncludeHeading defaults to true — the heading line is part of the section.
	// False replaces only the prose under it.
	IncludeHeading *bool `json:"include_heading,omitempty"`
	// Anchor is an exact substring that must occur EXACTLY ONCE. Two matches is
	// a refusal, never a choice; that refusal is the whole point of the form.
	Anchor string `json:"anchor,omitempty"`
	// Expect is the literal text the caller believes occupies the addressed
	// range. It is what turns matching from a guess into a proof, and it is
	// text rather than a hash so a caller that cannot run a hash function can
	// still supply it. Trailing newlines are the only tolerated difference.
	Expect *string `json:"expect,omitempty"`
	// Body replaces the addressed range. "" deletes it.
	Body string `json:"body"`
}

// Address reports the one address form this edit uses, for error messages.
func (e Edit) Address() string {
	switch {
	case e.StartLine != 0 || e.EndLine != nil:
		return "line range"
	case e.Section != "":
		return "section"
	case e.Anchor != "":
		return "anchor"
	}
	return ""
}

// PatchRequest is a partial write: several addressed edits, applied together or
// not at all.
type PatchRequest struct {
	// BaseSHA256 is the hash Doc returned for the version the caller read.
	// Optional, and the only guard against an edit that is right about its own
	// range and wrong about the document — because someone else rewrote the
	// rest of it in between.
	BaseSHA256 string `json:"base_sha256,omitempty"`
	Edits      []Edit `json:"edits"`
	Note       string `json:"note"`
	Author     string `json:"author,omitempty"`
	DryRun     bool   `json:"dry_run,omitempty"`
}

// PreparePatch validates a partial write without performing it.
//
// The address rules are checked here as well as in the store for the same
// reason ValidatePath is: a caller gets the rule back instead of a bare 4xx,
// and one obviously-wrong call never costs a round trip. The store remains the
// authority — this mirrors it, it does not reinterpret it.
func (c *Client) PreparePatch(path string, req PatchRequest) (PutTarget, error) {
	if err := ValidatePath(path); err != nil {
		return PutTarget{}, err
	}
	if strings.TrimSpace(req.Note) == "" {
		return PutTarget{}, fmt.Errorf("note is empty — say in one line why this revision exists (it is the commit message of the history)")
	}
	if len(req.Edits) == 0 {
		return PutTarget{}, fmt.Errorf("no edits — a patch with nothing in it changes nothing; use Put to replace a document wholesale")
	}
	bytes := 0
	for i, e := range req.Edits {
		n := i + 1
		forms := 0
		for _, used := range []bool{e.StartLine != 0 || e.EndLine != nil, e.Section != "", e.Anchor != ""} {
			if used {
				forms++
			}
		}
		if forms == 0 {
			return PutTarget{}, fmt.Errorf("edit %d has no address — give start_line+end_line, section, or anchor", n)
		}
		if forms > 1 {
			return PutTarget{}, fmt.Errorf("edit %d carries more than one address; an edit has exactly one", n)
		}
		if e.StartLine != 0 || e.EndLine != nil {
			if e.StartLine < 1 || e.EndLine == nil {
				return PutTarget{}, fmt.Errorf("edit %d: a line address needs start_line (1-indexed) and end_line (EXCLUSIVE — one line n is end_line n+1)", n)
			}
			if *e.EndLine < e.StartLine {
				return PutTarget{}, fmt.Errorf("edit %d: end_line %d is before start_line %d", n, *e.EndLine, e.StartLine)
			}
			if e.Expect == nil {
				return PutTarget{}, fmt.Errorf("edit %d: a line range carries no evidence of what is there, so expect (the literal current text of those lines) is required. Section and anchor addressing may omit it", n)
			}
		}
		bytes += len(e.Body)
	}
	if !c.Configured() {
		return PutTarget{}, ErrNoStore
	}
	if !c.CanWrite() {
		return PutTarget{}, ErrNoToken
	}
	return PutTarget{Path: path, Bytes: bytes, Note: req.Note, Author: req.Author}, nil
}

// Patch changes PART of a document. The store applies every edit or none, and
// records the result as one ordinary revision — so the saving is in the
// transfer, not in the history.
//
// A 409 means the document disagrees with the request (an ambiguous address, an
// expect that does not match, a stale base). That is not a malformed call and
// retrying it unchanged will fail the same way: re-read the document first.
func (c *Client) Patch(ctx context.Context, path string, req PatchRequest) (map[string]any, error) {
	if _, err := c.PreparePatch(path, req); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodPatch, "/api/doc/"+escapePath(path), nil, req, true, &out); err != nil {
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
		return PutTarget{}, ErrNoToken
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
// the token BEFORE it looks at the address, so this request comes back
// 401 when the token is wrong and 400 when the token was fine and only the path
// was not — which is exactly the question, answered without writing anything.
const tokenProbePath = "engram-token-probe"

// VerifyToken proves this machine's token is accepted, without creating a
// document.
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
		return ErrNoToken
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
