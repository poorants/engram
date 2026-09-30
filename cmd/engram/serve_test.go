package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/poorants/engram/pkg/brain"
)

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// fakeGoogle answers the token endpoint with an ID token for whichever email
// the test puts in *who. The signature part is junk: the server takes the
// token from the endpoint over TLS and does not verify it.
func fakeGoogle(t *testing.T, clientID string, who *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("client_secret") != "gsecret" || r.PostForm.Get("code") != "gcode" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": clientID, "sub": "sub-" + *who, "email": *who, "email_verified": true})
		idt := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
		json.NewEncoder(w).Encode(map[string]string{"id_token": idt})
	}))
}

type serveFixture struct {
	ts        *httptest.Server
	who       string
	mu        sync.Mutex
	putBody   map[string]any
	putToken  string
	putSesion string
}

func newServeFixture(t *testing.T) *serveFixture {
	f := &serveFixture{who: "alice@example.com"}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/doc/") {
			f.mu.Lock()
			json.NewDecoder(r.Body).Decode(&f.putBody)
			f.putToken, f.putSesion = r.Header.Get(brain.TokenHeader), r.Header.Get(brain.SessionHeader)
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "created"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(store.Close)
	g := fakeGoogle(t, "gid", &f.who)
	t.Cleanup(g.Close)
	old := googleTokenURL
	googleTokenURL = g.URL
	t.Cleanup(func() { googleTokenURL = old })

	f.ts = httptest.NewServer(nil)
	t.Cleanup(f.ts.Close)
	env := map[string]string{
		"ENGRAM_TOKEN":                      "store-cred",
		"ENGRAM_SERVE_ISSUER":               f.ts.URL,
		"ENGRAM_SERVE_KEY":                  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"ENGRAM_SERVE_GOOGLE_CLIENT_ID":     "gid",
		"ENGRAM_SERVE_GOOGLE_CLIENT_SECRET": "gsecret",
		"ENGRAM_SERVE_ALLOW":                "alice@example.com",
	}
	s, err := newServeConfig(store.URL, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	f.ts.Config.Handler = s.handler()
	return f
}

func (f *serveFixture) register(t *testing.T, redirect string) (int, map[string]any) {
	b, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "client_name": "t"})
	res, err := http.Post(f.ts.URL+"/oauth/register", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// login walks authorize → Google → callback and returns the callback
// response, whose Location carries the code or an error.
func (f *serveFixture) login(t *testing.T, clientID, redirect, verifier string) *http.Response {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"cs"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {f.ts.URL + "/mcp"}}
	res, err := noFollow.Get(f.ts.URL + "/oauth/authorize?" + q.Encode())
	if err != nil || res.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %v %v", err, res.Status)
	}
	g, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(g.String(), googleAuthURL) || g.Query().Get("client_id") != "gid" {
		t.Fatalf("authorize must send the browser to Google, got %s", g)
	}
	req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/oauth/google/callback?"+url.Values{"code": {"gcode"}, "state": {g.Query().Get("state")}}.Encode(), nil)
	for _, c := range res.Cookies() {
		req.AddCookie(c)
	}
	cb, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return cb
}

func (f *serveFixture) token(t *testing.T, form url.Values) (int, map[string]any) {
	res, err := http.PostForm(f.ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// The whole path Claude Code takes: discover, register, log in through
// Google, exchange with PKCE, call a tool, refresh. The write reaches the
// store with the server's credential and the logged-in person's byline.
func TestServeOAuthEndToEnd(t *testing.T) {
	f := newServeFixture(t)

	res, _ := http.Post(f.ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("an anonymous call must get 401 pointing at the metadata, got %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	var meta map[string]any
	res, _ = http.Get(f.ts.URL + "/.well-known/oauth-authorization-server")
	json.NewDecoder(res.Body).Decode(&meta)
	if meta["registration_endpoint"] != f.ts.URL+"/oauth/register" {
		t.Fatalf("metadata: %v", meta)
	}

	const redirect = "http://localhost:33418/callback"
	code, reg := f.register(t, redirect)
	if code != http.StatusCreated {
		t.Fatalf("register: %d %v", code, reg)
	}
	clientID := reg["client_id"].(string)

	cb := f.login(t, clientID, redirect, "verifier-0123456789-0123456789-0123456789")
	loc, _ := url.Parse(cb.Header.Get("Location"))
	if cb.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), redirect) || loc.Query().Get("state") != "cs" {
		t.Fatalf("callback: %d %s", cb.StatusCode, loc)
	}
	authCode := loc.Query().Get("code")

	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {authCode}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {"wrong"}}
	if code, _ := f.token(t, exchange); code != http.StatusBadRequest {
		t.Fatal("a wrong code_verifier was accepted")
	}
	exchange.Set("code_verifier", "verifier-0123456789-0123456789-0123456789")
	code, tok := f.token(t, exchange)
	if code != http.StatusOK || tok["access_token"] == nil {
		t.Fatalf("token: %d %v", code, tok)
	}
	if code, _ := f.token(t, exchange); code != http.StatusBadRequest {
		t.Fatal("a code was exchanged twice")
	}

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: f.ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok["access_token"].(string)}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "brain_put", Arguments: map[string]any{
		"path": "acme/shared/resources/x.md", "body": "# x\n", "note": "t", "author": "someone-else",
	}})
	if err != nil || r.IsError {
		t.Fatalf("brain_put: %v %+v", err, r)
	}
	f.mu.Lock()
	if f.putBody["author"] != "alice" || f.putToken != "store-cred" || f.putSesion == "" {
		t.Fatalf("store saw author=%v token=%q session=%q", f.putBody["author"], f.putToken, f.putSesion)
	}
	f.mu.Unlock()

	code, again := f.token(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {clientID}})
	if code != http.StatusOK || again["access_token"] == nil {
		t.Fatalf("refresh: %d %v", code, again)
	}
	// A refresh token is not an access token, whatever it is signed with.
	if _, _, _, err := f.tsOAuth(t).verifyAccess(tok["refresh_token"].(string)); err == nil {
		t.Fatal("a refresh token passed as an access token")
	}
}

func (f *serveFixture) tsOAuth(t *testing.T) *oauthServer {
	// The handler's oauthServer is reachable only through the mux; rebuild an
	// identical one from the same key for direct checks.
	return &oauthServer{key: bytes.Repeat([]byte{7}, 32), allow: []string{"alice@example.com"}}
}

func TestServeRefusesSomeoneNotOnTheList(t *testing.T) {
	f := newServeFixture(t)
	f.who = "mallory@example.com"
	const redirect = "http://127.0.0.1:9/cb"
	_, reg := f.register(t, redirect)
	cb := f.login(t, reg["client_id"].(string), redirect, "v")
	if cb.StatusCode != http.StatusForbidden {
		t.Fatalf("a person not on the list got %d", cb.StatusCode)
	}
}

func TestServeRegistersOnlyLoopbackRedirects(t *testing.T) {
	f := newServeFixture(t)
	for _, u := range []string{"https://evil.example/cb", "http://evil.example/cb", "http://localhost.evil.example/cb"} {
		if code, _ := f.register(t, u); code != http.StatusBadRequest {
			t.Errorf("%s: registered (%d)", u, code)
		}
	}
	for _, u := range []string{"http://localhost:33418/callback", "http://127.0.0.1:5/cb", "http://[::1]:5/cb"} {
		if code, _ := f.register(t, u); code != http.StatusCreated {
			t.Errorf("%s: refused (%d)", u, code)
		}
	}
}

func TestServeRefusesToStartIncomplete(t *testing.T) {
	full := map[string]string{
		"ENGRAM_SERVE_ISSUER":               "https://h.example:8443",
		"ENGRAM_SERVE_KEY":                  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"ENGRAM_SERVE_GOOGLE_CLIENT_ID":     "id",
		"ENGRAM_SERVE_GOOGLE_CLIENT_SECRET": "s",
		"ENGRAM_SERVE_ALLOW":                "a@example.com",
	}
	if _, err := newServeConfig("http://127.0.0.1:1", func(k string) string { return full[k] }); err != nil {
		t.Fatalf("a complete config was refused: %v", err)
	}
	for k, bad := range map[string]string{
		"ENGRAM_SERVE_ISSUER":           "http://h.example",
		"ENGRAM_SERVE_KEY":              "c2hvcnQ=",
		"ENGRAM_SERVE_GOOGLE_CLIENT_ID": "",
		"ENGRAM_SERVE_ALLOW":            " , ",
	} {
		env := map[string]string{}
		for kk, v := range full {
			env[kk] = v
		}
		env[k] = bad
		if _, err := newServeConfig("http://127.0.0.1:1", func(k string) string { return env[k] }); err == nil {
			t.Errorf("%s=%q: started", k, bad)
		}
	}
}

// Behind a proxy that keeps the client's Host (tailscale serve), /mcp must
// answer to the issuer's name and to nothing else.
func TestServeMCPAnswersToTheIssuerHostOnly(t *testing.T) {
	env := map[string]string{
		"ENGRAM_SERVE_ISSUER":               "https://h.example:8443",
		"ENGRAM_SERVE_KEY":                  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		"ENGRAM_SERVE_GOOGLE_CLIENT_ID":     "id",
		"ENGRAM_SERVE_GOOGLE_CLIENT_SECRET": "s",
		"ENGRAM_SERVE_ALLOW":                "a@example.com",
	}
	s, err := newServeConfig("http://127.0.0.1:1", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	h := s.handler()
	for host, want := range map[string]int{"h.example:8443": http.StatusUnauthorized, "evil.example": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: got %d, want %d", host, rec.Code, want)
		}
	}
}
