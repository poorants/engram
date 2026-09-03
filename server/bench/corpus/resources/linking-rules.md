# Linking rules

A brain is not a folder tree with search bolted on. What makes it a brain is the
edges, and the store distinguishes two kinds of edge because they mean different
things.

| kind | written as | meaning |
|---|---|---|
| `wiki` | `[[note-name]]` woven into a sentence | a CONTEXTUAL link — this idea relates to that one |
| `md` | `[text](path.md)` in a folder README | a STRUCTURAL link — this hub lists that document |

Both are shown to a reader. Only `wiki` counts for judging whether a document is
genuinely connected.

## Orphans are the floor, not the goal

An **orphan** has no inbound edge at all. It is invisible: nobody arrives at it
except by search, and knowledge nobody arrives at gets rewritten from scratch a
year later.

The fastest orphan fix is structural — one per-folder README MOC that links
every document in its folder clears a whole folder at once. Do that first.

But a document reachable ONLY from its own folder MOC is a **weak node**: a
lonely spoke. It passes the orphan check while the graph is still a star, which
is a folder tree drawn with arrows. Adding another MOC line does not fix a weak
node; it deepens the star. What fixes it is weaving a contextual link into a
related document's prose, where the idea actually comes up.

`GET /api/integrity` reports broken links, orphans and weak nodes separately for
exactly this reason — see [[store-api]].

## Do not force links

Linking unrelated documents is over-structuring and it muddies the signal. If a
document genuinely has no relative, leave it an orphan and say so. A brain with a
few honest orphans is more useful than one where everything points at everything.

## Broken links are kept, not dropped

A `[[name]]` pointing at a document that does not exist yet is stored with a null
destination rather than discarded. Two reasons: the broken link stays visible so
it can be fixed, and the record survives so the edge connects itself if that
document is later created. Deliberate forward links to notes you intend to write
are legitimate and are reported as warnings, not errors.

A common trap: a folder README full of `` `filename.md` `` in backticks looks
like a MOC and links nothing. Code spans are not links, and every document in
that folder is still an orphan.
