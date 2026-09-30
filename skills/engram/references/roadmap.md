# engram Roadmap

Planned capabilities not yet implemented. Design notes so the direction is fixed
even before the code exists.

## Publish / Export workflow (planned)

**Problem.** The brain is a messy, always-growing working space full of
wikilinks, MOCs, seeds, and half-finished notes. That is correct for *authoring*
but wrong for *distribution*. You often want to hand someone — or publish — a
clean, self-contained subset, on demand, without exposing the whole brain.

**Principle: source stays visible, distribution is extracted.** Do not hide the
brain. Instead add a publish step that pulls a curated subset out of the store
into a separate, portable artifact. This mirrors the
`areas/blog/seeds → drafts → published` pattern, generalized to the whole brain.

### Shape

```
the store                    ->   publish                  ->   dist/ (or a site)
  working source, messy            curate + transform           clean, portable artifact
```

- **Selection** — choose what to publish, in priority order:
  1. an explicit MOC that lists what to ship,
  2. an explicit list of store paths.
  Default: nothing is published unless opted in (avoid leaking private notes).

- **Transforms** applied on export:
  - Resolve `[[wikilinks]]` → portable relative markdown links (or plain text
    when the target is not published).
  - Drop links/sections pointing at unpublished notes (no dangling refs in the
    artifact).
  - Strip private blocks (e.g. a `> [!private]` callout).
  - Flatten the relevant MOC into a table of contents / index for the artifact.

- **Output targets** (pick per run):
  - a folder (`dist-docs/`) — portable markdown bundle,
  - a single combined file (one `.md`/PDF for handoff),
  - a static site (later; Quartz/MkDocs-style), reusing MOCs as navigation.

- **Idempotent + non-destructive**: publishing never mutates the store; it only
  writes the output target. Re-running republishes from the current source.

### Integration points

- Runs `brain_integrity` first: do not publish a selection with broken links
  into the artifact — check, repair, then export.
- Until a server-side export exists, the skill can do it in a session: read the
  selection with `brain_get`, apply the transforms, write the artifact locally.
  A "Publish Workflow" section in `SKILL.md` would carry the judgement (decide
  selection, run transforms, report).
- Trigger ideas: "publish docs", "export the brain", "build the doc bundle".

### Open questions

- Wikilink → portable link strategy when targets span published/unpublished sets.
- Whether the artifact keeps the PARA shape or flattens to a reader-first
  structure (by topic/MOC).
- Site generation: own renderer vs. emit a config for an existing SSG.
- A server-side export endpoint vs. assembling in a session (token cost grows
  with the selection).
