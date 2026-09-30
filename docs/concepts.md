# Concepts

Five things decide how engram behaves. None of them are configurable, and that
is the point — a knowledge store whose rules vary per machine is one whose
contents nobody can reason about.

## How a document is addressed

```
<owner>/<repo>/<area>/<name>.md
  acme / webapp / resources / logging.md
```

`owner` and `repo` are **derived from the git remote, never chosen**. Not a
flag, not a config value, not something a model decides in the moment:
`git remote get-url origin` in the working repo says what they are.

`area` is one of `projects` · `areas` · `resources` · `archives`, and at most
five levels sit below the document root. A repo hub MOC is the one address with
no area: `<owner>/<repo>/README.md`.

The MCP server cannot see which checkout a call is about, so a call always
carries the full path. The capture hooks, which do run in the checkout, tell the
session its coordinate when it opens.

## PARA

Four areas, and the sorting question is about *time and action*, not topic.

| Area | What belongs there | The test |
|---|---|---|
| `projects` | work with an end | Is there a finish line? |
| `areas` | standards held indefinitely | Would letting this slide be a failure? |
| `resources` | reference, no owner | Is it useful to someone not doing this work? |
| `archives` | done or abandoned | Is it over? |

A project that ends moves to `archives`, not away. A resource that becomes a
standard moves to `areas`. Movement between areas is normal and is what keeps
`projects` honest — it is the area that rots when nothing ever leaves.

Full rules and the edge cases: [`skills/engram/references/para-categories.md`](../skills/engram/references/para-categories.md).

## The graph

Folders are physical; the graph is the layer over them that actually carries
meaning. Three mechanisms:

- **Links.** `[[other-document]]` in a body creates an edge. Every link is
  bi-directional — `brain_get` returns both outgoing links and backlinks, so a
  document is reachable from anything that mentions it, not only from its folder.
- **MOC hubs.** A Map of Content is a document whose job is to point at others. A
  repo's `README.md` is its hub; area MOCs sit under each area. Hubs are what
  keeps a growing store navigable without a search.
- **The integrity check.** `brain_integrity` reports broken links, orphans (no inbound
  edge, reachable only by search) and weak nodes (one edge, effectively a
  dead end). Density is the health metric, not document count.

Link rules: [`linking-rules.md`](../skills/engram/references/linking-rules.md).
Weaving workflow: [`weave-workflow.md`](../skills/engram/references/weave-workflow.md).

## The scope boundary

The store admits a list of **owner groups**, set as `ENGRAM_OWNERS` when it is
brought up. A write whose path does not start with one of them is refused with
403.

It is a list of groups, not repos, and that asymmetry is deliberate: a new repo
under an admitted group works with no change at all, and a personal or client
repo never does. The boundary holds by default rather than by remembering to
maintain it.

The allow-list is about **what** may enter, not **who** may connect. Who may
connect is the remote MCP server's login (`ENGRAM_SERVE_ALLOW`), and the two are
separate on purpose: a store that is careful about which repos it admits and
then serves all of them unauthenticated has a boundary that only looks like
one. See
[`scope-boundary.md`](../server/bench/corpus/resources/scope-boundary.md).

A refusal is not an error to retry, and not a cue to write somewhere else. The
store is alive and declined: that repo's knowledge does not belong in this
brain. That is a different answer from the store being unreachable, which fails
outright.

## Revisions and the byline

Every write keeps the previous body. `brain_revisions` returns who changed a
document, when, and the `note` they gave for why. `brain_put` and `brain_patch`
require that note; a history of changes with no reasons is a history you cannot
use.

The byline is **the login**. The remote MCP server stamps every revision with
the person the session authenticated as (the part of their email before `@`, or
the server's fixed `ENGRAM_SERVE_AUTHOR`), whatever author a call names. The
store itself still has one shared credential, held only by that server.

## Never delete

There is no `brain_delete`. The contract is *move to
archives*, and `brain_move` leaves the old path behind as an alias, so links into a
document survive its being renamed, reclassified or archived.

A store that can forget is a store whose absences are ambiguous: nobody can tell
"we decided against this" from "somebody tidied up".
