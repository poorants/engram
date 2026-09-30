package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// The remote server admits only a token whose hash it was given, stamps every
// write with its own author whatever the call names, and speaks to the store
// with the server's credential rather than the caller's.
func TestServeAdmitsHashedTokenAndStampsItsAuthor(t *testing.T) {
	var mu sync.Mutex
	var got map[string]any
	var gotToken, gotSession string
	fakeStore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/doc/") {
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&got)
			gotToken = r.Header.Get(brain.TokenHeader)
			gotSession = r.Header.Get(brain.SessionHeader)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"status": "created"})
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeStore.Close()

	s, err := newServeConfig(fakeStore.URL, "store-cred", " "+strings.ToUpper(sha256hex("good"))+" ,", "alice")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(serveHandler("/mcp", s))
	defer ts.Close()

	connect := func(tok string) (*mcp.ClientSession, error) {
		return mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(),
			&mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{tok}}, MaxRetries: -1}, nil)
	}
	if _, err := connect("bad"); err == nil {
		t.Fatal("an unknown token connected")
	}
	cs, err := connect("good")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "brain_put", Arguments: map[string]any{
		"path": "acme/shared/resources/x.md", "body": "# x\n", "note": "t", "author": "someone-else",
	}})
	if err != nil || res.IsError {
		t.Fatalf("brain_put: %v %+v", err, res)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["author"] != "alice" {
		t.Fatalf("author must be the server's, got %v", got["author"])
	}
	if gotToken != "store-cred" {
		t.Fatalf("the store must see the server's credential, got %q", gotToken)
	}
	if gotSession == "" {
		t.Fatal("each MCP session must carry a session id to the store")
	}
}

func TestServeRefusesToStartWithoutTokensOrAuthor(t *testing.T) {
	good := sha256hex("x")
	for name, c := range map[string][2]string{
		"no tokens":  {"", "alice"},
		"not a hash": {"deadbeef", "alice"},
		"no author":  {good, " "},
	} {
		if _, err := newServeConfig("http://127.0.0.1:1", "t", c[0], c[1]); err == nil {
			t.Errorf("%s: started", name)
		}
	}
}

func TestServeHealthzNeedsNoToken(t *testing.T) {
	s, err := newServeConfig("http://127.0.0.1:1", "t", sha256hex("x"), "alice")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	serveHandler("/mcp", s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rec.Code)
	}
}
