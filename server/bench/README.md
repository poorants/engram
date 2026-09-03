# Search bench

Ranking is the product, and it is the part that degrades invisibly: a change that
helps five questions and quietly breaks three looks like an improvement from the
inside. This is the guard.

## Running it

```bash
# 1. bring a store up and seed it with the example corpus
cd server && docker compose up -d
python bin/import_tree.py bench/corpus --owner acme --repo shared \
    --url http://localhost:8081 --token "$ENGRAM_INGEST_TOKEN"

# 2. the baseline an agent would have had without a store
python bench/baseline_grep.py

# 3. the index, measured with the same ruler
python bench/eval_index.py --url http://localhost:8081 --prefix acme/shared
```

`eval_index.py` exits non-zero when the pass mark is missed (recall@5 ≥ 90% at
≤ 3,000 tokens per question), so it can gate a change.

Run both before and after touching the ranking, the chunking, or the lexeme
rules. `baseline_grep.py naive` recomputes the baseline with mechanically
extracted query terms instead of hand-picked ones.

## What is in here

- `corpus/` — 21 documents about engram itself, laid out as a real brain
  (PARA areas, MOC hubs, contextual wikilinks). It doubles as a worked example of
  what documents in a store look like, and it lints clean: zero broken links,
  zero orphans, zero weak nodes.
- `questions.jsonl` — 34 questions, each paired with the documents that should
  answer it. `exact` questions name an identifier or a value, `semantic` ones
  describe a situation in words the document does not use, `general` ones are
  broad.
- `baseline_grep.py` / `eval_index.py` — the two measurements.

## Two honest caveats

**The grep comparison does not reproduce the token-savings claim at this size.**
On 21 documents with hand-picked terms, grep scores recall@5 of 100% and reads
about 500 tokens per question, because the corpus is small enough that a
discriminating term appears in exactly one file and grep can stop the moment it
hits it. The index spends more tokens here, not fewer. That advantage only opens
up on a corpus large enough for grep's candidate list to get noisy — which is not
something a bundled example can honestly simulate. What this bench is genuinely
for is **regression detection on the ranking**, and for that it does not need to
be large.

**One question is expected to fail.** `s04` asks about documents belonging to a
"team" while the answering document says "group" throughout. Lexical search
cannot bridge that, and it is left in deliberately: a bench sitting at 100% has
stopped measuring anything. It is also the shape of question a vector channel
would help with, and therefore the honest place to look if that experiment is
ever revived.

## What it caught

The first run of this bench scored recall@5 of 65%. The cause was not the corpus:
`to_tsquery` ORed every lexeme with equal weight, and with no corpus-level IDF a
function word matching in a long document outranked the rare term that actually
discriminated. It was worst in the title/path channel, which is weighted 1.6×
precisely because a title match is supposed to be high signal — so the question
"what value is RRF_K set to" pulled up a document titled *Why a hit is a chunk*
on the strength of the word "is", while the one document defining `RRF_K` did not
appear at all. Searching `RRF_K` alone ranked it first, which is what isolated it.

Dropping English function words from the QUERY (never from the index) took
recall@5 from 65% to 97%: exact 8/12 → 12/12, semantic 6/14 → 13/14. The
behaviour is pinned by tests in `server/tests/test_core.py`.

This defect could not surface on the corpus engram grew up on, whose titles were
identifiers and non-English. That is worth remembering when reading any measured
claim about ranking: it holds for the corpus it was measured on.
