#!/usr/bin/env python3
"""Usage feedback — the signal the ranking cannot get from text.

A search knows which chunks match a question. It does not know which of them
answered it, and on a brain that keeps growing that is the difference between a
first page with the answer on it and a first page of things that merely share
its words. This module records the second kind of knowledge and turns it into
one number per document, ``utility``, that search.py folds into its ranking.

Three kinds of vote, each weighted:

    useful   +1.0   explicit — brain_feedback, `engram feedback`, the viewer's button
    noise    -1.0   explicit — the same surfaces, the other button
    opened   +0.3   implicit — the document was fetched right after a search
                    returned it. The model forgets to vote; it does not forget
                    to read. A weak signal, because the wrong document gets
                    opened too — that is exactly the loop this exists to shorten.

Votes decay with a half-life (HALF_LIFE_DAYS), so a document that answered a
lot two years ago and nothing since drifts back to neutral, and a new document
is not permanently behind the old ones. The decay is computed AT QUERY TIME from
the raw votes rather than stored — the same reason the store counts documents
instead of caching the count: a stored number is wrong at some moment, a
computed one never is, and the table is small enough that this is free.

The number is a BONUS in the ranking, never a filter and never a channel of its
own (search.py explains why; the one exception, the remembered channel, is
query-conditioned and explained there). Log-saturated, so ten votes reach the
ceiling and a hundred do not bury everything else: the ranking must stay
explainable by the text first.
"""
from __future__ import annotations

import json
import math

import psycopg

# Vote weights. Changing one changes every historical vote's contribution too,
# which is the intended meaning of a weight — it is not a property of the vote,
# it is a property of how much the ranking trusts that kind of signal today.
WEIGHT = {"useful": 1.0, "noise": -1.0, "opened": 0.3}
KINDS = frozenset(WEIGHT)

# Days for a vote to lose half its weight. Mem0 uses 7 for conversational
# memory; a knowledge document ages far slower than a chat, and 90 keeps a
# document that answered once a quarter from ever going neutral.
HALF_LIFE_DAYS = 90.0

# The bonus reaches its ceiling at this many net useful votes. Beyond it, more
# votes do not move the document — see search.py for the ceiling itself.
SATURATION = 10.0

# The SQL that turns raw votes into one decayed number per document. Embedded
# as a CTE by search.py and run standalone by summary(): one definition, so the
# number a person sees on a document page is the number the ranking used.
UTILITY_CTE = """
    utility AS (
      SELECT f.doc_id,
             sum(CASE f.kind WHEN 'useful' THEN %(w_useful)s
                             WHEN 'noise'  THEN %(w_noise)s
                             WHEN 'opened' THEN %(w_opened)s
                             ELSE 0 END
                 * power(0.5, EXTRACT(EPOCH FROM (now() - f.created_at)) / 86400.0 / %(half_life)s)
             )::float8 AS u
      FROM feedback f
      GROUP BY f.doc_id
    )"""


def utility_params() -> dict:
    """The named parameters UTILITY_CTE needs."""
    return {"w_useful": WEIGHT["useful"], "w_noise": WEIGHT["noise"],
            "w_opened": WEIGHT["opened"], "half_life": HALF_LIFE_DAYS}


def saturate(u: float) -> float:
    """Map a raw utility onto -1..1 with a log curve: sign kept, ceiling at
    SATURATION net votes. The Python twin of the SQL expression in search.py,
    used by tests and by the viewer to show where a document sits."""
    if u == 0:
        return 0.0
    return math.copysign(min(1.0, math.log1p(abs(u)) / math.log1p(SATURATION)), u)


def log_search(conn: psycopg.Connection, q: str, q_lexemes: list[str], author: str,
               tier: int, hits: list[dict], session: str = "") -> int:
    """Record one search and return its id — the handle a later vote names.

    hits is what the caller returned, in rank order; only the coordinates are
    kept (chunk, doc, path, heading_path, score), not the bodies. That is
    enough to attribute a vote to the fragment the voter saw and to measure
    later how often the first tier was enough. session is the caller's
    X-Engram-Session, so that "tier 2 after tier 1 for the same question" can
    be told apart from two people asking the same thing.
    """
    slim = [{"chunk": h.get("chunk_id"), "doc": h.get("doc_id"), "path": h.get("path"),
             "heading_path": h.get("heading_path"), "score": round(float(h.get("score") or 0), 5)}
            for h in hits]
    with conn.cursor() as cur:
        cur.execute(
            "INSERT INTO searches (q, q_tsv, author, tier, hits, session)"
            " VALUES (%s, array_to_tsvector(%s::text[]), %s, %s, %s::jsonb, %s) RETURNING id",
            (q, q_lexemes, author or "", tier, json.dumps(slim, ensure_ascii=False),
             (session or "")[:64]))
        sid = cur.fetchone()[0]
    conn.commit()
    return int(sid)


def chunk_from_search(conn: psycopg.Connection, search_id: int, doc_id: int) -> int | None:
    """Which chunk of doc_id the search actually showed — so a vote can say what
    was in front of the voter. None when the search is unknown or did not
    return that document (a vote is still recorded; it just names no chunk)."""
    with conn.cursor() as cur:
        cur.execute("SELECT hits FROM searches WHERE id = %s", (search_id,))
        row = cur.fetchone()
    if not row:
        return None
    for h in row[0] or []:
        if h.get("doc") == doc_id and h.get("chunk"):
            return int(h["chunk"])
    return None


def search_exists(conn: psycopg.Connection, search_id: int) -> bool:
    with conn.cursor() as cur:
        cur.execute("SELECT 1 FROM searches WHERE id = %s", (search_id,))
        return cur.fetchone() is not None


def record(conn: psycopg.Connection, doc_id: int, kind: str, author: str = "",
           note: str = "", search_id: int | None = None,
           chunk_id: int | None = None) -> str:
    """Store one vote. Returns 'recorded', or 'already' when this person has
    already cast this kind of vote on this document for this search today
    (feedback_one_per_question)."""
    if kind not in KINDS:
        raise ValueError(f"unknown feedback kind {kind!r} — one of {sorted(KINDS)}")
    with conn.cursor() as cur:
        cur.execute(
            "INSERT INTO feedback (doc_id, chunk_id, search_id, kind, author, note)"
            " VALUES (%s, %s, %s, %s, %s, %s)"
            " ON CONFLICT (doc_id, author, kind, (COALESCE(search_id, 0)),"
            " ((timezone('UTC', created_at))::date)) DO NOTHING"
            " RETURNING id",
            (doc_id, chunk_id, search_id, kind, author or "", note or ""))
        row = cur.fetchone()
    conn.commit()
    return "recorded" if row else "already"


def summary(conn: psycopg.Connection, doc_id: int) -> dict:
    """What a document has earned: raw counts per kind, the decayed utility, and
    where that puts it on the -1..1 curve the ranking uses."""
    with conn.cursor() as cur:
        cur.execute("SELECT kind, count(*) FROM feedback WHERE doc_id = %s GROUP BY kind",
                    (doc_id,))
        counts = {k: 0 for k in KINDS}
        for k, n in cur.fetchall():
            counts[k] = int(n)
        cur.execute("WITH " + UTILITY_CTE + " SELECT u FROM utility WHERE doc_id = %(doc)s",
                    {**utility_params(), "doc": doc_id})
        row = cur.fetchone()
    u = float(row[0]) if row else 0.0
    return {**counts, "utility": round(u, 3), "curve": round(saturate(u), 3)}
