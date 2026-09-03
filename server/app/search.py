#!/usr/bin/env python3
"""Search — two channels (body lexemes, title/path) fused with RRF.

RRF (Reciprocal Rank Fusion) is used because the channels' score scales are not
comparable. Even two ts_rank values have different distributions per channel, and
a weighted sum needs normalization constants tuned by hand — constants that go
wrong as soon as the corpus changes. Ranks alone do not have that problem.

The semantic (vector) channel is **off by default.** Measured with and without,
recall was identical and the failing questions were the same set, so the
production image does not even carry the vector extension. Turn it on with
use_vector=True only to re-measure in the bench, where a pgvector image is used.
"""
from __future__ import annotations

import os
import sys
from dataclasses import dataclass, asdict
from pathlib import Path

import psycopg

sys.path.insert(0, str(Path(__file__).parent))
from core import lexemes  # noqa: E402

DSN = os.environ.get("ENGRAM_DSN", "postgresql://engram:engram@127.0.0.1:5433/engram")
MODEL = os.environ.get("ENGRAM_MODEL", "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2")
RRF_K = 60
POOL = 40          # candidates each channel contributes

# The bonus given to the repo the caller is standing in. **A bonus, not a hard
# filter**: if another repo already solved the same problem, that answer must
# still appear, even below yours. A filter declares that other repos' knowledge
# does not exist, and that is the very problem of knowledge trapped per repo.
#
# The value: 1/(RRF_K+8) — "being in my scope is worth as much as ranking 8th in
# one channel". It is an explainable starting point rather than a measured one;
# re-measure once several repos are in and real failures accumulate. Tightening
# it on a guess makes things quietly worse.
REPO_BONUS = 1.0 / (RRF_K + 8)

_te = None


def embed(text: str) -> list[float]:
    global _te
    if _te is None:
        from fastembed import TextEmbedding
        _te = TextEmbedding(model_name=MODEL)
    return list(map(float, next(iter(_te.embed([text])))))


@dataclass
class Hit:
    chunk_id: int
    doc_id: int
    ord: int
    path: str
    title: str
    area: str
    owner: str
    repo: str
    heading_path: str
    body: str
    score: float
    lex_rank: int | None
    vec_rank: int | None

    def as_dict(self) -> dict:
        return asdict(self)


# Function words dropped from a QUERY. They are still indexed — the index keeps
# everything, so an identifier that happens to contain one is still findable.
#
# They are dropped here because the query ORs its lexemes with equal weight and
# ts_rank carries no corpus-level IDF, so a function word matching in a long
# document outranks the single occurrence of the rare term that actually
# discriminates. It is worst in the title/path channel, which is weighted 1.6x
# precisely because a title match is supposed to be high signal: without this,
# the question "what value is RRF_K set to" pulls up a document titled "Why a
# hit is a chunk" on the strength of the word "is", and the one document that
# answers it does not appear at all. (Searching "RRF_K" alone found it at rank 1,
# which is how the cause was isolated.)
#
# The list is deliberately English function words only. Korean and other CJK
# grammar attaches to the word and is indexed as syllable bigrams, so nothing
# here touches it.
STOPWORDS = frozenset("""
a an the this that these those it its there here
is are was were be been being am do does did done doing have has had having
can could should would will shall may might must
of to in on at by for from with without into about over under as
and or but not no nor if then than so too very just only also
i me my we our you your they them their he she his her him
what which who whom whose why how when where whether
""".split())


def to_tsquery(q: str) -> str:
    """Break the query into lexemes and OR them. Using the SAME function as the
    indexer for the lexemes themselves is the whole point — see STOPWORDS for the
    one thing that differs, and why it is a query-side decision only.

    If every term is a function word the original set is kept: a query of nothing
    but common words is still better answered than not at all.
    """
    lex = lexemes(q)
    kept = [t for t in lex if t not in STOPWORDS]
    return " | ".join("'" + t.replace("'", "''") + "'" for t in (kept or lex)) or "'zzzz'"


def search(q: str, limit: int = 6, include_archives: bool = False,
           conn: psycopg.Connection | None = None, use_vector: bool = False,
           boost_repo: str | None = None, only_repos: list[str] | None = None,
           only_owners: list[str] | None = None) -> list[Hit]:
    """boost_repo: lift this repo's documents (never exclude the others).
    only_repos / only_owners: restrict to them — for when isolation is genuinely
    what is wanted."""
    own = conn is None
    conn = conn or psycopg.connect(DSN)
    try:
        tsq = to_tsquery(q)
        # A soft-deleted document appears in no channel. It is kept so it can be
        # restored, not so it can be found.
        area_filter = "AND d.deleted_at IS NULL"
        if not include_archives:
            area_filter += " AND d.area <> 'archives'"
        if only_repos:
            area_filter += " AND d.repo = ANY(%(repos)s)"
        if only_owners:
            area_filter += " AND d.owner = ANY(%(owners)s)"
        # Named parameters, not positional: conditional fragments are spliced
        # in, and with positional ones the %s order shifts every time a CTE is
        # switched on or off.
        args: dict = {"tsq": tsq, "limit": limit, "repos": only_repos or [],
                      "owners": only_owners or [],
                      "boost": boost_repo or "", "bonus": REPO_BONUS}

        # Body lexeme channel.
        ctes = [f"""
            lexical AS (
              SELECT c.id, row_number() OVER (ORDER BY ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.id) AS r
              FROM chunks c JOIN docs d ON d.id = c.doc_id
              WHERE c.tsv @@ %(tsq)s::tsquery {area_filter}
              ORDER BY ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.id LIMIT {POOL}
            )"""]

        # Title/path channel. Pick the document first, then one best-matching
        # chunk from it. This is where grep was strong, so it stands as its own
        # axis and carries a higher RRF weight.
        ctes.append(f"""
            titled AS (
              SELECT DISTINCT ON (d.id) c.id, d.id AS did,
                     ts_rank(d.tsv, %(tsq)s::tsquery) AS dscore
              FROM docs d JOIN chunks c ON c.doc_id = d.id
              WHERE d.tsv @@ %(tsq)s::tsquery {area_filter}
              ORDER BY d.id, ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.ord
            ),
            title_ranked AS (
              SELECT id, row_number() OVER (ORDER BY dscore DESC, did) AS r
              FROM titled ORDER BY dscore DESC LIMIT {POOL}
            )""")

        if use_vector:
            vec = str(embed(("query: " if "e5" in MODEL.lower() else "") + q))
            ctes.append(f"""
            vectorial AS (
              SELECT c.id, row_number() OVER (ORDER BY c.embedding <=> %(vec)s::vector, c.id) AS r
              FROM chunks c JOIN docs d ON d.id = c.doc_id
              WHERE c.embedding IS NOT NULL {area_filter}
              ORDER BY c.embedding <=> %(vec)s::vector, c.id LIMIT {POOL}
            )""")
            args["vec"] = vec
            fused = f"""
            fused AS (
              SELECT COALESCE(l.id, v.id, t.id) AS id,
                     (COALESCE(1.0/({RRF_K}+l.r), 0)
                   + COALESCE(1.0/({RRF_K}+v.r), 0)
                   + COALESCE(1.6/({RRF_K}+t.r), 0))::float8 AS score,
                     l.r AS lex_rank, v.r AS vec_rank
              FROM lexical l
              FULL OUTER JOIN vectorial v    ON v.id = l.id
              FULL OUTER JOIN title_ranked t ON t.id = COALESCE(l.id, v.id)
            )"""
        else:
            fused = f"""
            fused AS (
              SELECT COALESCE(l.id, t.id) AS id,
                     (COALESCE(1.0/({RRF_K}+l.r), 0)
                   + COALESCE(1.6/({RRF_K}+t.r), 0))::float8 AS score,
                     l.r AS lex_rank, NULL::bigint AS vec_rank
              FROM lexical l
              FULL OUTER JOIN title_ranked t ON t.id = l.id
            )"""

        # The repo bonus is applied AFTER fusion. As a channel it would surface
        # documents that do not match the query at all, purely for being in the
        # caller's repo — a bonus shifts the order, it does not manufacture
        # candidates.
        sql = ("WITH " + ",".join(ctes) + "," + fused + """
            SELECT f.id, c.doc_id, c.ord, d.path, d.title, d.area, d.owner, d.repo,
                   c.heading_path, c.body,
                   (f.score + CASE WHEN d.repo = %(boost)s THEN %(bonus)s ELSE 0 END)::float8,
                   f.lex_rank, f.vec_rank
            FROM fused f
            JOIN chunks c ON c.id = f.id
            JOIN docs d   ON d.id = c.doc_id
            ORDER BY 11 DESC, f.id
            LIMIT %(limit)s""")

        with conn.cursor() as cur:
            cur.execute(sql, args)
            return [Hit(*row) for row in cur.fetchall()]
    finally:
        if own:
            conn.close()


if __name__ == "__main__":
    argv = [a for a in sys.argv[1:] if a != "vec"]
    query = " ".join(argv) or "how are documents addressed"
    for i, h in enumerate(search(query, use_vector="vec" in sys.argv), 1):
        loc = f"{h.path}" + (f"  ¶ {h.heading_path}" if h.heading_path else "")
        print(f"\n[{i}] {loc}\n    score={h.score:.4f} lex={h.lex_rank} vec={h.vec_rank}")
        print("    " + h.body[:240].replace("\n", "\n    "))
