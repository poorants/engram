#!/usr/bin/env python3
"""The grep baseline — the line the index has to beat.

It mechanically imitates how an agent answers a knowledge question without a
store:

  1. for each search term, collect candidate files with ``grep -rcF``
  2. rank by (number of terms matched, total match count), take the top 5
  3. read the files WHOLE in rank order, stopping at the answer

Step 3 is where all the cost is. grep returns matching lines with no context, so
in a real session the agent ends up reading the candidates. What is measured here
is therefore not match precision but **how much had to be read to reach the
answer**.

The terms are hand-picked, and that is deliberate: they are closer to what a
capable agent would actually type than any mechanical extraction, which makes the
baseline GENEROUS. Beating a generous grep is the only result worth reporting.
Pass ``naive`` to use mechanically extracted terms instead and see both numbers.
"""
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).parent
_args = [a for a in sys.argv[1:] if a != "naive"]
CORPUS = Path(_args[0]).expanduser() if _args else HERE / "corpus"
QS = HERE / "questions.jsonl"
TOPK = 5
NAIVE = "naive" in sys.argv

_STOP = {
    "the", "a", "an", "is", "are", "was", "were", "be", "been", "do", "does", "did",
    "what", "which", "why", "how", "when", "where", "who", "whom", "if", "of", "to",
    "in", "on", "at", "for", "from", "with", "without", "and", "or", "but", "not",
    "it", "its", "this", "that", "these", "those", "my", "me", "i", "you", "your",
    "can", "could", "should", "would", "will", "shall", "may", "might", "must",
    "there", "here", "any", "all", "some", "more", "most", "one", "two", "still",
    "get", "got", "make", "made", "have", "has", "had", "than", "then", "so", "up",
    "out", "into", "about", "instead", "only", "even", "just", "now", "does",
}


def naive_terms(q: str) -> list[str]:
    """Terms as a mechanical extractor would produce them — the pessimistic
    baseline, for comparison with the hand-picked one."""
    out = [w for w in (t.strip(".?!\"'`,") for t in re.split(r"[\s,/()]+", q))
           if len(w) >= 3 and w.lower() not in _STOP]
    return out or [q[:6]]


_CJK = re.compile(r"[가-힣ᄀ-ᇿ㄰-㆏぀-ヿ一-鿿]")


def est_tokens(text: str) -> int:
    """Token estimate for mixed text: CJK ~0.9 tokens/char, otherwise ~4 chars
    per token.

    An approximation, since tiktoken is not a dependency here. The figure that
    matters is the RATIO between the two paths, not either absolute value, and
    that ratio holds monotonically under any tokenizer.
    """
    c = len(_CJK.findall(text))
    return int(c * 0.9 + (len(text) - c) / 4)


def grep_files(term: str) -> dict[str, int]:
    """Files containing the term -> number of matching lines."""
    p = subprocess.run(["grep", "-rcF", "--include=*.md", "--", term, "."],
                       cwd=CORPUS, capture_output=True, text=True)
    out = {}
    for line in p.stdout.splitlines():
        path, _, n = line.rpartition(":")
        if path and n.isdigit() and int(n) > 0:
            out[path.lstrip("./")] = int(n)
    return out


def rank(terms: list[str]) -> list[str]:
    hits: dict[str, list[int]] = {}
    for t in terms:
        for path, n in grep_files(t).items():
            e = hits.setdefault(path, [0, 0])
            e[0] += 1      # how many terms matched
            e[1] += n      # total matching lines
    return [p for p, _ in sorted(hits.items(), key=lambda kv: (-kv[1][0], -kv[1][1], kv[0]))]


def main() -> int:
    if not CORPUS.is_dir():
        print(f"no corpus at {CORPUS}", file=sys.stderr)
        return 2
    questions = [json.loads(l) for l in QS.read_text(encoding="utf-8").splitlines() if l.strip()]
    rows = []

    for q in questions:
        terms = naive_terms(q["q"]) if NAIVE else q["terms"]
        ranked = rank(terms)
        top = ranked[:TOPK]
        gold = set(q["gold"])

        # Read in rank order, stopping at the answer. Not finding it means all
        # five were read and the question failed.
        read, toks = [], 0
        for path in top:
            body = (CORPUS / path).read_text(encoding="utf-8", errors="replace")
            read.append(path)
            toks += est_tokens(body)
            if path in gold:
                break

        rows.append(dict(id=q["id"], type=q["type"], q=q["q"], candidates=len(ranked),
                         hit1=bool(top and top[0] in gold), hit5=bool(gold & set(top)),
                         read=len(read), tokens=toks, top=top[:3]))

    w = sys.stdout.write
    w(f"{'id':5} {'type':9} {'cand':>5} {'@1':>3} {'@5':>3} {'read':>5} {'tokens':>7}  question\n")
    w("-" * 100 + "\n")
    for r in rows:
        w(f"{r['id']:5} {r['type']:9} {r['candidates']:5} "
          f"{'O' if r['hit1'] else '.':>3} {'O' if r['hit5'] else '.':>3} "
          f"{r['read']:5} {r['tokens']:7}  {r['q'][:44]}\n")

    n = len(rows)
    w(f"\n== grep baseline [{'mechanically extracted terms' if NAIVE else 'hand-picked terms'}] ==\n")
    w(f"questions        {n}\n")
    w(f"recall@1         {sum(r['hit1'] for r in rows)}/{n} ({sum(r['hit1'] for r in rows)/n:.0%})\n")
    w(f"recall@5         {sum(r['hit5'] for r in rows)}/{n} ({sum(r['hit5'] for r in rows)/n:.0%})\n")
    w(f"total tokens     {sum(r['tokens'] for r in rows):,}\n")
    w(f"per question     {sum(r['tokens'] for r in rows)//n:,}\n")
    w(f"documents read   {sum(r['read'] for r in rows)/n:.1f} on average\n")
    for t in ("exact", "semantic", "general"):
        g = [r for r in rows if r["type"] == t]
        if g:
            w(f"  {t:9} recall@5 {sum(r['hit5'] for r in g)}/{len(g)}"
              f"   {sum(r['tokens'] for r in g)//len(g):,} tokens on average\n")

    out = HERE / ("baseline_grep_naive.json" if NAIVE else "baseline_grep.json")
    out.write_text(json.dumps(rows, ensure_ascii=False, indent=1), encoding="utf-8")
    w(f"\n-> wrote {out.name}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
