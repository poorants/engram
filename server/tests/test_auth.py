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


def load_app(monkeypatch, *, public_reads=False, token=TOKEN):
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
    "/",
    "/search?q=anything",
    "/changes",
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


def test_public_reads_opens_reads(monkeypatch):
    web = load_app(monkeypatch, public_reads=True)
    assert client(web.app).get("/api/search?q=x").status_code != 401


# -- how a rejection is reported ---------------------------------------------

def test_a_browser_is_given_the_login_page_and_a_program_is_given_json(monkeypatch):
    """One body for both would mean one of them parsing an error written for
    the other."""
    web = load_app(monkeypatch)
    c = client(web.app)

    page = c.get("/", headers={"Accept": "text/html"})
    assert page.status_code == 401
    assert "text/html" in page.headers["content-type"]

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
