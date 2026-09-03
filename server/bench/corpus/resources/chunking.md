# Why a hit is a chunk

Search returns chunks, not documents. A document is often several thousand
characters of which forty are the answer, and returning the whole thing spends
the reader's attention — and an agent's context — on the other several thousand.

## How a document is cut

Heading boundaries come first, size second. Each chunk aims for about 700
characters and is cut hard at 1400.

Every chunk carries its `heading_path` — `Guide > Setup > Token` — which is
assembled from the heading stack as the document is walked. Without it, a reader
holding a fragment cannot tell where in the document it came from, and will cite
it as if it were the whole story.

## A code fence is never split

Size limits give way to a fence boundary. Half a code block is neither runnable
nor quotable, and a chunk ending mid-fence produces an answer that looks complete
and is not.

## Chunks are derived

Chunks, lexemes and edges are all derived from `docs.body`, which is the
canonical copy. They can be rebuilt at any time with `POST /api/rederive`, which
is what to run after changing the chunking or link rules — it rewrites every
chunk without touching a single body or revision.

That distinction matters: rebuilding derived data is safe, and "re-index the
whole store" became a dangerous phrase the moment the canonical copy moved into
the database. See [[revisions-and-aliases]] for what is canonical and what is not.
