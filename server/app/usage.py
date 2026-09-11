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
                   count(*) FILTER (WHERE tool IN ('put', 'patch', 'move')),
                   -- Tokens per kind of call. No new column and no new table:
                   -- one row per call already carries both `tool` and
                   -- `tokens`, so the split is a GROUP BY the report was
                   -- summing away, not something that has to be collected.
                   coalesce(sum(tokens) FILTER (WHERE tool = 'search'), 0),
                   coalesce(sum(tokens) FILTER (WHERE tool IN ('doc', 'revisions', 'integrity')), 0),
                   coalesce(sum(tokens) FILTER (WHERE tool = 'feedback'), 0),
                   coalesce(sum(tokens) FILTER (WHERE tool IN ('put', 'patch', 'move')), 0)
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
            "tok": {"search": int(r[11]), "read": int(r[12]),
                    "vote": int(r[13]), "write": int(r[14])},
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

    # The sessionless bucket is not a session, and listing it as one is how it
    # gets misread — it sorts in among the sessions, spans days rather than
    # hours, and is usually the biggest row on the page.
    #
    # It is what calls that carried no X-Engram-Session land in: a bare CLI
    # invocation (a terminal, a script, a hook) has no session because one
    # invocation is not an editor session, and giving each its own id would
    # make a hundred one-call "sessions" — worse, not better. A caller that
    # DOES belong to a session can say so by exporting ENGRAM_SESSION, which
    # both the CLI and the MCP server already honour (config.Brain()).
    #
    # So it is pulled out of the list and shown apart, under its own name.
    # It stays inside `totals`: the calls are real usage.
    loose = next((r for r in rows if not r["session"]), None)
    rows = [r for r in rows if r["session"]]
    totals["sessions"] = len(rows)
    return {"days": days, "sessions": rows, "loose": loose,
            "totals": totals, "repeated": repeated}


# -- activity ------------------------------------------------------------------
# The ledger above answers "what did THIS session cost". This answers a
# different question — "is the brain being used at all, and is that going up or
# down" — and the two want different shapes, which is why they are different
# tabs rather than one longer page.
#
# The day view is a contribution grid, because the only thing worth reading off
# a daily series is whether the habit held; a bar chart of 300 days answers that
# worse and costs more pixels. The week and month views are bars, because there
# the question IS the magnitude and its direction, and a grid cannot show either.
#
# **The grid sizes itself to the data.** A fixed 52-week grid on a store that
# started three days ago is 361 empty cells and 3 filled ones, which reads as a
# broken page rather than a young one. So the window runs from the first call
# to today, with a floor (a grid needs a recognisable shape) and a ceiling (a
# year is as far back as this question is ever asked).

GRID_WEEKS_MIN = 12
GRID_WEEKS_MAX = 53


# The four groups are the ledger's columns, and they are what a day's tokens
# are worth splitting by. There is only ONE kind of token in `calls` (see
# log_call: the response for a read, the request body for a write), so "by
# kind" is not a question this table can answer — "by tool" is.
_GROUPS = {
    "search": ("search",),
    "read": ("doc", "revisions", "integrity"),
    "write": ("put", "patch", "move"),
    "vote": ("feedback",),
}


def _bucket_rows(conn: psycopg.Connection, unit: str, since_days: int) -> list[dict]:
    """calls grouped into one time unit. `unit` is trusted — it is never user
    input, only the three literals below."""
    with conn.cursor() as cur:
        cur.execute(f"""
            SELECT date_trunc('{unit}', created_at)::date AS b,
                   count(*), coalesce(sum(tokens), 0),
                   count(DISTINCT session) FILTER (WHERE session <> ''),
                   count(*) FILTER (WHERE tool = 'search'),
                   count(*) FILTER (WHERE tool IN ('put', 'patch', 'move')),
                   tool, coalesce(sum(tokens), 0)
            FROM calls
            WHERE created_at >= now() - make_interval(days => %s)
            GROUP BY GROUPING SETS ((1), (1, 7)) ORDER BY 1""", (since_days,))
        rows, by_tool = {}, {}
        for b, calls, toks, sess, srch, wr, tool, ttoks in cur.fetchall():
            if tool is None:            # the (bucket) set — the totals line
                rows[b] = {"bucket": b, "calls": int(calls), "tokens": int(toks),
                           "sessions": int(sess), "searches": int(srch), "writes": int(wr)}
            else:                       # the (bucket, tool) set — the split
                by_tool.setdefault(b, []).append((tool, int(calls), int(ttoks)))
    for b, row in rows.items():
        split = {g: {"calls": 0, "tokens": 0} for g in _GROUPS}
        for tool, calls, toks in by_tool.get(b, []):
            for g, members in _GROUPS.items():
                if tool in members:
                    split[g]["calls"] += calls
                    split[g]["tokens"] += toks
        row["split"] = [{"name": g, **v} for g, v in split.items() if v["calls"]]
    return list(rows.values())


def _levels(values: list[int]) -> list[int]:
    """GitHub's four shades, cut at quartiles of the NON-EMPTY days.

    Cutting on all days would put every real day in the top bucket while the
    store is young, since the median of mostly-zero is zero. Quartiles of the
    days that happened describe the days that happened.
    """
    live = sorted(v for v in values if v > 0)
    if not live:
        return [0, 0, 0]
    q = lambda p: live[min(len(live) - 1, int(len(live) * p))]  # noqa: E731
    return [max(1, q(0.25)), max(2, q(0.50)), max(3, q(0.75))]


def activity(conn: psycopg.Connection) -> dict:
    """The day grid, plus weekly and monthly bars."""
    import datetime as _dt

    with conn.cursor() as cur:
        cur.execute("SELECT min(created_at)::date, max(created_at)::date FROM calls")
        first, last = cur.fetchone()
    today = _dt.date.today()
    if first is None:
        return {"empty": True, "weeks": [], "weekly": [], "monthly": [],
                "cuts": [0, 0, 0], "first": None, "days": 0, "totals": {}}

    # The grid ends on the Saturday of this week so the last column is whole,
    # and starts on a Sunday — the column is a week, and a week that begins
    # mid-column is not one.
    end = today + _dt.timedelta(days=6 - ((today.weekday() + 1) % 7))
    span_weeks = ((end - first).days // 7) + 1
    weeks = max(GRID_WEEKS_MIN, min(GRID_WEEKS_MAX, span_weeks))
    start = end - _dt.timedelta(days=weeks * 7 - 1)

    daily = {r["bucket"]: r for r in _bucket_rows(conn, "day", (today - start).days + 1)}
    cuts = _levels([r["calls"] for r in daily.values()])

    grid = []
    d = start
    while d <= end:
        col = []
        for _ in range(7):
            row = daily.get(d)
            n = row["calls"] if row else 0
            lvl = 0 if not n else (1 if n <= cuts[0] else 2 if n <= cuts[1] else 3 if n <= cuts[2] else 4)
            col.append({"date": d, "calls": n, "tokens": row["tokens"] if row else 0,
                        "split": row["split"] if row else [],
                        "level": lvl, "future": d > today})
            d += _dt.timedelta(days=1)
        grid.append(col)

    since = (today - first).days + 1
    weekly = _bucket_rows(conn, "week", min(since, 370))
    monthly = _bucket_rows(conn, "month", min(since, 370))
    tot = {
        "calls": sum(r["calls"] for r in daily.values()),
        "tokens": sum(r["tokens"] for r in daily.values()),
        "searches": sum(r["searches"] for r in daily.values()),
        "writes": sum(r["writes"] for r in daily.values()),
        "active_days": len([1 for r in daily.values() if r["calls"]]),
        "busiest": max(daily.values(), key=lambda r: r["calls"]) if daily else None,
    }
    return {"empty": False, "weeks": grid, "weekly": weekly, "monthly": monthly,
            "cuts": cuts, "first": first, "last": last, "days": since,
            "months": _month_labels(grid), "totals": tot}


def _month_labels(grid: list[list[dict]]) -> list[dict]:
    """Where each month starts along the columns, for the strip above the grid.

    A label is placed on the first column whose first day is in a new month,
    and the very first column is skipped unless it happens to start one — a
    label over a column that is mostly the previous month points at the wrong
    place.
    """
    out, seen = [], None
    for i, col in enumerate(grid):
        m = col[0]["date"].strftime("%b")
        if m != seen:
            if seen is not None or col[0]["date"].day <= 7:
                out.append({"col": i, "label": m})
            seen = m
    return out
