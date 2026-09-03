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
flag, not a config value, not something a model decides in the moment. Run
`engram scope` in any directory and it tells you what this directory's git
origin resolves to.

`area` is one of `projects` · `areas` · `resources` · `archives`, and at most
five levels sit below the document root. A repo hub MOC is the one address with
no area: `<owner>/<repo>/README.md`.

Given a relative path — `./resources/logging.md` — the coordinates are filled in
from the current directory's git origin, so a session that knows where it is
does not have to say so.

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
- **The lint.** `engram integrity` reports broken links, orphans (no inbound
  edge, reachable only by search) and weak nodes (one edge, effectively a
  dead end). Density is the health metric, not document count.

Link rules: [`linking-rules.md`](../skills/engram/references/linking-rules.md).
Weaving workflow: [`weave-workflow.md`](../skills/engram/references/weave-workflow.md).

## The scope boundary

The store admits a list of **owner groups**, set as `ENGRAM_OWNERS` when it is
brought up. A write whose path does not start with one of them is refused with
403, and `engram` exits `3`.

It is a list of groups, not repos, and that asymmetry is deliberate: a new repo
under an admitted group works with no change at all, and a personal or client
repo never does. The boundary holds by default rather than by remembering to
maintain it.

**Reads need no token.** What must not be readable is therefore never let in —
the allow-list is the only control, so it is where the thinking goes. See
[`scope-boundary.md`](../server/bench/corpus/resources/scope-boundary.md).

A refusal is not an error to retry. The store is alive and declined, so the
document goes to the local file brain instead — which is where it belonged.
That is a different path from the store being unreachable, which fails outright.

## Revisions and the byline

Every write keeps the previous body. `brain_revisions` returns who changed a
document, when, and the `--note` they gave for why. `put` requires that note;
a history of changes with no reasons is a history you cannot use.

The byline is **a claim, not a proof**. The write token is one shared
credential, so the recorded author is what the client says it is:
`ENGRAM_AUTHOR`, else `git config user.name`, else `$USER`. The goal is to make
it honest by default, not provable — proving it means accounts, issuing and
revocation, which is a different system than this one.

## Never delete

There is no `brain_delete` and no `engram delete`. The contract is *move to
archives*, and `move` leaves the old path behind as an alias, so links into a
document survive its being renamed, reclassified or archived.

A store that can forget is a store whose absences are ambiguous: nobody can tell
"we decided against this" from "somebody tidied up".
