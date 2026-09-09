"""Feedback against a real database: the log, the votes, and what they do to
the ranking.

Needs ENGRAM_TEST_DSN pointing at a database THIS TEST MAY WIPE — it truncates
every table at the start of the session. Skipped when the variable is unset,
so the databaseless suite stays databaseless:

    docker run -d --name engram-test-db -e POSTGRES_USER=engram \\
      -e POSTGRES_PASSWORD=engram -e POSTGRES_DB=engram_test -p 127.0.0.1:5434:5432 postgres:17
    ENGRAM_TEST_DSN=postgresql://engram:engram@127.0.0.1:5434/engram_test pytest tests/test_feedback_db.py
"""
import os
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))
os.environ.setdefault("ENGRAM_OWNERS", "acme")

DSN = os.environ.get("ENGRAM_TEST_DSN", "")
pytestmark = pytest.mark.skipif(not DSN, reason="ENGRAM_TEST_DSN is not set")

DOCS = {
    "acme/shared/resources/token-rotation.md":
        "# Rotating the store token\n\nRun `engram store set` with the new token on every "
        "machine. The old token stops working the moment the server restarts.\n",
    "acme/shared/resources/token-format.md":
        "# What the token looks like\n\nThe token is 48 hex characters from `openssl rand -hex 24`. "
        "Rotation is a separate procedure.\n",
    "acme/shared/resources/unrelated.md":
        "# Log growth\n\nDocker's default is unbounded log growth; the limits are per service.\n",
}


@pytest.fixture(scope="module")
def conn():
    import psycopg
    from ingest import ensure_schema, write_doc
    c = psycopg.connect(DSN)
    ensure_schema(c)
    with c.cursor() as cur:
        cur.execute("TRUNCATE feedback, searches, links, aliases, chunks, revisions, docs RESTART IDENTITY CASCADE")
    c.commit()
    for path, body in DOCS.items():
        write_doc(c, path, body, author="test", note="seed")
    yield c
    c.close()


def paths(hits):
    return [h.path.split("/")[-1] for h in hits]


def test_a_search_is_logged_with_its_hits(conn):
    import feedback as fb
    from search import query_lexemes, search_tier
    res = search_tier("how do I rotate the token", 1, conn=conn)
    sid = fb.log_search(conn, "how do I rotate the token",
                        query_lexemes("how do I rotate the token"), "tester", 1,
                        [h.as_dict() for h in res.hits])
    with conn.cursor() as cur:
        cur.execute("SELECT q, author, tier, hits FROM searches WHERE id = %s", (sid,))
        q, author, tier, hits = cur.fetchone()
    assert (q, author, tier) == ("how do I rotate the token", "tester", 1)
    assert hits[0]["path"].endswith("token-rotation.md")
    assert "body" not in hits[0]           # coordinates only, never the text


def test_a_vote_is_one_per_person_per_question_per_day(conn):
    import feedback as fb
    from search import query_lexemes
    with conn.cursor() as cur:
        cur.execute("SELECT id FROM docs WHERE path LIKE '%%unrelated.md'")
        doc = cur.fetchone()[0]
    assert fb.record(conn, doc, "useful", author="a") == "recorded"
    assert fb.record(conn, doc, "useful", author="a") == "already"
    assert fb.record(conn, doc, "useful", author="b") == "recorded"
    assert fb.record(conn, doc, "noise", author="a") == "recorded"
    # The same document answering a DIFFERENT question is a second fact.
    s1 = fb.log_search(conn, "q one", query_lexemes("q one"), "a", 1, [])
    s2 = fb.log_search(conn, "q two", query_lexemes("q two"), "a", 1, [])
    assert fb.record(conn, doc, "useful", author="a", search_id=s1) == "recorded"
    assert fb.record(conn, doc, "useful", author="a", search_id=s1) == "already"
    assert fb.record(conn, doc, "useful", author="a", search_id=s2) == "recorded"
    s = fb.summary(conn, doc)
    assert (s["useful"], s["noise"], s["opened"]) == (4, 1, 0)
    assert s["utility"] == pytest.approx(3.0, abs=0.01)   # 4 - 1, undecayed


def test_an_unknown_kind_is_refused(conn):
    import feedback as fb
    with pytest.raises(ValueError):
        fb.record(conn, 1, "loved")


def test_the_remembered_channel_brings_back_what_answered_a_similar_question(conn):
    """The whole point: a document voted useful for one phrasing surfaces for
    a different phrasing that shares lexemes — even one the text channels
    would rank lower."""
    import feedback as fb
    from search import query_lexemes, search, search_tier
    q1 = "token rotation procedure"
    res = search_tier(q1, 1, conn=conn)
    sid = fb.log_search(conn, q1, query_lexemes(q1), "tester", 1, [h.as_dict() for h in res.hits])
    # Vote for the FORMAT document — the one that is not the obvious answer —
    # so the test proves the channel moves things and not just that the top
    # hit stays on top.
    with conn.cursor() as cur:
        cur.execute("SELECT id FROM docs WHERE path LIKE '%%token-format.md'")
        fmt = cur.fetchone()[0]
    assert fb.record(conn, fmt, "useful", author="voter", search_id=sid) == "recorded"
    hits = search("what is the rotation procedure for a token", conn=conn)
    fmt_hit = next(h for h in hits if h.path.endswith("token-format.md"))
    assert fmt_hit.mem_rank == 1
    assert fmt_hit.utility > 0
    # and a question that shares nothing with the voted one gets no memory
    hits = search("docker log growth", conn=conn)
    assert all(h.mem_rank is None for h in hits)


def test_the_usefulness_bonus_reorders_but_never_manufactures(conn):
    """A heavily voted document must not appear for a question it does not
    match at all — the bonus is applied to candidates, it does not create them."""
    import feedback as fb
    from search import search
    with conn.cursor() as cur:
        cur.execute("SELECT id FROM docs WHERE path LIKE '%%unrelated.md'")
        doc = cur.fetchone()[0]
    for who in "cdefgh":
        fb.record(conn, doc, "useful", author=who)
    hits = search("openssl hex characters", conn=conn)
    assert hits and not any(h.path.endswith("unrelated.md") for h in hits)


def test_opened_is_attributed_to_the_chunk_the_search_showed(conn):
    import feedback as fb
    from search import query_lexemes, search_tier
    q = "what does the token look like"
    res = search_tier(q, 1, conn=conn)
    sid = fb.log_search(conn, q, query_lexemes(q), "reader", 1, [h.as_dict() for h in res.hits])
    top = res.hits[0]
    chunk = fb.chunk_from_search(conn, sid, top.doc_id)
    assert chunk == top.chunk_id
    assert fb.chunk_from_search(conn, sid, 999999) is None
    assert fb.record(conn, top.doc_id, "opened", author="reader", search_id=sid, chunk_id=chunk) == "recorded"
    with conn.cursor() as cur:
        cur.execute("SELECT chunk_id, search_id FROM feedback WHERE doc_id = %s AND kind = 'opened'", (top.doc_id,))
        assert cur.fetchone() == (chunk, sid)


def test_tier_one_cuts_the_page_when_one_answer_stands_out(conn):
    from search import TIERS, search_tier
    res = search_tier("docker log growth limits", 1, conn=conn)
    assert res.tier == 1 and res.next["tier"] in (2, 3)
    assert len(res.hits) <= TIERS[1].limit
    assert paths(res.hits)[0] == "unrelated.md"
    page = res.payload("docker log growth limits")
    assert set(page[0]) == {"path", "heading_path", "score", "snippet"}
