# How search ranks results

One ranking exists, and both a person's search box and an agent's `brain_search`
hit it. If there were two, the order a person saw and the order an agent received
would drift and nobody could reason about either.

## Two channels, fused with RRF

1. **Body lexemes** — the chunk text, matched against the query's lexemes.
2. **Title and path** — a separate channel, because filename matching is what
   made grep strong and mixing that signal into the body's bag dilutes it. It
   carries a higher fusion weight (1.6 against 1.0).

The two are combined with Reciprocal Rank Fusion: each channel contributes
`1/(k + rank)` with `k = 60`. RRF is used because the channels' scores are not
comparable — even two `ts_rank` values have different distributions per channel,
and a weighted sum needs normalization constants that go stale as the corpus
grows. Ranks have no such problem.

## The repo bonus is a bonus, never a filter

Searching from inside a checkout boosts that repo's documents by
`1/(RRF_K + 8)` — worth about as much as placing 8th in one channel. It is added
AFTER fusion, so it shifts the order without manufacturing candidates: a document
that does not match the query at all never surfaces just for being in your repo.

It is deliberately not a filter. If another repo already solved your problem,
that answer must still appear, even below yours. Filtering declares that other
repos' knowledge does not exist, which is the very problem of knowledge trapped
per repo. `only_repo` and `only_owner` exist for when isolation is genuinely what
is wanted, and they are separate parameters for that reason.

## Ask a question, not keywords

The ranking is tuned on whole natural questions. Breaking a question into
keywords throws away the words that discriminate. The unit that comes back is a
chunk with its heading path, not a document — see [[chunking]].

Changing any of this without measuring is how a ranking degrades invisibly;
[[search-quality]] describes the bench that guards it.

Semantic (vector) search is off by default. Measured with and without it, recall
was identical and the failing questions were the same set, so the production
image does not carry the vector extension at all. Keeping the default deployment
to one compose file is worth more than an unmeasurable gain.
