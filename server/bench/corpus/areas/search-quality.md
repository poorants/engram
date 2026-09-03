# Keeping search honest

Ranking is the product. It is also the part that degrades invisibly: a change
that helps five questions and quietly breaks three looks like an improvement from
the inside.

## The bench is the guard

`bench/questions.jsonl` holds questions paired with the documents that should
answer them. `bench/eval_index.py` runs them through the real
[[search-ranking]] path and reports recall@1, recall@5 and the tokens a caller
would spend. `bench/baseline_grep.py` measures the same questions the way an
agent would have answered them before — grep, then read whole files.

Run both before and after touching anything in the ranking, the chunking
([[chunking]]) or the lexemes ([[lexeme-indexing]]).

## What the comparison is fair about

- the same questions and the same gold documents on both sides
- recall@5 measured on the top 5 DISTINCT documents, so chunks and files are
  compared like for like
- tokens counted as what a caller actually consumes: returned chunk bodies for
  the index, whole file bodies for grep

One asymmetry is left in deliberately, and it **favours the baseline**: grep can
stop reading the moment it hits the answer, which is knowledge it would not have
had in advance. The index returns its top K in one shot and cannot stop early.

## Interpreting a regression

A drop in `exact` questions usually means the lexeme rules changed — an
identifier that used to be kept whole is now split, or the reverse. A drop in
`semantic` questions usually means the title/path channel's weight moved, or the
chunk size changed enough that a heading and its explanation ended up in
different chunks.

Never tune the repo bonus on a guess. It is set to an explainable value and
should move only when several repos are in the store and real failures have
accumulated.
