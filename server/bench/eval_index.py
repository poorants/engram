#!/usr/bin/env python3
"""Measure the index against the grep baseline **with the same ruler.**

Matched for fairness:
  - the same questions (questions.jsonl) and the same gold documents
  - recall@5 measured on the top 5 DISTINCT documents among the returned chunks,
    which is the equivalent of grep's five files
  - tokens counted as what a caller actually consumes — the sum of the returned
    chunk bodies (for grep it was the sum of whole file sizes)

One asymmetry is left in and named: grep can stop reading the moment it hits the
answer, which is knowledge it would not have had in advance, while the index
returns its top K in one shot and cannot stop early. **That favours the
baseline**, so it stays.

By default this goes through `/api/search`, the same path a real client takes,
which means it needs nothing but the store's URL and works against a remote one.
`--dsn` queries the database directly instead — the mode to use when measuring
the vector channel, which is not part of the default deployment and needs a
pgvector image.

    python bench/eval_index.py --url http://localhost:8081 --prefix acme/shared
    python bench/eval_index.py --dsn postgresql://engram:...@localhost:5432/engram vec

Tiers are measured the same way with `--tier N`. Two numbers are reported per
question then: `tok`, the text a caller reads (bodies, or snippets at tiers 1
and 3), and `wire`, the whole JSON hit list — what a model's context actually
pays for. The pass mark tightens with the tier: recall@5 >= 80% at <= 600 wire
tokens for tier 1, >= 90% at <= 1,500 for tier 2.

`--vote-gold` is the lock-in check for usage feedback. It runs every question,
votes the gold documents "useful" for HALF of them (the even-indexed ones)
through the same /api/feedback an agent uses, then runs everything again and
reports the two halves separately. The voted half is expected to rise; the
unvoted half must not fall — a document that answers one question must not
start crowding out the answers to others. It needs the token, and it leaves the
votes in the store, so run it against a bench store, not a real one.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).parent
QS = HERE / "questions.jsonl"
BASE = HERE / "baseline_grep.json"

_CJK = re.compile(r"[가-힣ᄀ-ᇿ㄰-㆏぀-ヿ一-鿿]")

# The pass mark per tier: (min recall@5, max wire tokens per question). The
# untiered call keeps its original mark, measured on bodies.
PASS = {None: (0.90, 3000), 1: (0.80, 600), 2: (0.90, 1500), 3: (0.90, 2500)}


def est_tokens(t: str) -> int:
    """Token estimate for mixed text. An approximation — what matters is the
    RATIO between the two paths, and that holds under any tokenizer."""
    c = len(_CJK.findall(t))
    return int(c * 0.9 + (len(t) - c) / 4)


def http_json(url: str, token: str, method: str = "GET", body: dict | None = None) -> dict:
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("X-Engram-Token", token)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)


def http_search(url: str, token: str, q: str, limit: int, archives: bool,
                tier: int | None) -> dict:
    params = {"q": q, "author": "bench"}
    if tier is None:
        params["limit"] = str(limit)
        if archives:
            params["archives"] = "true"
    else:
        params["tier"] = str(tier)
        # The tier decides the page; only an explicit archives=true overrides it.
        if archives:
            params["archives"] = "true"
    full = url.rstrip("/") + "/api/search?" + urllib.parse.urlencode(params)
    return http_json(full, token)


def dsn_search(conn, q: str, limit: int, archives: bool, use_vector: bool,
               tier: int | None) -> dict:
    from search import search, search_tier  # imported lazily: the HTTP path needs no psycopg
    if tier is None:
        hits = search(q, limit=limit, conn=conn, use_vector=use_vector, include_archives=archives)
        return {"hits": [h.as_dict() for h in hits], "search_id": None}
    res = search_tier(q, tier, conn=conn, include_archives=archives or None)
    return {"hits": res.payload(q), "search_id": None}


def text_of(hit: dict) -> str:
    return hit.get("body") or hit.get("snippet") or ""


def main() -> int:
    ap = argparse.ArgumentParser(description="measure engram's search against the grep baseline")
    ap.add_argument("--url", default="http://127.0.0.1:8081", help="store URL (default mode)")
    ap.add_argument("--token", default="", help="the store token (reads are closed by default)")
    ap.add_argument("--dsn", help="query the database directly instead of the HTTP API")
    ap.add_argument("--prefix", default="acme/shared",
                    help="the document root the corpus was imported under, stripped before comparing")
    ap.add_argument("--topk", type=int, default=6, help="results requested per question (untiered)")
    ap.add_argument("--tier", type=int, choices=(1, 2, 3), help="measure one tier of the budget")
    ap.add_argument("--vote-gold", action="store_true",
                    help="vote gold useful for half the questions and re-measure (needs --token)")
    ap.add_argument("vec", nargs="?", help="with --dsn, also run the vector channel")
    args = ap.parse_args()

    use_vector = args.vec == "vec"
    if use_vector and not args.dsn:
        print("the vector channel can only be measured with --dsn", file=sys.stderr)
        return 2
    if args.vote_gold and (args.dsn or not args.token):
        print("--vote-gold goes through /api/feedback and needs --url and --token", file=sys.stderr)
        return 2

    prefix = args.prefix.strip("/") + "/"

    def norm(path: str) -> str:
        """Put a store path and a corpus-relative gold path on the same ruler."""
        return path[len(prefix):] if path.startswith(prefix) else path

    questions = [json.loads(l) for l in QS.read_text(encoding="utf-8").splitlines() if l.strip()]
    base = {}
    if BASE.is_file():
        base = {r["id"]: r for r in json.loads(BASE.read_text(encoding="utf-8"))}

    conn = None
    if args.dsn:
        sys.path.insert(0, str(HERE.parent / "app"))
        import psycopg
        conn = psycopg.connect(args.dsn)

    def run(q: str, archives: bool) -> dict:
        if conn is not None:
            return dsn_search(conn, q, args.topk, archives, use_vector, args.tier)
        return http_search(args.url, args.token, q, args.topk, archives, args.tier)

    try:
        run("warmup", False)          # keep connection setup out of the timings
    except urllib.error.URLError as e:
        print(f"could not reach {args.url}: {e}", file=sys.stderr)
        return 1

    def measure() -> list[dict]:
        rows = []
        for q in questions:
            t0 = time.time()
            res = run(q["q"], bool(q.get("archives")))
            ms = (time.time() - t0) * 1000
            hits = res.get("hits", [])
            gold = set(q["gold"])
            ranked: list[str] = []
            for h in hits:
                hp = norm(h["path"])
                if hp not in ranked:
                    ranked.append(hp)
            top5 = ranked[:5]
            rows.append(dict(
                id=q["id"], type=q["type"], q=q["q"],
                hit1=bool(ranked and ranked[0] in gold),
                hit5=bool(gold & set(top5)),
                tokens=sum(est_tokens(text_of(h)) for h in hits),
                wire=est_tokens(json.dumps(hits, ensure_ascii=False, indent=2)),
                docs=len(ranked), ms=ms, top=top5[:3],
                search_id=res.get("search_id"), hit_paths=[h["path"] for h in hits],
            ))
        return rows

    rows = measure()

    if args.vote_gold:
        voted = {q["id"] for i, q in enumerate(questions) if i % 2 == 0}
        before = {r["id"]: r for r in rows}
        n_votes = 0
        for r in rows:
            if r["id"] not in voted:
                continue
            q = next(x for x in questions if x["id"] == r["id"])
            gold_paths = [p for p in r["hit_paths"] if norm(p) in set(q["gold"])]
            if not gold_paths:
                # The gold was not on the page, so there is nothing to vote on:
                # a vote names a document the searcher saw. It stays a miss.
                continue
            http_json(args.url.rstrip("/") + "/api/feedback", args.token, "POST",
                      {"kind": "useful", "paths": sorted(set(gold_paths)),
                       "search_id": r["search_id"], "author": "bench"})
            n_votes += 1
        rows = measure()
        print(f"[vote-gold] voted the gold useful on {n_votes} of {len(voted)} even-indexed questions, then re-ran\n")
        for label, ids in (("voted half", voted), ("unvoted half", {q["id"] for q in questions} - voted)):
            b5 = sum(before[i]["hit5"] for i in ids)
            a5 = sum(1 for r in rows if r["id"] in ids and r["hit5"])
            b1 = sum(before[i]["hit1"] for i in ids)
            a1 = sum(1 for r in rows if r["id"] in ids and r["hit1"])
            print(f"  {label:13} recall@5 {b5}/{len(ids)} -> {a5}/{len(ids)}   recall@1 {b1}/{len(ids)} -> {a1}/{len(ids)}")
        regressed = [r["id"] for r in rows if r["id"] not in voted and before[r["id"]]["hit5"] and not r["hit5"]]
        if regressed:
            print(f"  LOCK-IN: unvoted questions that lost their answer: {', '.join(regressed)}")
        print()

    if conn is not None:
        conn.close()

    n = len(rows)
    w = sys.stdout.write
    have_base = bool(base)
    w(f"{'id':5} {'type':9} {'@1':>3} {'@5':>3} {'tok':>6} {'wire':>6} {'base':>7} {'x':>5} {'ms':>5}  question\n")
    w("-" * 112 + "\n")
    for r in rows:
        b = base.get(r["id"])
        bt = b["tokens"] if b else 0
        ratio = (bt / max(r["tokens"], 1)) if b else 0
        flag = "" if r["hit5"] else "  <- miss"
        bflag = "" if (not b or b["hit5"]) else "*"
        w(f"{r['id']:5} {r['type']:9} {'O' if r['hit1'] else '.':>3} {'O' if r['hit5'] else '.':>3} "
          f"{r['tokens']:6} {r['wire']:6} {bt:6}{bflag:1} {ratio:5.1f} {r['ms']:5.0f}  {r['q'][:38]}{flag}\n")

    it = sum(r["tokens"] for r in rows)
    wire = sum(r["wire"] for r in rows)
    bt = sum(base[r["id"]]["tokens"] for r in rows if r["id"] in base)
    channel = "lexical + vector" if use_vector else "lexical only"
    page = f"tier {args.tier}" if args.tier else f"top-{args.topk}"
    w(f"\n[{channel}]  {page}, prefix {args.prefix}\n")
    w(f"{'':16}{'grep baseline':>16}{'index':>12}\n")
    if have_base:
        w(f"{'recall@1':16}{sum(base[r['id']]['hit1'] for r in rows if r['id'] in base)/n:>15.0%}"
          f"{sum(r['hit1'] for r in rows)/n:>12.0%}\n")
        w(f"{'recall@5':16}{sum(base[r['id']]['hit5'] for r in rows if r['id'] in base)/n:>15.0%}"
          f"{sum(r['hit5'] for r in rows)/n:>12.0%}\n")
        w(f"{'total tokens':16}{bt:>15,}{it:>12,}\n")
        w(f"{'per question':16}{bt//n:>15,}{it//n:>12,}\n")
    else:
        w(f"{'recall@1':16}{'-':>15}{sum(r['hit1'] for r in rows)/n:>12.0%}\n")
        w(f"{'recall@5':16}{'-':>15}{sum(r['hit5'] for r in rows)/n:>12.0%}\n")
        w(f"{'per question':16}{'-':>15}{it//n:>12,}\n")
        w("\n(run baseline_grep.py first for the comparison columns)\n")
    w(f"{'wire per question':16}{'-':>15}{wire//n:>12,}\n")
    w(f"{'median latency':16}{'-':>15}{sorted(r['ms'] for r in rows)[n//2]:>10.0f}ms\n")

    min_recall, max_tokens = PASS[args.tier]
    budget = "wire" if args.tier else "body"
    w(f"\npass mark: recall@5 >= {min_recall:.0%} at <= {max_tokens:,} {budget} tokens per question\n")
    for t in ("exact", "semantic", "general"):
        g = [r for r in rows if r["type"] == t]
        if g:
            line = f"  {t:9} recall@5 {sum(r['hit5'] for r in g)}/{len(g)}"
            if have_base:
                line += f"  (grep {sum(base[r['id']]['hit5'] for r in g if r['id'] in base)}/{len(g)})"
            w(line + f"   {sum(r['tokens'] for r in g)//len(g):,} tokens, {sum(r['wire'] for r in g)//len(g):,} wire on average\n")

    out = HERE / (f"eval_index_t{args.tier}.json" if args.tier else f"eval_index_k{args.topk}.json")
    out.write_text(json.dumps(rows, ensure_ascii=False, indent=1), encoding="utf-8")
    w(f"\n-> wrote {out.name}\n")

    spent = (wire if args.tier else it) // n
    ok = sum(r["hit5"] for r in rows) / n >= min_recall and spent <= max_tokens
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
