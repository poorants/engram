"""The usage log and its report, against a real database. Same switch as
test_feedback_db.py: ENGRAM_TEST_DSN names a database this may wipe."""
import os
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))
os.environ.setdefault("ENGRAM_OWNERS", "acme")

DSN = os.environ.get("ENGRAM_TEST_DSN", "")
pytestmark = pytest.mark.skipif(not DSN, reason="ENGRAM_TEST_DSN is not set")


@pytest.fixture(scope="module")
def conn():
    import psycopg
    from ingest import ensure_schema
    c = psycopg.connect(DSN)
    ensure_schema(c)
    with c.cursor() as cur:
        cur.execute("TRUNCATE calls, feedback, searches RESTART IDENTITY CASCADE")
    c.commit()
    yield c
    c.close()


def test_the_report_adds_up_per_session_and_counts_escalations(conn):
    import feedback as fb
    import usage
    from search import query_lexemes

    # Session A: one tier-1 search that was enough, one that was widened
    # (tier 1 then tier 2, same question), one read, one vote.
    usage.log_call(conn, "A", "search", "q1", {"hits": ["x" * 400]}, "me")
    usage.log_call(conn, "A", "search", "q2", {"hits": []}, "me")
    usage.log_call(conn, "A", "search", "q2", {"hits": ["y" * 800]}, "me")
    usage.log_call(conn, "A", "doc", "acme/shared/resources/x.md", "z" * 2000, "me")
    usage.log_call(conn, "A", "feedback", "acme/shared/resources/x.md", {"ok": 1}, "me")
    fb.log_search(conn, "q1", query_lexemes("q1"), "me", 1, [], session="A")
    fb.log_search(conn, "q2", query_lexemes("q2"), "me", 1, [], session="A")
    fb.log_search(conn, "q2", query_lexemes("q2"), "me", 2, [], session="A")
    # Session B asks q1 too, at tier 1 only; and writes a document.
    usage.log_call(conn, "B", "search", "q1", {"hits": []}, "you")
    usage.log_call(conn, "B", "put", "acme/shared/resources/y.md", "w" * 4000, "you")
    fb.log_search(conn, "q1", query_lexemes("q1"), "you", 1, [], session="B")
    # An unattributed CLI call.
    usage.log_call(conn, "", "integrity", "", {"counts": {}})

    r = usage.report(conn, days=1)
    by = {s["session"]: s for s in r["sessions"]}
    a, b = by["A"], by["B"]
    assert (a["calls"], a["searches"], a["reads"], a["votes"], a["writes"]) == (5, 3, 1, 1, 0)
    assert (a["tier1"], a["escalated"], a["tier1_hit_rate"]) == (2, 1, 0.5)
    assert a["tokens"] > 700            # 400 + 800 + 2000 chars, at ~4 per token
    assert (b["searches"], b["writes"], b["tier1"], b["escalated"], b["tier1_hit_rate"]) == (1, 1, 1, 0, 1.0)
    assert b["tokens"] >= 1000          # the put's body counts, not its receipt
    assert by[""]["calls"] == 1 and by[""]["tier1_hit_rate"] is None
    t = r["totals"]
    assert (t["sessions"], t["calls"], t["tier1"], t["escalated"]) == (3, 8, 3, 1)
    assert t["tier1_hit_rate"] == pytest.approx(2 / 3, abs=0.001)
    # q1 was asked in two sessions: a document waiting to be written.
    assert [x["q"] for x in r["repeated"]] == ["q1"]
    assert r["repeated"][0]["sessions"] == 2


def test_a_session_filter_narrows_the_rows_but_not_the_repeats(conn):
    import usage
    r = usage.report(conn, days=1, session="B")
    assert [s["session"] for s in r["sessions"]] == ["B"]
    assert r["totals"]["sessions"] == 1
    assert r["repeated"] and r["repeated"][0]["q"] == "q1"


def test_the_window_excludes_old_calls(conn):
    import usage
    with conn.cursor() as cur:
        cur.execute("UPDATE calls SET created_at = now() - interval '40 days' WHERE session = 'B'")
    conn.commit()
    assert "B" not in {s["session"] for s in usage.report(conn, days=7)["sessions"]}
    assert "B" in {s["session"] for s in usage.report(conn, days=90)["sessions"]}
