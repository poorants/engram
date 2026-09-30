package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The OAuth authorization server `engram serve` carries for its own /mcp —
// the MCP authorization spec's shape: protected-resource metadata, server
// metadata, dynamic client registration, authorization code + PKCE (S256),
// refresh. Who a person is comes from Google; whether they may in comes from
// ENGRAM_SERVE_ALLOW. Google is used to log in and for nothing else.
//
// Everything the server issues is a value signed with ENGRAM_SERVE_KEY — the
// client id, the code, the access and refresh tokens — so there is no table
// to lose on a restart and nothing to migrate. The price is revocation:
// withdrawing every grant is rotating the key (or removing the person from
// the allow-list, which every token check reads). For a store with a handful
// of admitted people that is the right trade.

const (
	oauthCodeTTL     = 5 * time.Minute
	oauthLoginTTL    = 10 * time.Minute
	oauthAccessTTL   = time.Hour
	oauthRefreshTTL  = 90 * 24 * time.Hour // sliding: every refresh issues a new one
	oauthLoginCookie = "engram_login"
	viewerTicketTTL  = time.Minute
)

// Google's endpoints are variables so a test can stand in for them.
var (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

type oauthServer struct {
	issuer       string // https://host[:port], no trailing slash
	key          []byte
	googleID     string
	googleSecret string
	allow        []string // emails (matched when verified) or Google subject ids
	viewer       string   // the store viewer's origin, or "" when it does not sign in here
	http         *http.Client

	mu   sync.Mutex
	used map[string]time.Time // codes already exchanged, until they expire
}

func (o *oauthServer) resource() string { return o.issuer + "/mcp" }

func (o *oauthServer) register(mux *http.ServeMux) {
	prm := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 o.resource(),
			"authorization_servers":    []string{o.issuer},
			"bearer_methods_supported": []string{"header"},
		})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", prm)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                o.issuer,
			"authorization_endpoint":                o.issuer + "/oauth/authorize",
			"token_endpoint":                        o.issuer + "/oauth/token",
			"registration_endpoint":                 o.issuer + "/oauth/register",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("POST /oauth/register", o.handleRegister)
	mux.HandleFunc("GET /oauth/authorize", o.handleAuthorize)
	mux.HandleFunc("GET /oauth/google/callback", o.handleGoogleCallback)
	mux.HandleFunc("GET /oauth/viewer", o.handleViewerLogin)
	mux.HandleFunc("POST /oauth/token", o.handleToken)
}

// ── signed values ───────────────────────────────────────────────────────────

// seal signs a claim set under a type, so a value issued as one kind (a
// code) can never be presented as another (an access token).
func (o *oauthServer) seal(typ string, claims map[string]any) string {
	claims["typ"] = typ
	b, _ := json.Marshal(claims)
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, o.key)
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (o *oauthServer) open(typ, v string) (map[string]any, error) {
	p, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, errors.New("malformed")
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, errors.New("malformed")
	}
	m := hmac.New(sha256.New, o.key)
	m.Write([]byte(p))
	if !hmac.Equal(got, m.Sum(nil)) {
		return nil, errors.New("bad signature")
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, errors.New("malformed")
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, errors.New("malformed")
	}
	if c["typ"] != typ {
		return nil, errors.New("wrong kind")
	}
	if exp, ok := c["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
		return nil, errors.New("expired")
	}
	return c, nil
}

func str(c map[string]any, k string) string { s, _ := c[k].(string); return s }

func exp(d time.Duration) int64 { return time.Now().Add(d).Unix() }

// ── who may in ──────────────────────────────────────────────────────────────

// allowed matches an entry with @ against a verified email, and any other
// entry against Google's subject id — the one that survives an email change.
func (o *oauthServer) allowed(sub, email string, verified bool) bool {
	for _, a := range o.allow {
		if strings.Contains(a, "@") {
			if verified && strings.EqualFold(a, email) {
				return true
			}
		} else if a == sub {
			return true
		}
	}
	return false
}

// verifyAccess is the /mcp gate: a live access token for someone still on the
// allow-list. Reading the list on every check is what makes removing a person
// take effect without waiting for their tokens to run out.
func (o *oauthServer) verifyAccess(token string) (sub, email string, expires time.Time, err error) {
	c, err := o.open("at", token)
	if err != nil {
		return "", "", time.Time{}, err
	}
	sub, email = str(c, "sub"), str(c, "email")
	if !o.allowed(sub, email, true) {
		return "", "", time.Time{}, errors.New("no longer allowed")
	}
	e, _ := c["exp"].(float64)
	return sub, email, time.Unix(int64(e), 0), nil
}

// ── registration ────────────────────────────────────────────────────────────

// loopbackRedirect admits only http://localhost / 127.0.0.1 / [::1] — where
// Claude Code listens for the callback. A code can then only ever land on the
// machine that asked for it, so a registration by anyone else is useless.
func loopbackRedirect(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (o *oauthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || len(in.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "redirect_uris is required")
		return
	}
	for _, u := range in.RedirectURIs {
		if !loopbackRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "only loopback http redirects are accepted: "+u)
			return
		}
	}
	id := o.seal("client", map[string]any{"redirect_uris": in.RedirectURIs})
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                in.ClientName,
		"redirect_uris":              in.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

func (o *oauthServer) clientRedirects(clientID string) ([]string, error) {
	c, err := o.open("client", clientID)
	if err != nil {
		return nil, err
	}
	raw, _ := c["redirect_uris"].([]any)
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// ── authorize → Google → callback ───────────────────────────────────────────

func (o *oauthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID, redirect := q.Get("client_id"), q.Get("redirect_uri")
	redirects, err := o.clientRedirects(clientID)
	if err != nil || !contains(redirects, redirect) {
		// Never redirect to an address that was not registered — say it here.
		http.Error(w, "unknown client or redirect_uri", http.StatusBadRequest)
		return
	}
	fail := func(code, desc string) {
		redirectWith(w, r, redirect, url.Values{"error": {code}, "error_description": {desc}, "state": {q.Get("state")}})
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only code")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "PKCE with S256 is required")
		return
	}
	if res := q.Get("resource"); res != "" && strings.TrimRight(res, "/") != o.resource() {
		fail("invalid_target", "this server only issues tokens for "+o.resource())
		return
	}

	o.toGoogle(w, r, map[string]any{
		"client_id": clientID, "redirect_uri": redirect, "state": q.Get("state"),
		"challenge": q.Get("code_challenge"),
	})
}

// handleViewerLogin signs a person into the store's web viewer with the same
// Google login and the same allow-list the MCP clients go through, so the
// viewer needs no Google client of its own. The result is a one-minute
// ticket, signed with the key the viewer shares, handed to the viewer's
// /auth/callback — and only there: `return` must be on the configured viewer
// origin, or this would be an open redirect carrying a login.
func (o *oauthServer) handleViewerLogin(w http.ResponseWriter, r *http.Request) {
	ret := r.URL.Query().Get("return")
	if o.viewer == "" || !strings.HasPrefix(ret, o.viewer+"/") {
		http.Error(w, "return must be on the viewer this server was configured with", http.StatusBadRequest)
		return
	}
	o.toGoogle(w, r, map[string]any{"viewer_return": ret})
}

// toGoogle starts a Google login carrying claims through a signed state.
// The login is bound to this browser: the cookie's value is inside the
// signed state, so a callback that arrives in some other browser (a
// login-CSRF) does not match it.
func (o *oauthServer) toGoogle(w http.ResponseWriter, r *http.Request, claims map[string]any) {
	nonce := randomString()
	http.SetCookie(w, &http.Cookie{Name: oauthLoginCookie, Value: nonce, Path: "/oauth/", MaxAge: int(oauthLoginTTL.Seconds()),
		HttpOnly: true, Secure: strings.HasPrefix(o.issuer, "https://"), SameSite: http.SameSiteLaxMode})
	claims["nonce"], claims["exp"] = nonce, exp(oauthLoginTTL)
	state := o.seal("login", claims)
	g := url.Values{
		"client_id":     {o.googleID},
		"redirect_uri":  {o.issuer + "/oauth/google/callback"},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"state":         {state},
		"prompt":        {"select_account"},
	}
	http.Redirect(w, r, googleAuthURL+"?"+g.Encode(), http.StatusFound)
}

func (o *oauthServer) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	login, err := o.open("login", q.Get("state"))
	if err != nil {
		http.Error(w, "login expired or not started here — start again from Claude Code", http.StatusBadRequest)
		return
	}
	ck, err := r.Cookie(oauthLoginCookie)
	if err != nil || !hmac.Equal([]byte(ck.Value), []byte(str(login, "nonce"))) {
		http.Error(w, "this login was started in another browser", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oauthLoginCookie, Path: "/oauth/", MaxAge: -1})
	if ret := str(login, "viewer_return"); ret != "" {
		o.finishViewerLogin(w, r, ret)
		return
	}
	redirect, clientState := str(login, "redirect_uri"), str(login, "state")
	if e := q.Get("error"); e != "" {
		redirectWith(w, r, redirect, url.Values{"error": {"access_denied"}, "error_description": {"google: " + e}, "state": {clientState}})
		return
	}
	sub, email, verified, err := o.googleIdentity(r, q.Get("code"))
	if err != nil {
		http.Error(w, "google login failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !o.allowed(sub, email, verified) {
		http.Error(w, email+" is not allowed on this store", http.StatusForbidden)
		return
	}
	code := o.seal("code", map[string]any{
		"client_id": str(login, "client_id"), "redirect_uri": redirect, "challenge": str(login, "challenge"),
		"sub": sub, "email": email, "jti": randomString(), "exp": exp(oauthCodeTTL),
	})
	redirectWith(w, r, redirect, url.Values{"code": {code}, "state": {clientState}})
}

func (o *oauthServer) finishViewerLogin(w http.ResponseWriter, r *http.Request, ret string) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Error(w, "google: "+e, http.StatusForbidden)
		return
	}
	sub, email, verified, err := o.googleIdentity(r, q.Get("code"))
	if err != nil {
		http.Error(w, "google login failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !o.allowed(sub, email, verified) {
		http.Error(w, email+" is not allowed on this store", http.StatusForbidden)
		return
	}
	ticket := o.seal("viewer", map[string]any{"sub": sub, "email": email, "jti": randomString(), "exp": exp(viewerTicketTTL)})
	redirectWith(w, r, ret, url.Values{"ticket": {ticket}})
}

// googleIdentity exchanges Google's code for an ID token. The token comes
// straight from Google's token endpoint over TLS, so its claims are taken as
// they are (OIDC Core 3.1.3.7) — issuer and audience are still checked.
func (o *oauthServer) googleIdentity(r *http.Request, code string) (sub, email string, verified bool, err error) {
	form := url.Values{
		"code": {code}, "client_id": {o.googleID}, "client_secret": {o.googleSecret},
		"redirect_uri": {o.issuer + "/oauth/google/callback"}, "grant_type": {"authorization_code"},
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := o.http.Do(req)
	if err != nil {
		return "", "", false, err
	}
	defer res.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tok); err != nil || tok.IDToken == "" {
		return "", "", false, fmt.Errorf("token endpoint: HTTP %d %s", res.StatusCode, tok.Error)
	}
	parts := strings.Split(tok.IDToken, ".")
	if len(parts) != 3 {
		return "", "", false, errors.New("malformed id_token")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false, errors.New("malformed id_token")
	}
	var c struct {
		Iss      string `json:"iss"`
		Aud      string `json:"aud"`
		Sub      string `json:"sub"`
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return "", "", false, errors.New("malformed id_token")
	}
	if (c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com") || c.Aud != o.googleID || c.Sub == "" {
		return "", "", false, errors.New("id_token is not for this client")
	}
	return c.Sub, c.Email, c.Verified, nil
}

// ── token ───────────────────────────────────────────────────────────────────

func (o *oauthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "form body expected")
		return
	}
	f := r.PostForm
	switch f.Get("grant_type") {
	case "authorization_code":
		c, err := o.open("code", f.Get("code"))
		if err != nil || str(c, "client_id") != f.Get("client_id") || str(c, "redirect_uri") != f.Get("redirect_uri") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code is invalid, expired, or not this client's")
			return
		}
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != str(c, "challenge") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match")
			return
		}
		if !o.spend(str(c, "jti")) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
			return
		}
		o.issue(w, str(c, "client_id"), str(c, "sub"), str(c, "email"))
	case "refresh_token":
		c, err := o.open("rt", f.Get("refresh_token"))
		if err != nil || str(c, "client_id") != f.Get("client_id") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or not this client's")
			return
		}
		if !o.allowed(str(c, "sub"), str(c, "email"), true) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "no longer allowed")
			return
		}
		o.issue(w, str(c, "client_id"), str(c, "sub"), str(c, "email"))
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "authorization_code or refresh_token")
	}
}

func (o *oauthServer) issue(w http.ResponseWriter, clientID, sub, email string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  o.seal("at", map[string]any{"sub": sub, "email": email, "exp": exp(oauthAccessTTL)}),
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL.Seconds()),
		"refresh_token": o.seal("rt", map[string]any{"client_id": clientID, "sub": sub, "email": email, "exp": exp(oauthRefreshTTL)}),
	})
}

// spend marks a code used. A code is single-use; the set only has to outlive
// the code itself, and a restart forgetting it is bounded by the same five
// minutes and by PKCE.
func (o *oauthServer) spend(jti string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	for k, t := range o.used {
		if now.After(t) {
			delete(o.used, k)
		}
	}
	if _, dup := o.used[jti]; dup {
		return false
	}
	o.used[jti] = now.Add(oauthCodeTTL)
	return true
}

// ── helpers ─────────────────────────────────────────────────────────────────

func randomString() string {
	var b [24]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func redirectWith(w http.ResponseWriter, r *http.Request, to string, v url.Values) {
	u, _ := url.Parse(to)
	q := u.Query()
	for k, vs := range v {
		if len(vs) > 0 && vs[0] != "" {
			q.Set(k, vs[0])
		}
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code int, e, desc string) {
	writeJSON(w, code, map[string]string{"error": e, "error_description": desc})
}
