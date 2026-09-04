---
name: engram
description: >
  Networked PARA knowledge brain — manages documents by PARA (Projects, Areas,
  Resources, Archives) AND weaves them into one connected knowledge graph through
  bi-directional links, MOC hubs, and integrity linting: a logical link layer over
  physical folders, so the brain grows like a network instead of a filing cabinet.
  The brain is normally a shared Postgres-backed STORE that is searched rather than
  grepped, with a revision history per document; a local file brain is used for repos
  the store does not admit. Use to manage, organize, search or save documents; save
  meeting notes; archive or migrate a project; connect notes; find orphans; fix broken
  links; update MOCs; raise neural density; review what the brain gained this session;
  read a document's history; or point this machine at a store. Matches intent in any
  language — e.g. "문서 정리", "회의록 저장", "노트 연결", "링크 점검", "고아 문서",
  "MOC 업데이트", "브레인 리뷰", "브레인 검색", "저장소에서 찾아줘", "이 문서 이력",
  "누가 언제 바꿨어", "organize docs", "save this as a note", "connect notes",
  "find orphans", "search the brain", "document history", "set up the brain store".
---

# engram — networked PARA knowledge brain

Manage documents with the PARA method while weaving them into **one connected
knowledge network** through bi-directional links, MOC hubs, and integrity
linting. The core idea is *Networked PARA*: a logical link layer on top of
physical classification.

Two layers:

- **Management (PARA)** — create, move, archive, migrate, review. Folders own
  governance.
- **Connection (network)** — link documents by context, clear orphans, weave
  lonely spokes into a mesh rather than a star, catch broken links. Links own
  context. The detailed rules are in
  [references/linking-rules.md](references/linking-rules.md); load it before any
  linking work and follow it.

The thing being defended is simple: **a session dies, knowledge should not.** The
measure of success is not investigating the same question twice.

## The store — resolve this first

The brain is normally **not a tree of files**. It is a Postgres-backed service,
and there is **one client for it**: the `engram` binary. Everything below is a
surface over that one client — the address, the token, the path rules and the
author byline live there, once.

1. **MCP tools (prefer these in a session)** — `brain_search`, `brain_get`,
   `brain_revisions`, `brain_integrity`, `brain_put`, `brain_move`. Same
   endpoints, same single ranking, no permission churn, and available before this
   skill even loads. One difference: an MCP server is spawned once per session
   and cannot see which checkout a call is about, so **you** supply the full
   `<owner>/<repo>/<area>/<name>.md` path — take owner and repo from `origin`.
2. **`engram <verb>`** — the same operations for anything that is not the model:
   hooks, scripts, a person at a terminal. It runs IN a directory, so
   `./<area>/<name>.md` is filled in from that repo's `origin`. It prints for a
   person and takes `--json` for a machine, it writes a scope-refused document to
   the local file brain itself, and its exit codes are a contract (`3` the store
   refused and nothing local took it, `4` store unreachable).

**This skill ships no scripts and needs no interpreter.** Everything below is
`engram`, one binary. It used to be a set of Python helpers, and on Windows they
never ran: `python3` is not a command there even where Python is installed.

```bash
engram status                                    # store, this repo's scope, your byline
engram search "why does the copy button copy nothing"   # ranked chunks, not whole files
engram get   acme/shared/resources/x.md
engram put   acme/webapp/resources/x.md --file draft.md --note "why this exists"
engram revisions acme/shared/resources/x.md      # this replaces git log
engram move  <old> <new>                         # the old path stays as an alias
engram integrity                                 # broken / orphan / weak nodes
```

**Search the store before Grep. Always.** It returns chunks with their heading
path instead of whole documents, so an answer costs a fraction of what a file
sweep does.

**You never choose scope — the git remote does.** The CLI derives `owner/repo`
from `origin`, so a repo whose owner group the store admits writes to it and one
it does not cannot. That is the confidentiality boundary, and it is enforced by
the service (403), not by anyone remembering.

**If the store is unreachable, reading and writing both fail on the spot.** There
is no fallback and no queue: a stale answer and a spool sitting somewhere both
manufacture the belief that it worked, and that belief outlives the outage. No
store means no brain — say so, rather than answering from something older than
the question.

A refusal is a different thing. **Scope refused (403) → write the local file
brain.** The store is alive and *declined*; knowledge from an unadmitted repo
belongs there anyway.

**The `<repo>` coordinate is a routing decision — this repo, or `shared`.** The
test: *which code does this knowledge age with?* Knowledge that goes stale when
one repo's code changes (its conventions, its troubleshooting, its build traps,
its issue reproductions) takes that repo's coordinate. Knowledge that must
outlive any one repo — **contracts between repos** (ABIs, SQL dialects, error
code tables, protocol specs), manuals one repo produces but all consume,
cross-repo handoffs, meetings, team conventions — goes to `<owner>/shared/`.
Contracts are *always* shared: two copies in two repos have already diverged.
When genuinely unsure, prefer the repo coordinate and promote later with a move —
the alias keeps links alive, so promotion is cheap and copies are expensive.

Full contract and setup: [references/store.md](references/store.md).

## Setting up a store

Setup is a **deterministic act**, so it is three commands rather than a
procedure you improvise. Your job is to call them in order and translate a
failure into plain language — never to reimplement what they do.

```bash
engram store set <url> --token <write token>   # designate it
engram store doctor                            # prove it end to end
engram store show                              # where the settings came from
```

`doctor` checks the write token, not only the connection: "the store is up" and
"I can write to it" are different facts, and a check that proves only the first
lets someone finish read-only and discover it when a save fails at the end of a
session.

If there is no store yet, one is brought up with Docker on any machine —
`server/` in the engram repo holds a single compose file. If the user has no
store and does not want to run one, the file-brain path below works on its own.

## Path resolution

Run the resolver first — `engram resolve --json` →
`{base, source, store, owner, repo, in_scope, warning}` — then route on `source`:

1. **`store`** (the normal case) — **the store is the brain and resolution stops
   here.** `store` is the URL, `owner`/`repo` are the coordinates derived from
   `origin`, `in_scope` says whether the store admits this owner. There is no
   second base to route between: shared and repo-only knowledge are the same
   table, told apart by the `repo` coordinate and decided **per write**. `base`
   here is the **fallback vault only** — where a document goes when the store
   answers 403. A local `brain/` in an admitted repo is a leftover mid-migration;
   `warning` says so, and it is not a second brain.
2. **`absorb`** — you are working inside the designated file brain itself; the
   base is its PARA base.
3. **`shared`** — a file brain is designated and you are elsewhere; use it.
4. **`local`** — no designation: `brain/` if present, else legacy `para/`, else
   PARA folders at the root (flat mode — consider the Upgrade Workflow).
5. **`none`** — nothing yet. Ask the user to point at a store
   (`engram store set <url>`) or designate a file brain
   (`engram brain set <path>`). **Never invent a path.**

An explicit `CLAUDE.md` convention wins over all of the above. Once determined,
stay consistent within that project. Full semantics — the settings file, its two
owners, and how a brain is designated:
[references/workspace.md](references/workspace.md).

Throughout, `<base>/` means the resolved base — `brain/` when nested, an empty
prefix in flat mode. The linter auto-detects it the same way (force with
`--base`).

> **Legacy base (`para/`, or PARA folders at the root)?** A first-class case:
> engram *is* the upgrade path. "Complete the migration to brain" migrates both
> the **layout** (rename to `brain/`) and the **connection layer** (links, MOCs,
> lint) → **Upgrade Workflow**, a guarded refactor, because a base name can be
> load-bearing in code and CI.

`engram link` writes a small portable pointer block into the repo's
`CLAUDE.md` so that even a session without this skill knows a brain exists and
where to look. It is optional — engram always resolves positionally — and
`link --remove` strips it.

## Quick reference

| Category | Path | Purpose | Lifespan |
|---|---|---|---|
| **Projects** | `<base>/projects/` | active work with a goal and a deadline | temporary — archive on completion |
| **Areas** | `<base>/areas/` | ongoing responsibility, no end date | persistent — review periodically |
| **Resources** | `<base>/resources/` | reference material and collected knowledge | persistent — update as it changes |
| **Archives** | `<base>/archives/` | anything above that is finished | permanent, read-only in practice |

PARA folders own one axis (actionability). The repo axis is a folder **only** in
`projects/<repo>/` — `resources/` and `areas/` mix across repos by design, and
domain knowledge stays shallow under a MOC rather than in deep folders. Retiring
a repo archives only its `projects/<repo>/`; its reusable knowledge stays.

## Migration: three operations, one word

"Migrate" names **three independent operations**. Keep them distinct — a request
can want one, two, or all three, and routing to the wrong one is the most common
failure here.

| Operation | What it changes | Workflow |
|---|---|---|
| **Classify & Import** | scattered, *unclassified* docs → PARA folders | Classify & Import |
| **Base migration** | the base *layout/name* → `brain/` | Upgrade, Phase A |
| **Connection-layer upgrade** | folders-only vault → *networked brain* | Link & Connect (= Upgrade, Phase B) |

Route by the *current state of the vault*, not by the user's word: docs unfiled →
**Classify & Import**; filed but the base is `para/` or flat and they want
`brain/` → **Upgrade** (A+B); filed and based, only links missing → **Link &
Connect** (Phase B alone).

## Brain boundary — what stays in, what separates

The brain holds **thinking and knowledge** — everything you link to and revisit.
Under PARA that includes active **planning, spec and strategy documents**: they
are Projects, and the highest-value nodes, where knowledge gets applied. Keep
them in; do not pull them out for looking "output-like".

Separate a set of documents into a **root sibling folder** (next to `<base>/`,
e.g. `blog/`) only when it has its own **external delivery lifecycle** — a
workflow, repo or timeline outside your thinking network. The test: *is this
linked and re-read as part of thinking (→ brain), or an output with its own
external lifecycle (→ sibling)?*

- **Default to keeping documents in the brain.** Separation severs the
  project↔knowledge links that make Networked PARA worth anything, so it has to
  earn its place.
- A separated sibling sits **outside the link network and the lint base**. It may
  reference brain docs one way; the brain must not depend on it.
- **Separation is the one move you do NOT decide alone.** Make every other call
  autonomously; externalize a document set only on explicit request.

## Init Workflow

**When**: the first PARA interaction, or the category folders are missing.
**Auto-execute without asking** — it is idempotent.

Resolve the base first, run `engram init` (`--output .` for nested under
`brain/`, add `--flat` for categories at the root), and report what was created. In store mode there is nothing to initialize — the store already
exists.

## Create Workflow

**When**: the user wants a new document.

1. **Category** — Projects (deadline/goal), Areas (ongoing), or Resources
   (reference). Unsure? Load `references/para-categories.md`.
2. **Structure** — a single `.md` for one topic, or a `kebab-case/` directory for
   multi-deliverable work.
3. **Filename** — `kebab-case.md`; date-prefix time-sensitive items
   (`YYYY-MM-DD-topic.md`).
4. **Write** — plain markdown, starting with an H1. No frontmatter, no `---`
   rules. Title and headings in the words someone would actually ask; the
   conclusion first in each section; identifiers verbatim. Write it **through the
   store**: the `brain_put` MCP tool (body plus `note`), or `engram put
   <owner>/<repo>/<area>/<name>.md --file <draft> --note "<why this exists>"`.
   The note lands in the revision history, which is what replaces `git log` now
   that there are no files. If the store refuses the owner (403), `engram put`
   writes the local file brain itself and says where it landed; the MCP tool
   reports the refusal instead, so retry that one through `engram put`.
5. **Connect** — secure at least one inbound link, weave contextual
   `[[wikilinks]]` into the prose, update the MOC, and ground a `resources/` doc
   to an `areas/` or `projects/` one. Follow
   [references/linking-rules.md](references/linking-rules.md).
6. **Report** — path, PARA category, a brief summary, and what now links to it.

Templates and full per-step detail:
[references/create-workflow.md](references/create-workflow.md).

## Move Workflow

**When**: relocating a document between PARA categories.

Common moves: `projects/`→`archives/` (completed), `areas/`→`archives/` (ended),
`resources/`→`archives/` (outdated), `archives/`→`projects/` (reactivated).

**Do not `mv` a file for a store document.** There is no file to move.

```bash
engram move <owner>/<repo>/<area>/<name>.md <owner>/<repo>/archives/<name>.md
```

(or the `brain_move` MCP tool with the same two paths.)

The store records the old path as an **alias**, so `[[old-name]]` written
elsewhere keeps resolving. On a file brain that was impossible — you either
edited every referring document or left the links broken.

Steps: find the source (`search` or `get`) → determine the target category (ask
or infer) → move → report `source → destination` and the alias → close with the
Integrity Lint.

**Never delete documents. Move them to archives.** In the store `delete` is soft
(the body stays in revisions and `restore` brings it back), but archiving is
still the right operation: deletion removes a document from search, archiving
reclassifies it.

## Classify & Import Workflow

**When**: bulk-reclassifying documents **scattered outside** the PARA structure.

> **First check: are they already PARA-classified?** If they already live in
> `projects/`·`areas/`·`resources/`·`archives/`, there is nothing to classify —
> do NOT run scan→classify→move. What is missing is one of the other two
> migrations: the `brain/` layout → **Upgrade Workflow**; links and MOCs only →
> **Link & Connect**.

Six steps: **Scan** (Glob `**/*.md`, `**/*.txt`, excluding existing PARA folders,
`.git/`, `node_modules/`, root metadata) → **Classify** (read each doc, assign a
category via `references/para-categories.md`, flag the unclear) → **Present a
plan** (a Classified/Manual/Skipped table; name the collisions) → **Confirm**
(execute only after approval) → **Execute** → **Report**, then close with the
Integrity Lint.

Full detail, plan and report templates, exclusion and classification heuristics:
[references/migration-patterns.md](references/migration-patterns.md).

## Upgrade Workflow — legacy vault → engram brain

**When**: a repo is a legacy vault (a `para/` base, or flat PARA folders at the
root) and the user wants the engram brain model. Two independent phases; "full
migration" runs both, "just connect it" runs only B.

**Decide scope first** (ask if unstated): full (A+B) vs connection-only (B).
Default a legacy base to full unless the user opts out — but never auto-execute
Phase A, which touches code.

- **Phase A — base migration**: a **guarded refactor, not a bare `git mv`**. The
  base name can be load-bearing in import paths and CI. Grep the whole repo for
  the old name, `git mv` to preserve history, update every non-doc reference, fix
  links, then scope → approve → execute.
- **Phase B — connection layer**: the real value. Run **Link & Connect**
  (per-folder README MOCs first, then contextual links, then re-lint).
- **Verify and report** both phases.

Phase A detail (grep targets, the code-import-path smell, `git mv` recipes, the
approval gate): [references/migration-patterns.md](references/migration-patterns.md).

## List & Search Workflow

- **Search** — the `brain_search` MCP tool or `engram search`, with **the
  user's question, verbatim**. Ask it as a question, not as keywords: the ranking
  is tuned on natural questions. Report the returned chunks as
  `path ¶ heading_path`.
  - **Do not fall back to Grep when the store is down.** There is nothing local
    to grep, and grepping a repo's own source to answer a brain question yields a
    confident wrong answer. Say the store is down.
  - `--only-repo <name>` isolates one repo. Do not reach for it by default — the
    default already boosts this repo without hiding what other repos solved.
- **List (dashboard)** — **file brains only** (Glob to discover, `git log` for
  dates): a markdown table per non-empty category, rows grouped under
  `### <Category> (N items)`, columns `Name | Type | Last Modified`. The store has
  no listing tool by design; its dashboard is the web viewer, and per-repo counts
  come from `engram status`.
- **List (filename patterns)** — still Glob, over file brains only. The store is
  addressed by path, not globbed.

## Review Workflow

**When**: a documentation review or periodic checkup. Load
`references/review-checklist.md` for the procedure and report format.

Generate the dashboard, flag archival candidates (projects completed or stale
>30 days; areas and resources outdated), present the findings, and **suggest**
archives — **never auto-archive**. Execute moves only after confirmation.

## Link & Connect Workflow

**When**: the user wants to connect notes, tidy the graph, link orphans, or
update MOCs.

First load [references/linking-rules.md](references/linking-rules.md).

1. **Assess** — run the Integrity Lint for orphans and broken links.
2. **Connect orphans** — read each orphan, find semantically related documents
   (`brain_search`/`engram search` for store documents; Grep/Glob only on a
   file brain) and either **weave a contextual wikilink into that document's
   prose** or add a line in the folder's `README.md` MOC. Store edits go through
   `brain_put` (get → edit → put); never Edit a store document as if it were a
   file.
3. **Tidy MOCs** — check each folder's `README.md` actually ties its documents
   together, and fill in what is missing.
4. **Re-check** — lint again, confirm the numbers moved, and report.

**Do not force connections.** Linking unrelated documents is over-structuring and
muddies the signal. If there is no related note, leave the orphan and say so.
When orphans are zero but the graph is a **star** (everything hanging off its
MOC), the next move is the Weave Workflow, not more MOCs.

## Weave Workflow — raise neural density

**When**: orphans are handled but the brain is a star, not a mesh (low
`woven_ratio`, many `weak_nodes`). Link & Connect removes orphans (≥1 inbound);
this removes *lonely spokes* (earns a contextual, cross-folder inbound).

**File brains only** — `engram weave` walks a filesystem and cannot see the
store. For store documents, work from `brain_integrity`'s weak-node list, use
`brain_search` to find candidates, and weave with get → edit → `brain_put`.

`engram weave --json` gives two advisory lists — **missing_links**
(a document already names another note but does not link it; the cheapest
spoke-dissolver, spokes ranked first) and **concept_candidates** (a term
recurring across folders with no note of its own). Judge which are *real*, weave
them where the mention already sits, then re-run `engram lint --json` to
confirm the metrics moved. Full procedure:
[references/weave-workflow.md](references/weave-workflow.md).

## Integrity Lint Workflow

**When**: checking link integrity or hunting orphans and broken links. Also the
closing check of Create, Move, Classify & Import, Upgrade and Review.

**For store documents — the normal case:**

```bash
engram integrity
```

It reports broken links, orphans, and **weak nodes** separately, because the
store keeps a `kind` on every edge: `wiki` is a contextual link woven into prose,
`md` is a structural link from a folder MOC. That distinction is what makes
`references/linking-rules.md` machine-checkable — *the orphan check is the floor,
not the goal*, and a document reachable only from its own folder MOC is a lonely
spoke even though it passes. Clear it by weaving a contextual wikilink into
related prose; another MOC line does not.

`engram lint` applies to **file brains only**:

```bash
engram lint --json
```

In store mode it says so and does nothing — the link graph is a table there, and
`engram integrity` is the check that reads it.

Handling results:

- **broken_md_links** — a path that does not exist. Fix the path, create the
  target, or fix the typo. If the target is **permanently gone**, **de-link it**:
  turn `[text](dead/path.md)` into a plain code span `` `text` `` so the
  reference survives without a broken link. Do not turn it into a `[[wikilink]]`,
  which falsely implies an intended future note.
- **orphans** — connect inbound links (Link & Connect). The fastest fix is
  structural: orphans cluster by folder, so one per-folder README MOC clears a
  whole folder at once. Build MOCs before hunting individual links. **Gotcha:** a
  README full of `` `filename.md` `` in backticks explains why its folder is
  still all orphans — code spans are not links. Rewrite them as
  `[filename.md](filename.md)`.
- **weak_nodes** and **metrics** (`woven_ratio`, `cross_folder_link_ratio`) —
  advisory, never blocking. Weak nodes are lonely spokes. Do not fix them with
  more MOC links, which deepens the star; route to the Weave Workflow.
- **dangling_wikilinks** — warnings only. Fix typos; leave intended future notes
  alone. You may ask whether to create them.

The exit code is always 0, so it never blocks work — report and fix together.

## Capture loop — keep the brain fed

Durable thinking that stays in the chat and never lands in the brain is lost.
The bundled hooks are triggers and backstops, **not** the engine — judging what
is worth keeping is the model's job. Three triggers:

1. **Capture-as-you-go (primary)** — record a durable concept, decision or trap
   *when it crystallizes*, via the Create Workflow. Do not wait for the end of
   the session. Stay selective.
2. **Wrap-up** (`UserPromptSubmit` hook) — a sign-off injects a
   reflect-and-save instruction; act on it before replying.
3. **Backstop** (`Stop` hook) — a throttled nudge (default 30 min) for long
   sessions with no sign-off. If nothing is worth keeping, say so in one line —
   no filler.

Hooks ship with the plugin and never block. Tune with
`ENGRAM_CAPTURE_COOLDOWN_MIN` and `ENGRAM_CAPTURE_PHRASES`; disable with
`ENGRAM_CAPTURE_DISABLE=1`. Details:
[references/capture-loop.md](references/capture-loop.md).

## Session Update Review Workflow

**When**: the user asks what the brain gained *this session*. Command-triggered,
not a hook — the read-back counterpart to the capture loop.

Load [references/session-review.md](references/session-review.md). In short:
reconcile your **session memory** (notes, links, MOCs touched) with a
**cross-check** (`engram revisions` on what you wrote, or `git status --short`
on a file brain), run the Integrity Lint as the closing check, and present the
report. If nothing landed, say so in one line.

## Roadmap — designed, not yet built

A **Publish / Export** workflow: extract a curated, portable subset of the brain
(opt-in by tag or MOC) into a separate artifact — a directory, a single file, or
a static site — resolving wikilinks and stripping unpublished notes, with the
source brain left visible and untouched. The design is fixed even though the code
is not written: [references/roadmap.md](references/roadmap.md). If the user asks
to "publish", "export the brain", or "build a doc bundle", follow that design
rather than improvising one.

## Rules

1. **Auto-init**: if the category folders do not exist when a PARA operation is
   requested, create them without asking. Idempotent and safe.
2. **Never delete**: documents are never deleted. In the store, delete is
   **soft** — the body survives in revisions and `restore` brings it back, which
   is why this rule still holds there. Inactive items move to `archives/`. If the
   user explicitly asks to delete, warn them and suggest archiving; proceed only
   after they confirm twice.
3. **Hybrid structure**: an item is either a single file (`topic.md`) or a
   directory (`topic/*.md`). Choose by expected deliverables.
4. **Naming**: `kebab-case` for files and directories. Date prefixes
   (`YYYY-MM-DD-`) for time-sensitive documents. No spaces, no uppercase.
5. **Plain markdown**: no frontmatter, no special syntax. Start with an H1.
   **Never use horizontal rules (`---`)** — a parser may read one as a
   frontmatter delimiter.
6. **Archive confirmation**: moving to archives always requires explicit
   confirmation. Present candidates and wait.
7. **User language**: respond in the user's language, and write documents in
   their preference. PARA directory names are always English.
8. **Paths are the store's three coordinates**: `<owner>/<repo>/<area>/<name>.md`
   — e.g. `acme/shared/resources/foo.md` (crosses repos) or
   `acme/webapp/resources/foo.md` (repo-specific). Report them in full: owner and
   repo are not decoration, they say whose knowledge it is and who may read it.
   Both are **derived from the git remote, never chosen by hand.** File brains
   keep the relative form (`brain/projects/my-doc.md`).
9. **Migration safety**: any operation that moves files or renames the base
   (Classify & Import, Upgrade Phase A) — show the full plan first and execute
   only after approval. Never auto-migrate. A base rename also touches **non-doc
   references** (import paths, CI scripts); include those in the plan.
10. **No orphans**: every newly created document must receive at least one
    inbound link. Unlinked knowledge gets lost.
11. **Contextual links**: weave links into the prose. Do not dump a "related
    links" list at the bottom, and do not force connections.
12. **MOC as hub**: each folder's `README.md` is that folder's entry point. When
    you add or move a document, update the relevant MOC.
13. **Store first**: any read of brain content goes through the store before
    Grep/Read. If a fallback to local files happened, **say so in your answer** —
    the user needs to know the answer may be stale and that store-only documents
    were not searched. Never present a degraded answer as a normal one.
14. **Lint scope**: `engram lint` auto-detects the base and is non-blocking.
    Unresolved wikilinks are warnings, not errors — they may be intended future
    notes.
