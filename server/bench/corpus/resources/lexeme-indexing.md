# How text becomes searchable

The indexer builds lexemes itself instead of handing text to Postgres's default
parser. The search path calls the SAME function. If they ever diverged, queries
would stop reaching the index and nothing would announce it.

## Identifiers are kept whole and split

The default parser breaks `http_client_pool` into pieces. Here the whole token is
kept AND its parts are added, so a query from either direction reaches the
document. A token longer than 60 characters is dropped — it is a hash or a
base64 blob, not a word anyone will search for.

## CJK is indexed as syllable 2-grams

No morphological extension is present in the image. Generating syllable bigrams
in the indexer keeps everything on stock Postgres, which is what keeps the
deployment to a single compose file. That property decides the installation
barrier, and it is worth more than a marginally better tokenizer.

## Weights

`array_to_tsvector` builds the vector directly, with `setweight` applying:

- **A** — the document title and the chunk's heading path
- **B** — the chunk body

`ts_rank` weighs A about 2.5 times B, which is what makes a heading match beat a
passing mention in a paragraph.

The document row carries a separate `tsv` built from the title and the path
words. That is the title/path channel described in [[search-ranking]], and it is
kept separate from the body vector on purpose.
