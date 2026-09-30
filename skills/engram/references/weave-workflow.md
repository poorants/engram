# Weave Workflow — raise neural density (full procedure)

The deepening counterpart to Link & Connect. Link & Connect removes **orphans**
(gets every doc ≥1 inbound link); Weave removes **lonely spokes** — docs whose
only inbound is a MOC, so the graph is a star, not a brain. See
[linking-rules.md](linking-rules.md) "Connected vs woven" and rule 3 "No lonely
spokes" before starting.

**When**: orphans are handled but the brain still feels like isolated folders —
the user wants it "more connected / more neural", or `brain_integrity` reports
many `weak_nodes`.

## Steps

1. **Measure** — `brain_integrity` and read `counts` (`weak`, `orphans`,
   `broken`, and `by_kind` — how many edges are contextual `wiki` versus
   structural `md`) and the `weak_nodes` list. This is the baseline to beat.
   (orphans = 0 with nearly every content document weak = a pristine star.)

2. **Find candidates** — for each weak node (start with the ones in the area
   the user cares about), `brain_get` it to learn its title and central claim,
   then `brain_search` for them **as questions**. Two kinds of candidate come
   out; neither is ever applied automatically:
   - **missing links** — another document already *mentions* this note (by its
     title, its filename, or its core term) in prose but does not link it.
     Adding that link gives the spoke a contextual inbound: the cheapest
     spoke-dissolver. The search hit's heading path shows where the mention sits.
   - **concept candidates** — a term that recurs across documents in
     **different areas or repos** with no note of its own. Promote it to a
     shared atomic concept note (`resources/` or `areas/`) and route those
     documents through it; this builds the cross-folder connective tissue a star
     lacks (Matuschak: concept-oriented AND densely linked).

3. **Judge, then weave** — the model decides which candidates are *real*:
   - missing links → `brain_patch` the link into the prose **where the mention
     already sits** (an `anchor` on the mentioning phrase, `expect` and
     `baseSha256` from the `brain_get`) — contextual, not a trailing "related"
     list.
   - concept candidates → if worth promoting, create the concept note via the
     Create Workflow, then link the referring docs to it contextually.
   - **Stay selective.** Skip forced or trivial matches; a spoke that genuinely
     relates to nothing stays an acknowledged leaf. Forcing links is the failure
     mode here (over-structuring), not a win.

4. **Re-measure & report** — `brain_integrity` again; report `counts.weak` and
   the `wiki` edge count before→after, plus the links woven and concept notes
   created.

## Caveat — respect the brain boundary

A weave pass can surface clusters that are really **external deliverables**
with their own lifecycle (generated reports, a manual's source). Don't
over-weave those — if anything they are candidates to keep out of the brain
(the user's call; see SKILL.md "Brain boundary"), not density targets.

## Reading the numbers

- `counts.weak` — content documents whose only inbound links are structural
  (`md`, from a MOC). It falls as you weave.
- `counts.by_kind.wiki.total` — contextual edges. It rises as you weave; a
  rising share of `wiki` in all edges is the brain getting less star-shaped.
- A link that crosses an area or repo boundary is worth more than one inside
  the same group — it is what turns a folder tree into a network. Prefer those
  when choosing between candidates.

These are the scoreboard for whether the brain is getting more neural over time,
beyond the pass/fail orphan check.
