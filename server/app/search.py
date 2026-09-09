#!/usr/bin/env python3
"""Search — channels fused with RRF, then a bonus for what has answered before,
served in tiers so that the common case costs a fraction of the worst one.

RRF (Reciprocal Rank Fusion) is used because the channels' score scales are not
comparable. Even two ts_rank values have different distributions per channel, and
a weighted sum needs normalization constants tuned by hand — constants that go
wrong as soon as the corpus changes. Ranks alone do not have that problem.

The channels:

  lexical     body lexemes                                 weight 1.0
  title       title and path — where grep was strong       weight 1.6
  remembered  documents voted useful for a SIMILAR past
              question (feedback joined to the search log) weight 2.0
  vectorial   embeddings; off by default (see below)       weight 1.0

The semantic (vector) channel is **off by default.** Measured with and without,
recall was identical and the failing questions were the same set, so the
production image does not even carry the vector extension. Turn it on with
use_vector=True only to re-measure in the bench, where a pgvector image is used.

After fusion two BONUSES shift the order without manufacturing candidates: the
repo the caller stands in (REPO_BONUS), and how useful the document has proved
(feedback.py). A bonus can only reorder what the text already found; that is
what keeps the ranking explainable by the question.

Tiers (TIERS) are the token budget. Tier 1 is a handful of snippets — enough to
recognise the answer, cheap enough to call on every question. Tier 2 is the
full chunks; tier 3 widens to archives and drops the repo bonus. A caller that
does not see the answer raises the tier with the SAME question rather than
rephrasing, which keeps the search log attributable and costs, in the worst
case, about what a single untiered call used to.
"""
from __future__ import annotations

import math
import os
import re
import sys
from dataclasses import dataclass, asdict
from pathlib import Path

import psycopg

sys.path.insert(0, str(Path(__file__).parent))
from core import lexemes  # noqa: E402
from feedback import SATURATION, UTILITY_CTE, utility_params  # noqa: E402

DSN = os.environ.get("ENGRAM_DSN", "postgresql://engram:engram@127.0.0.1:5433/engram")
MODEL = os.environ.get("ENGRAM_MODEL", "sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2")
RRF_K = 60
POOL = 40          # candidates each channel contributes (a tier may widen it)

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

# The ceiling of the usefulness bonus — and it is small on purpose. RRF scores
# at the top of a page sit a few ten-thousandths apart (1/61 vs 1/63), so any
# bonus on that scale is a lever, not a nudge: measured on the bench, one vote
# with a ceiling of 1/(RRF_K+4) put the voted document first for six
# UNRELATED questions it had merely appeared on. This ceiling is the distance
# between ranks 1 and 4 in one channel: ten net votes move a document up three
# places among near-ties and never across a channel, one vote about one place.
# Popularity that is not about the question must only break ties; the signal
# that IS about the question is the remembered channel, which is a channel and
# can carry real weight because it is gated on the question.
UTIL_BONUS = 1.0 / (RRF_K + 1) - 1.0 / (RRF_K + 4)

# The remembered channel's RRF weight. Above body (1.0) and title (1.6), and
# above their sum at the top: a document someone said ANSWERED this question
# outranks one that merely matches it in both text channels (1/61 + 1.6/61 =
# 0.043 < 2/61 + a body rank). Measured with the bench's lock-in check: at 1.2
# a vote lifted the answer to first place for 4 of 8 re-asked questions, at
# 2.0 for all of them, and in neither case did an unrelated question move —
# the overlap gate below is what makes the weight safe to carry.
REMEMBERED_WEIGHT = 2.0

# How much of the CURRENT question a past one must share to count as similar.
# Without a gate the channel is a trap of RRF's own making: one vote whose
# question shares a single lexeme ("token") is the channel's only candidate,
# so it is rank 1, so it is worth 1.2/(RRF_K+1) — the top of the page, for a
# question it has nothing to do with. Half the lexemes, and at least one, is
# the gate; for CJK text (indexed as syllable bigrams) that means genuinely
# similar phrasing rather than one shared word.
REMEMBERED_MIN_OVERLAP = 0.5

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
    mem_rank: int | None = None     # rank in the remembered channel, if any
    utility: float = 0.0            # the document's decayed feedback (feedback.py)

    def as_dict(self) -> dict:
        return asdict(self)

    def compact(self, q: str, snippet_chars: int) -> dict:
        """The hit as an agent needs it: where, and enough text to recognise
        the answer. Everything else in as_dict() — ids, ranks, owner, repo,
        title — is envelope; measured on a real store it was 35-40% of every
        search result and nothing an agent acts on. snippet_chars=0 sends the
        whole chunk body (tier 2)."""
        out = {"path": self.path, "heading_path": self.heading_path,
               "score": round(self.score, 4)}
        if snippet_chars > 0:
            out["snippet"] = snippet(self.body, q, snippet_chars)
        else:
            out["body"] = self.body
        return out


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


def query_lexemes(q: str) -> list[str]:
    """The lexemes a query searches with: the indexer's, minus function words.
    If every term is a function word the original set is kept: a query of
    nothing but common words is still better answered than not at all."""
    lex = lexemes(q)
    kept = [t for t in lex if t not in STOPWORDS]
    return kept or lex


def to_tsquery(q: str) -> str:
    """Break the query into lexemes and OR them. Using the SAME function as the
    indexer for the lexemes themselves is the whole point — see STOPWORDS for the
    one thing that differs, and why it is a query-side decision only."""
    return " | ".join("'" + t.replace("'", "''") + "'" for t in query_lexemes(q)) or "'zzzz'"


# -- tiers --------------------------------------------------------------------

@dataclass(frozen=True)
class Tier:
    limit: int          # hits returned at most
    pool: int           # candidates per channel
    archives: bool      # archived documents included
    snippet: int        # characters of snippet per hit; 0 = the whole chunk body
    cutoff: float       # drop hits scoring below this fraction of the top hit
    repo_boost: bool    # apply REPO_BONUS
    collapse: bool      # one chunk per document — a page of distinct documents


# The budget per tier. Measured on a 255-document store, an untiered call cost
# 1,500-2,400 tokens; tier 1 is built to cost 300-500 and to be right often
# enough that the second call is the exception.
#
# The numbers come from a sweep on the bench corpus, not from taste:
#   - a cutoff of 0.5 took tier 1 from 94% to 65% recall at every limit. RRF
#     puts a document matched in two channels at about twice the score of one
#     matched in one, so half-of-top cut everything but the double matches.
#     0.35 costs no recall and still trims a page with one clear answer.
#   - collapsing to one chunk per document turned four hits into four
#     DOCUMENTS: 97% recall at ~380 wire tokens, against 94% for four chunks.
#     Tier 1 exists to recognise the right document; three fragments of one
#     document spend the page saying the same thing.
# Tier 2 does not collapse: it is the tier that sends the text, and two chunks
# of the answering document are the answer, not a repetition.
#
# Tier 3 drops the repo bonus on purpose: if two boosted tiers did not find it,
# the answer is likelier to be in another repo, and the boost would keep
# putting this repo's near-misses first.
TIERS: dict[int, Tier] = {
    1: Tier(limit=4, pool=POOL, archives=False, snippet=240, cutoff=0.35, repo_boost=True, collapse=True),
    2: Tier(limit=6, pool=POOL, archives=False, snippet=0, cutoff=0.0, repo_boost=True, collapse=False),
    3: Tier(limit=12, pool=100, archives=True, snippet=240, cutoff=0.0, repo_boost=False, collapse=True),
}


def next_tier(tier: int, shown: int, candidates: int) -> dict | None:
    """What to call next when the answer is not in this page — or None at the
    top. Phrased for the caller that reads it, which is usually a model."""
    if tier >= 3:
        return None
    if candidates == 0:
        return {"tier": 3, "why": "nothing matched outside archives; tier 3 searches "
                                  "archives too and drops the repo boost"}
    if tier == 1:
        return {"tier": 2, "why": f"{shown} of {candidates} candidate documents shown as snippets; "
                                  "tier 2 returns up to 6 full chunks for the same question, "
                                  "tier 3 adds archives and more candidates"}
    return {"tier": 3, "why": f"{shown} of {candidates} candidate chunks shown; tier 3 returns up to "
                              "12 documents, includes archives and drops the repo boost"}


def apply_cutoff(hits: list[Hit], fraction: float) -> list[Hit]:
    """Dynamic k. A page of six where the first scores three times the rest is
    one answer and five distractions; cutting below a fraction of the top score
    sends the answer alone. The top hit always survives."""
    if not hits or fraction <= 0:
        return hits
    floor = hits[0].score * fraction
    return [h for h in hits if h.score >= floor]


_WS = re.compile(r"\s+")


def snippet(body: str, q: str, n: int = 240) -> str:
    """A window of the chunk around the first query term, whitespace folded.

    Not ts_headline: the tsvector holds direct lexemes that never went through
    a parser, and ts_headline would re-parse them. This finds the earliest
    occurrence of any query lexeme (case-insensitive; CJK bigrams work as
    substrings) and centres a window a third of the way in, so the term is
    seen in context. No term in the body — a title-channel hit, say — means the
    opening of the chunk, which is where a section says what it is about.
    """
    flat = _WS.sub(" ", body).strip()
    if len(flat) <= n:
        return flat
    low = flat.lower()
    positions = [p for p in (low.find(t) for t in query_lexemes(q) if len(t) >= 2) if p >= 0]
    start = 0
    if positions:
        start = max(0, min(positions) - n // 3)
        # back up to a word boundary so the window does not open mid-token
        sp = flat.rfind(" ", 0, start)
        if sp > 0 and start - sp < 24:
            start = sp + 1
    end = min(len(flat), start + n)
    if end < len(flat):
        sp = flat.rfind(" ", start, end)
        if sp > start + n // 2:
            end = sp
    out = flat[start:end].strip()
    if start > 0:
        out = "…" + out
    if end < len(flat):
        out += "…"
    return out


# -- the query ----------------------------------------------------------------

def search(q: str, limit: int = 6, include_archives: bool = False,
           conn: psycopg.Connection | None = None, use_vector: bool = False,
           boost_repo: str | None = None, only_repos: list[str] | None = None,
           only_owners: list[str] | None = None, pool: int = POOL,
           collapse: bool = False) -> list[Hit]:
    """boost_repo: lift this repo's documents (never exclude the others).
    only_repos / only_owners: restrict to them — for when isolation is genuinely
    what is wanted. collapse: one chunk per document."""
    hits, _ = search_counted(q, limit, include_archives, conn, use_vector,
                             boost_repo, only_repos, only_owners, pool, collapse)
    return hits


def search_counted(q: str, limit: int = 6, include_archives: bool = False,
                   conn: psycopg.Connection | None = None, use_vector: bool = False,
                   boost_repo: str | None = None, only_repos: list[str] | None = None,
                   only_owners: list[str] | None = None,
                   pool: int = POOL, collapse: bool = False) -> tuple[list[Hit], int]:
    """search(), plus how many fused candidates there were before the limit —
    what tells a caller whether a wider tier has anything more to show
    (chunks, or documents when collapsed)."""
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
        lex = query_lexemes(q)
        args: dict = {"tsq": tsq, "limit": limit, "repos": only_repos or [],
                      "owners": only_owners or [],
                      "boost": boost_repo or "", "bonus": REPO_BONUS,
                      "util_bonus": UTIL_BONUS, "sat": SATURATION,
                      "lex": lex, "need": max(1, math.ceil(len(lex) * REMEMBERED_MIN_OVERLAP)),
                      **utility_params()}

        # Body lexeme channel.
        ctes = [f"""
            lexical AS (
              SELECT c.id, row_number() OVER (ORDER BY ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.id) AS r
              FROM chunks c JOIN docs d ON d.id = c.doc_id
              WHERE c.tsv @@ %(tsq)s::tsquery {area_filter}
              ORDER BY ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.id LIMIT {pool}
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
              FROM titled ORDER BY dscore DESC LIMIT {pool}
            )""")

        # Remembered channel: documents someone voted useful for a question that
        # shares lexemes with this one. It IS a channel — it can surface a
        # document the text channels would not — and that is the point: it is
        # how a person answering "team means group here" once makes the next
        # such question find the document. It is query-conditioned, so it
        # cannot surface a popular document for an unrelated question; the
        # unconditioned signal is the utility bonus below, which only reorders.
        # Within the document, the chunk shown is the best match for THIS
        # question, not the one the earlier voter saw.
        # Similarity is how many of THIS question's lexemes the past one
        # carries (gated by REMEMBERED_MIN_OVERLAP); ties go to the document
        # with more votes for such questions. The GIN index on q_tsv prunes
        # the log to rows sharing at least one lexeme before anything is
        # counted, so the lateral intersection runs on a handful of rows.
        ctes.append(f"""
            remembered_docs AS (
              SELECT f.doc_id, max(ov.n) AS overlap, count(*) AS votes
              FROM feedback f
              JOIN searches s ON s.id = f.search_id
              CROSS JOIN LATERAL (
                SELECT cardinality(ARRAY(
                  SELECT unnest(tsvector_to_array(s.q_tsv))
                  INTERSECT SELECT unnest(%(lex)s::text[]))) AS n
              ) ov
              WHERE f.kind = 'useful' AND s.q_tsv @@ %(tsq)s::tsquery AND ov.n >= %(need)s
              GROUP BY f.doc_id
            ),
            remembered AS (
              SELECT DISTINCT ON (rd.doc_id) c.id, rd.doc_id AS did, rd.overlap, rd.votes
              FROM remembered_docs rd
              JOIN docs d ON d.id = rd.doc_id
              JOIN chunks c ON c.doc_id = d.id
              WHERE true {area_filter}
              ORDER BY rd.doc_id, ts_rank(c.tsv, %(tsq)s::tsquery) DESC, c.ord
            ),
            remembered_ranked AS (
              SELECT id, row_number() OVER (ORDER BY overlap DESC, votes DESC, did) AS r
              FROM remembered ORDER BY overlap DESC, votes DESC LIMIT {pool}
            )""")

        scored = [f"SELECT id, 1.0/({RRF_K}+r) AS s, r AS lex_rank, NULL::bigint AS vec_rank, NULL::bigint AS mem_rank FROM lexical",
                  f"SELECT id, 1.6/({RRF_K}+r), NULL, NULL, NULL FROM title_ranked",
                  f"SELECT id, {REMEMBERED_WEIGHT}/({RRF_K}+r), NULL, NULL, r FROM remembered_ranked"]

        if use_vector:
            vec = str(embed(("query: " if "e5" in MODEL.lower() else "") + q))
            ctes.append(f"""
            vectorial AS (
              SELECT c.id, row_number() OVER (ORDER BY c.embedding <=> %(vec)s::vector, c.id) AS r
              FROM chunks c JOIN docs d ON d.id = c.doc_id
              WHERE c.embedding IS NOT NULL {area_filter}
              ORDER BY c.embedding <=> %(vec)s::vector, c.id LIMIT {pool}
            )""")
            args["vec"] = vec
            scored.append(f"SELECT id, 1.0/({RRF_K}+r), NULL, r, NULL FROM vectorial")

        # Fusion as a UNION and a GROUP BY rather than a chain of FULL OUTER
        # JOINs: adding a channel is one more line, and the arithmetic is the
        # same RRF sum either way.
        ctes.append("scored AS (" + " UNION ALL ".join(scored) + ")")
        ctes.append("""
            fused AS (
              SELECT id, sum(s)::float8 AS score,
                     max(lex_rank) AS lex_rank, max(vec_rank) AS vec_rank, max(mem_rank) AS mem_rank
              FROM scored GROUP BY id
            )""")
        ctes.append(UTILITY_CTE)

        # The bonuses are applied AFTER fusion. As channels they would surface
        # documents that do not match the query at all, purely for being in the
        # caller's repo or for having been useful once — a bonus shifts the
        # order, it does not manufacture candidates. The usefulness bonus is
        # log-saturated: the curve reaches 1 at %(sat)s net votes and stays there.
        ctes.append("""
            ranked AS (
              SELECT f.id, c.doc_id, c.ord, d.path, d.title, d.area, d.owner, d.repo,
                     c.heading_path, c.body,
                     (f.score
                      + CASE WHEN d.repo = %(boost)s THEN %(bonus)s ELSE 0 END
                      + CASE WHEN COALESCE(u.u, 0) = 0 THEN 0
                             ELSE sign(u.u) * least(1.0, ln(1 + abs(u.u)) / ln(1 + %(sat)s)) * %(util_bonus)s
                        END)::float8 AS score,
                     f.lex_rank, f.vec_rank, f.mem_rank, COALESCE(u.u, 0)::float8 AS utility
              FROM fused f
              JOIN chunks c ON c.id = f.id
              JOIN docs d   ON d.id = c.doc_id
              LEFT JOIN utility u ON u.doc_id = d.id
            )""")
        # One chunk per document, when asked: the best-scoring one stands for
        # the document and the rest of its chunks leave the page to others.
        if collapse:
            ctes.append("""
            page AS (
              SELECT DISTINCT ON (doc_id) * FROM ranked ORDER BY doc_id, score DESC, id
            )""")
        else:
            ctes.append("page AS (SELECT * FROM ranked)")

        sql = ("WITH " + ",".join(ctes) + """
            SELECT id, doc_id, ord, path, title, area, owner, repo, heading_path, body,
                   score, lex_rank, vec_rank, mem_rank, utility,
                   count(*) OVER () AS candidates
            FROM page
            ORDER BY score DESC, id
            LIMIT %(limit)s""")

        with conn.cursor() as cur:
            cur.execute(sql, args)
            rows = cur.fetchall()
        hits = [Hit(*row[:-1]) for row in rows]
        candidates = int(rows[0][-1]) if rows else 0
        return hits, candidates
    finally:
        if own:
            conn.close()


@dataclass
class TierResult:
    tier: int
    hits: list[Hit]
    candidates: int
    next: dict | None
    snippet_chars: int

    def payload(self, q: str) -> list[dict]:
        return [h.compact(q, self.snippet_chars) for h in self.hits]


def search_tier(q: str, tier: int, conn: psycopg.Connection | None = None,
                boost_repo: str | None = None, only_repos: list[str] | None = None,
                only_owners: list[str] | None = None, limit: int | None = None,
                include_archives: bool | None = None) -> TierResult:
    """One tier of the budget. limit and include_archives override the tier's
    own when given, so a caller can still ask for exactly what it wants."""
    cfg = TIERS[tier]
    hits, candidates = search_counted(
        q, limit=limit or cfg.limit,
        include_archives=cfg.archives if include_archives is None else include_archives,
        conn=conn, boost_repo=boost_repo if cfg.repo_boost else None,
        only_repos=only_repos, only_owners=only_owners, pool=cfg.pool,
        collapse=cfg.collapse)
    hits = apply_cutoff(hits, cfg.cutoff)
    return TierResult(tier=tier, hits=hits, candidates=candidates,
                      next=next_tier(tier, len(hits), candidates),
                      snippet_chars=cfg.snippet)


if __name__ == "__main__":
    argv = [a for a in sys.argv[1:] if a != "vec"]
    query = " ".join(argv) or "how are documents addressed"
    for i, h in enumerate(search(query, use_vector="vec" in sys.argv), 1):
        loc = f"{h.path}" + (f"  ¶ {h.heading_path}" if h.heading_path else "")
        print(f"\n[{i}] {loc}\n    score={h.score:.4f} lex={h.lex_rank} vec={h.vec_rank} mem={h.mem_rank} util={h.utility:.2f}")
        print("    " + h.body[:240].replace("\n", "\n    "))
