# Search bench

Ranking is the product, and it is the part that degrades invisibly: a change that
helps five questions and quietly breaks three looks like an improvement from the
inside. This is the guard.

## Running it

```bash
# 1. bring a store up and seed it with the example corpus
cd server && docker compose up -d
python bin/import_tree.py bench/corpus --owner acme --repo shared \
    --url http://localhost:8081 --token "$ENGRAM_TOKEN"

# 2. the baseline an agent would have had without a store
python bench/baseline_grep.py

# 3. the index, measured with the same ruler
python bench/eval_index.py --url http://localhost:8081 --prefix acme/shared
```

`eval_index.py` exits non-zero when the pass mark is missed (recall@5 ≥ 90% at
≤ 3,000 tokens per question), so it can gate a change. Reads are closed by
default, so pass `--token` unless the store was started with
`ENGRAM_PUBLIC_READS=true`.

Run both before and after touching the ranking, the chunking, or the lexeme
rules. `baseline_grep.py naive` recomputes the baseline with mechanically
extracted query terms instead of hand-picked ones.

### Tiers and feedback

```bash
python bench/eval_index.py --url http://localhost:8081 --prefix acme/shared --tier 1
python bench/eval_index.py --url http://localhost:8081 --prefix acme/shared --tier 1 \
    --token "$ENGRAM_TOKEN" --vote-gold
```

`--tier N` measures one tier of the budget, reporting `tok` (the text a caller
reads) and `wire` (the whole JSON hit list — what a model's context pays for)
with a pass mark per tier: tier 1 recall@5 ≥ 80% at ≤ 600 wire tokens, tier 2
≥ 90% at ≤ 1,500. Measured on this corpus: the untiered page costs ~1,500 wire
tokens per question at 97% recall; tier 1 costs ~380 at the same 97%.

`--vote-gold` is the lock-in check for usage feedback. It runs every question,
votes the gold document "useful" for the even-indexed half through
`/api/feedback`, runs everything again and reports the halves separately. The
voted half should rise (it did: 9/17 → 17/17 at rank 1); the unvoted half must
not move. It leaves the votes in the store, so run it against a bench store.

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

Three more, from the tiers and feedback work — each a number that looked
reasonable until it was run:

- **A tier-1 cut at half the top score** took recall from 94% to 65% at every
  page size. RRF scores a document matched in two channels at about twice one
  matched in one, so the cut kept only the double matches. 35% costs nothing.
- **A usefulness bonus of 1/(RRF_K+4)** — "worth ranking 4th in one channel" —
  put one voted document first for six unrelated questions it had merely
  appeared on. Top-of-page RRF scores are a few ten-thousandths apart; the
  ceiling is now the distance between ranks 1 and 4, a tie-breaker.
- **One vote per document per day** made the same document rememberable for
  only one of the two questions it answered that day; `--vote-gold` recorded
  12 votes for 17 questions. Uniqueness is now per question too.
