#!/usr/bin/env python3
"""Usage — what a session cost the model, and whether the tiers paid for it.

Every API call leaves one row in ``calls``: the tool, what it was about, and
how much text crossed the wire. Nothing here is a counter that runs up; it is
a log, and the report is computed from it when asked — the same principle as
the document count, which is counted rather than stored.

The report answers four questions the tiers work was undertaken for:

- how many tokens a session spent in the brain, and on which tools;
- the tier-1 hit rate: of the tier-1 searches, how many were NOT followed by
  the same question at a higher tier in the same session;
- whether votes (brain_feedback) happen at all — a ranking that learns from
  votes nobody casts learns nothing, and the fix is a nudge, not a formula;
- which questions come back session after session. A question asked in three
  sessions is a document nobody wrote yet, or a MOC that does not point at it.

None of this is fed back into the model's context on its own. Telling a
session what it has spent costs tokens on every call to save tokens on some;
the report is for `engram usage` and the viewer's /usage page.
"""
from __future__ import annotations

import json

import psycopg

from core import est_tokens

TOOLS = ("search", "doc", "feedback", "put", "patch", "move", "revisions", "integrity")


def log_call(conn: psycopg.Connection, session: str, tool: str, ref: str,
             payload, author: str = "") -> None:
    """Record one call. payload is the text that crossed the wire for it — the
    response for a read, the request body for a write — as a string or a
    JSON-serialisable value; only its size is kept."""
    text = payload if isinstance(payload, str) else json.dumps(payload, ensure_ascii=False, default=str)
    with conn.cursor() as cur:
        cur.execute(
            "INSERT INTO calls (session, tool, ref, bytes, tokens, author)"
            " VALUES (%s, %s, %s, %s, %s, %s)",
            ((session or "")[:64], tool, (ref or "")[:500],
             len(text.encode("utf-8")), est_tokens(text), author or ""))
    conn.commit()


def report(conn: psycopg.Connection, days: int = 7, session: str | None = None,
           limit: int = 30) -> dict:
    """Per-session totals for the window, the escalation count from the search
    log, and the questions that keep coming back."""
    args = {"days": days, "limit": limit, "session": session or ""}
    only = " AND session = %(session)s" if session else ""
    only_s = " AND s.session = %(session)s" if session else ""
    with conn.cursor() as cur:
        cur.execute(f"""
            SELECT session, max(author), min(created_at), max(created_at), count(*),
                   coalesce(sum(tokens), 0), coalesce(sum(bytes), 0),
                   count(*) FILTER (WHERE tool = 'search'),
                   count(*) FILTER (WHERE tool = 'doc'),
                   count(*) FILTER (WHERE tool = 'feedback'),
                   count(*) FILTER (WHERE tool IN ('put', 'patch', 'move'))
            FROM calls
            WHERE created_at >= now() - make_interval(days => %(days)s){only}
            GROUP BY session
            ORDER BY max(created_at) DESC
            LIMIT %(limit)s""", args)
        sessions = {r[0]: {
            "session": r[0], "author": r[1],
            "first": r[2].isoformat(timespec="minutes"), "last": r[3].isoformat(timespec="minutes"),
            "calls": int(r[4]), "tokens": int(r[5]), "bytes": int(r[6]),
            "searches": int(r[7]), "reads": int(r[8]), "votes": int(r[9]), "writes": int(r[10]),
            "tier1": 0, "escalated": 0,
        } for r in cur.fetchall()}

        # An escalation is the same question, same session, tier 1 first and a
        # higher tier later. Counted per question, so tier 2 then tier 3 is one
        # escalation and one tier-1 miss, not two.
        cur.execute(f"""
            SELECT s.session,
                   count(*) FILTER (WHERE s.tier = 1),
                   count(DISTINCT s.q) FILTER (WHERE s.tier > 1 AND EXISTS (
                       SELECT 1 FROM searches p
                       WHERE p.session = s.session AND p.q = s.q AND p.tier = 1
                         AND p.created_at < s.created_at))
            FROM searches s
            WHERE s.created_at >= now() - make_interval(days => %(days)s){only_s}
            GROUP BY s.session""", args)
        for sess, tier1, escalated in cur.fetchall():
            if sess in sessions:
                sessions[sess]["tier1"] = int(tier1)
                sessions[sess]["escalated"] = int(escalated)

        # Questions asked in more than one session. The session filter does
        # not apply: the point is the recurrence across sessions.
        cur.execute("""
            SELECT q, count(DISTINCT session), count(*)
            FROM searches
            WHERE created_at >= now() - make_interval(days => %(days)s) AND session <> ''
            GROUP BY q HAVING count(DISTINCT session) >= 2
            ORDER BY 2 DESC, 3 DESC LIMIT 10""", args)
        repeated = [{"q": r[0], "sessions": int(r[1]), "times": int(r[2])} for r in cur.fetchall()]

    rows = list(sessions.values())
    for r in rows:
        r["tier1_hit_rate"] = round(1 - r["escalated"] / r["tier1"], 3) if r["tier1"] else None
    t1 = sum(r["tier1"] for r in rows)
    esc = sum(r["escalated"] for r in rows)
    totals = {
        "sessions": len(rows),
        "calls": sum(r["calls"] for r in rows),
        "tokens": sum(r["tokens"] for r in rows),
        "searches": sum(r["searches"] for r in rows),
        "reads": sum(r["reads"] for r in rows),
        "votes": sum(r["votes"] for r in rows),
        "writes": sum(r["writes"] for r in rows),
        "tier1": t1, "escalated": esc,
        "tier1_hit_rate": round(1 - esc / t1, 3) if t1 else None,
    }
    return {"days": days, "sessions": rows, "totals": totals, "repeated": repeated}
