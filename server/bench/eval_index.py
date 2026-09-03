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


def est_tokens(t: str) -> int:
    """Token estimate for mixed text. An approximation — what matters is the
    RATIO between the two paths, and that holds under any tokenizer."""
    c = len(_CJK.findall(t))
    return int(c * 0.9 + (len(t) - c) / 4)


def http_search(url: str, q: str, limit: int, archives: bool) -> list[dict]:
    params = {"q": q, "limit": str(limit)}
    if archives:
        params["archives"] = "true"
    full = url.rstrip("/") + "/api/search?" + urllib.parse.urlencode(params)
    with urllib.request.urlopen(full, timeout=30) as r:
        return json.load(r).get("hits", [])


def dsn_search(conn, q: str, limit: int, archives: bool, use_vector: bool) -> list[dict]:
    from search import search  # imported lazily: the HTTP path needs no psycopg
    return [h.as_dict() for h in
            search(q, limit=limit, conn=conn, use_vector=use_vector, include_archives=archives)]


def main() -> int:
    ap = argparse.ArgumentParser(description="measure engram's search against the grep baseline")
    ap.add_argument("--url", default="http://127.0.0.1:8081", help="store URL (default mode)")
    ap.add_argument("--dsn", help="query the database directly instead of the HTTP API")
    ap.add_argument("--prefix", default="acme/shared",
                    help="the document root the corpus was imported under, stripped before comparing")
    ap.add_argument("--topk", type=int, default=6, help="results requested per question")
    ap.add_argument("vec", nargs="?", help="with --dsn, also run the vector channel")
    args = ap.parse_args()

    use_vector = args.vec == "vec"
    if use_vector and not args.dsn:
        print("the vector channel can only be measured with --dsn", file=sys.stderr)
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

    def run(q: str, archives: bool) -> list[dict]:
        if conn is not None:
            return dsn_search(conn, q, args.topk, archives, use_vector)
        return http_search(args.url, q, args.topk, archives)

    try:
        run("warmup", False)          # keep connection setup out of the timings
    except urllib.error.URLError as e:
        print(f"could not reach {args.url}: {e}", file=sys.stderr)
        return 1

    rows = []
    for q in questions:
        t0 = time.time()
        hits = run(q["q"], bool(q.get("archives")))
        ms = (time.time() - t0) * 1000

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
            tokens=sum(est_tokens(h["body"]) for h in hits),
            docs=len(ranked), ms=ms, top=top5[:3],
        ))
    if conn is not None:
        conn.close()

    n = len(rows)
    w = sys.stdout.write
    have_base = bool(base)
    w(f"{'id':5} {'type':9} {'@1':>3} {'@5':>3} {'tok':>6} {'base':>7} {'x':>5} {'ms':>5}  question\n")
    w("-" * 104 + "\n")
    for r in rows:
        b = base.get(r["id"])
        bt = b["tokens"] if b else 0
        ratio = (bt / max(r["tokens"], 1)) if b else 0
        flag = "" if r["hit5"] else "  <- miss"
        bflag = "" if (not b or b["hit5"]) else "*"
        w(f"{r['id']:5} {r['type']:9} {'O' if r['hit1'] else '.':>3} {'O' if r['hit5'] else '.':>3} "
          f"{r['tokens']:6} {bt:6}{bflag:1} {ratio:5.1f} {r['ms']:5.0f}  {r['q'][:38]}{flag}\n")

    it = sum(r["tokens"] for r in rows)
    bt = sum(base[r["id"]]["tokens"] for r in rows if r["id"] in base)
    channel = "lexical + vector" if use_vector else "lexical only"
    w(f"\n[{channel}]  top-{args.topk}, prefix {args.prefix}\n")
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
    w(f"{'median latency':16}{'-':>15}{sorted(r['ms'] for r in rows)[n//2]:>10.0f}ms\n")

    w(f"\npass mark: recall@5 >= 90% at <= 3,000 tokens per question\n")
    for t in ("exact", "semantic", "general"):
        g = [r for r in rows if r["type"] == t]
        if g:
            line = f"  {t:9} recall@5 {sum(r['hit5'] for r in g)}/{len(g)}"
            if have_base:
                line += f"  (grep {sum(base[r['id']]['hit5'] for r in g if r['id'] in base)}/{len(g)})"
            w(line + f"   {sum(r['tokens'] for r in g)//len(g):,} tokens on average\n")

    out = HERE / f"eval_index_k{args.topk}.json"
    out.write_text(json.dumps(rows, ensure_ascii=False, indent=1), encoding="utf-8")
    w(f"\n-> wrote {out.name}\n")

    ok = sum(r["hit5"] for r in rows) / n >= 0.90 and it // n <= 3000
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
