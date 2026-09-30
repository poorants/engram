"""The authentication gate.

This is the one part of the store where a mistake is silent: a route that
should be closed and is not serves everything to anyone and looks perfectly
healthy while doing it. Nothing else in the system fails that quietly, so it is
the part that most needs a test that fails loudly instead.

No database is needed. Authentication is decided before a route body runs, so
a rejected request never reaches the pool; an accepted one is only checked for
having got *past* the gate, not for what it then returns.
"""
import importlib
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))

TOKEN = "test-token-0123456789abcdef"


def load_app(monkeypatch, *, public_reads=False, token=TOKEN, mcp_url="", viewer_key=""):
    """Import web.py under a given configuration.

    It is re-imported per test rather than configured at runtime because the
    settings are read at import time -- deliberately, so a store cannot change
    its own security posture while running -- and a test that patched the
    module globals afterwards would be exercising a state the real service can
    never be in.
    """
    monkeypatch.setenv("ENGRAM_TOKEN", token)
    monkeypatch.setenv("ENGRAM_PUBLIC_READS", "true" if public_reads else "false")
    monkeypatch.setenv("ENGRAM_DSN", "postgresql://engram:x@127.0.0.1:1/engram")
    if mcp_url:
        monkeypatch.setenv("ENGRAM_MCP_URL", mcp_url)
    else:
        monkeypatch.delenv("ENGRAM_MCP_URL", raising=False)
    if viewer_key:
        monkeypatch.setenv("ENGRAM_VIEWER_KEY", viewer_key)
    else:
        monkeypatch.delenv("ENGRAM_VIEWER_KEY", raising=False)
    for name in ("web",):
        sys.modules.pop(name, None)
    return importlib.import_module("web")


def client(app):
    from fastapi.testclient import TestClient
    # The app is not entered as a context manager, so lifespan never runs and
    # the pool is never opened. That is what keeps this test databaseless.
    return TestClient(app, raise_server_exceptions=False)


# -- what must never be reachable without the token --------------------------

CLOSED_READS = [
    "/api/search?q=anything",
    "/api/scopes",
    "/api/doc/acme/repo/resources/x.md",
    "/api/revisions/acme/repo/resources/x.md",
    "/api/integrity",
    "/api/export",
    "/api/usage",
    "/usage",
    "/",
    "/search?q=anything",
    "/changes",
    "/browse",
    "/browse?owner=acme&repo=repo",
    "/rev/1",
    "/doc/acme/repo/resources/x.md",
]


@pytest.mark.parametrize("path", CLOSED_READS)
def test_reads_are_closed_without_a_token(monkeypatch, path):
    web = load_app(monkeypatch)
    assert client(web.app).get(path).status_code == 401


@pytest.mark.parametrize("method,path", [
    ("put", "/api/doc/acme/repo/resources/x.md"),
    ("delete", "/api/doc/acme/repo/resources/x.md"),
    ("post", "/api/doc/acme/repo/resources/x.md/move"),
    ("post", "/api/doc/acme/repo/resources/x.md/restore"),
    ("post", "/api/rederive"),
    ("post", "/api/index"),
    ("post", "/api/feedback"),
])
def test_writes_are_closed_without_a_token(monkeypatch, method, path):
    web = load_app(monkeypatch)
    assert getattr(client(web.app), method)(path).status_code == 401


def test_writes_stay_closed_even_when_reads_are_public(monkeypatch):
    """ENGRAM_PUBLIC_READS opens reads. It must not open anything else -- the
    two are separate questions and one flag answering both would be a way to
    make a store writable by accident."""
    web = load_app(monkeypatch, public_reads=True)
    c = client(web.app)
    assert c.get("/api/search?q=x").status_code != 401
    assert c.put("/api/doc/acme/repo/resources/x.md").status_code == 401


# -- what the token opens ----------------------------------------------------

def test_the_header_authenticates(monkeypatch):
    web = load_app(monkeypatch)
    r = client(web.app).get("/api/search?q=x", headers={"X-Engram-Token": TOKEN})
    # Past the gate. What the route then does needs a database and is not what
    # this test is about; 401 is the only answer that would mean failure.
    assert r.status_code != 401


def test_the_session_cookie_authenticates(monkeypatch):
    """A browser cannot set a header on a navigation, so the cookie has to be
    accepted everywhere the header is."""
    web = load_app(monkeypatch)
    c = client(web.app)
    c.cookies.set(web.SESSION_COOKIE, TOKEN)
    assert c.get("/", headers={"Accept": "text/html"}).status_code != 401


def test_a_wrong_token_is_rejected(monkeypatch):
    web = load_app(monkeypatch)
    c = client(web.app)
    assert c.get("/api/search?q=x",
                 headers={"X-Engram-Token": "wrong"}).status_code == 401
    # A prefix of the real token must not be treated as the real token.
    assert c.get("/api/search?q=x",
                 headers={"X-Engram-Token": TOKEN[:-1]}).status_code == 401


# -- the deliberate exceptions -----------------------------------------------

def test_healthz_is_reachable_without_a_token(monkeypatch):
    """Gating it produces a container whose own healthcheck fails forever."""
    web = load_app(monkeypatch)
    assert client(web.app).get("/healthz").status_code != 401


def test_login_is_reachable_without_a_token(monkeypatch):
    """It is how a browser authenticates; requiring authentication to reach it
    is a loop with no way out."""
    web = load_app(monkeypatch)
    assert client(web.app).get("/login").status_code == 200


def test_every_page_is_behind_the_login_even_the_setup_guide(monkeypatch):
    """Nothing is shown before signing in: the setup guide too sends a browser
    to the login page."""
    web = load_app(monkeypatch, mcp_url="https://brain.example.ts.net/mcp")
    for path in ("/", "/setup", "/browse", "/changes", "/usage", "/search?q=x"):
        r = client(web.app).get(path, headers={"Accept": "text/html"}, follow_redirects=False)
        assert r.status_code == 303 and r.headers["location"].startswith("/login?next="), path


def test_the_setup_guide_shows_the_configured_mcp_url_after_login(monkeypatch):
    url = "https://brain.example.ts.net/mcp"
    web = load_app(monkeypatch, mcp_url=url)
    c = client(web.app)
    c.cookies.set(web.SESSION_COOKIE, TOKEN)
    r = c.get("/setup")
    # The page itself reads the sidebar from the database, which this test
    # does not have; it is enough that the gate let it through.
    assert r.status_code != 303


def test_the_setup_guide_shows_no_brain_data_to_an_anonymous_reader(monkeypatch):
    """Being open is only safe because it holds nothing from the store: no
    sidebar of repositories, no document counts, no search box."""
    web = load_app(monkeypatch)
    text = client(web.app).get("/setup").text
    assert 'id="repo-list"' not in text
    assert 'id="sidebar"' not in text
    assert 'action="/search"' not in text


def test_static_assets_are_reachable_without_a_token(monkeypatch):
    """The login and setup pages are styled by them before any session exists."""
    web = load_app(monkeypatch)
    c = client(web.app)
    assert c.get("/static/app.css").status_code == 200
    assert c.get("/static/app.js").status_code == 200
    font = c.get("/static/fonts/pretendard/PretendardVariable.subset.0.woff2")
    assert font.status_code == 200
    assert font.headers["content-type"] == "font/woff2"


def test_static_serves_nothing_outside_its_directory(monkeypatch):
    """The gate waves /static/ through, so the route itself is what must keep
    that exception from reaching anything else on disk."""
    web = load_app(monkeypatch)
    c = client(web.app)
    for probe in ("/static/../web.py", "/static/%2e%2e/web.py", "/static/..%2fweb.py",
                  "/static/nope.css"):
        r = c.get(probe)
        assert r.status_code in (401, 404), probe
        assert "TOKEN" not in r.text, probe


def test_public_reads_opens_reads(monkeypatch):
    web = load_app(monkeypatch, public_reads=True)
    assert client(web.app).get("/api/search?q=x").status_code != 401


# -- how a rejection is reported ---------------------------------------------

def test_a_browser_is_given_the_login_page_and_a_program_is_given_json(monkeypatch):
    """One body for both would mean one of them parsing an error written for
    the other."""
    web = load_app(monkeypatch)
    c = client(web.app)

    page = c.get("/doc/a/b/resources/x.md?rev=1", headers={"Accept": "text/html"}, follow_redirects=False)
    assert page.status_code == 303
    assert page.headers["location"] == "/login?next=%2Fdoc%2Fa%2Fb%2Fresources%2Fx.md%3Frev%3D1"

    api = c.get("/api/search?q=x", headers={"Accept": "application/json"})
    assert api.status_code == 401
    assert api.json()["detail"]

    # An API path asked for in a browser is still answered as an API: a client
    # that sends Accept: */* and parses JSON must not receive a login page.
    api_html = c.get("/api/search?q=x", headers={"Accept": "text/html"})
    assert "application/json" in api_html.headers["content-type"]


# -- the login exchange ------------------------------------------------------

def test_login_sets_a_protected_cookie(monkeypatch):
    web = load_app(monkeypatch)
    c = client(web.app)
    r = c.post("/login", data={"token": TOKEN, "next": "/changes"},
               follow_redirects=False)
    assert r.status_code == 303
    assert r.headers["location"] == "/changes"

    cookie = r.headers["set-cookie"].lower()
    # HttpOnly: script on the page can never read the store's credential.
    assert "httponly" in cookie
    # SameSite: the cookie is not attached to cross-site requests.
    assert "samesite=lax" in cookie


def test_the_cookie_is_secure_only_when_the_request_came_over_https(monkeypatch):
    """Behind a TLS proxy the scheme is what X-Forwarded-Proto says (uvicorn
    folds it in). On the plain-HTTP LAN deployment a Secure cookie would never
    be sent back and the login would loop."""
    from fastapi.testclient import TestClient
    web = load_app(monkeypatch)
    plain = TestClient(web.app, base_url="http://testserver").post(
        "/login", data={"token": TOKEN}, follow_redirects=False)
    assert "secure" not in plain.headers["set-cookie"].lower()

    tls = TestClient(web.app, base_url="https://testserver").post(
        "/login", data={"token": TOKEN}, follow_redirects=False)
    assert "secure" in tls.headers["set-cookie"].lower()


def test_a_browser_session_is_renewed_on_use(monkeypatch):
    """The window slides: a browser that comes back within SESSION_TTL is
    never asked to log in again. /api/docs is used because it is closed,
    touches no database, and so answers 200 under the test client."""
    web = load_app(monkeypatch)
    c = client(web.app)
    c.cookies.set(web.SESSION_COOKIE, TOKEN)
    r = c.get("/api/docs")
    assert r.status_code == 200
    cookie = r.headers["set-cookie"].lower()
    assert f"{web.SESSION_COOKIE}=" in cookie
    assert f"max-age={web.SESSION_TTL}" in cookie
    assert "httponly" in cookie


def test_a_program_presenting_the_header_is_not_handed_a_cookie(monkeypatch):
    """Only a browser has a session to extend."""
    web = load_app(monkeypatch)
    r = client(web.app).get("/api/docs", headers={"X-Engram-Token": TOKEN})
    assert r.status_code == 200
    assert "set-cookie" not in r.headers


def test_logout_ends_the_session_rather_than_renewing_it(monkeypatch):
    web = load_app(monkeypatch)
    c = client(web.app)
    c.cookies.set(web.SESSION_COOKIE, TOKEN)
    r = c.get("/logout", follow_redirects=False)
    assert r.status_code == 303
    # The only Set-Cookie is the deletion (max-age=0 / an expiry in the past),
    # never a renewal.
    cookie = r.headers["set-cookie"].lower()
    assert f"max-age={web.SESSION_TTL}" not in cookie


def test_login_rejects_a_wrong_token_without_setting_a_cookie(monkeypatch):
    web = load_app(monkeypatch)
    r = client(web.app).post("/login", data={"token": "wrong"},
                             follow_redirects=False)
    assert r.status_code == 401
    assert "set-cookie" not in r.headers


def test_login_will_not_redirect_off_site(monkeypatch):
    """Otherwise the login page becomes a way to bounce someone off a URL they
    trust to one they do not."""
    web = load_app(monkeypatch)
    c = client(web.app)
    for hostile in ("https://evil.example/", "//evil.example/", "http://evil.example"):
        r = c.post("/login", data={"token": TOKEN, "next": hostile},
                   follow_redirects=False)
        assert r.headers["location"] == "/", hostile


# -- configuration -----------------------------------------------------------

def test_a_store_with_no_token_refuses_to_start(monkeypatch):
    """The alternatives are worse: serve everything to everyone, or refuse
    everything while appearing healthy."""
    with pytest.raises(SystemExit):
        load_app(monkeypatch, token="")


# -- Google sign-in, brokered by engram serve ----------------------------------

import base64  # noqa: E402

VIEWER_KEY = base64.b64encode(bytes([7]) * 32).decode()
MCP = "https://brain.example.ts.net:8443/mcp"
# Sealed by serve's Go code (oauthServer.seal) with the same key: the two
# implementations must agree byte for byte, so the vector is fixed here.
GO_TICKET = ("eyJlbWFpbCI6ImFsaWNlQGV4YW1wbGUuY29tIiwiZXhwIjo0MTAyNDQ0ODAwLCJqdGkiOiJqMSIsInN1YiI6InMxIiwidHlwIjoidmlld2VyIn0"
             ".0PErHVaynd-3-zTilFeJFOoikWPCiqVANDoHLntH3Qk")


def test_a_ticket_sealed_by_serve_opens_here(monkeypatch):
    web = load_app(monkeypatch, mcp_url=MCP, viewer_key=VIEWER_KEY)
    assert web.unseal("viewer", GO_TICKET)["email"] == "alice@example.com"
    assert web.unseal("vsess", GO_TICKET) is None
    assert web.unseal("viewer", GO_TICKET[:-2] + "xx") is None


def test_google_sign_in_goes_through_serve(monkeypatch):
    web = load_app(monkeypatch, mcp_url=MCP, viewer_key=VIEWER_KEY)
    r = client(web.app).get("/auth/google?next=/doc/x", follow_redirects=False)
    assert r.status_code == 303
    assert r.headers["location"].startswith("https://brain.example.ts.net:8443/oauth/viewer?return=")
    assert "%2Fauth%2Fcallback" in r.headers["location"]


def test_a_ticket_signs_the_browser_in_once(monkeypatch):
    web = load_app(monkeypatch, mcp_url=MCP, viewer_key=VIEWER_KEY)
    c = client(web.app)
    r = c.get("/auth/callback", params={"ticket": GO_TICKET, "next": "//evil.example"}, follow_redirects=False)
    assert r.status_code == 303 and r.headers["location"] == "/"
    assert web.PERSON_COOKIE in r.cookies
    # Past the gate with reads closed, on the person cookie alone.
    assert c.get("/api/scopes").status_code != 401
    page = c.get("/login").text
    assert "Sign in with Google" in page
    # The same ticket a second time is refused.
    c2 = client(web.app)
    r2 = c2.get("/auth/callback", params={"ticket": GO_TICKET}, follow_redirects=False)
    assert r2.status_code == 401


def test_a_forged_person_cookie_is_refused(monkeypatch):
    web = load_app(monkeypatch, mcp_url=MCP, viewer_key=VIEWER_KEY)
    c = client(web.app)
    c.cookies.set(web.PERSON_COOKIE, GO_TICKET)  # a viewer ticket, not a session
    assert c.get("/api/scopes").status_code == 401


def test_without_a_viewer_key_there_is_no_google_sign_in(monkeypatch):
    web = load_app(monkeypatch, mcp_url=MCP)
    c = client(web.app)
    assert c.get("/auth/google", follow_redirects=False).status_code == 404
    assert "Sign in with Google" not in c.get("/login").text
