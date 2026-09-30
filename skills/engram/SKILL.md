---
name: engram
description: >
  Networked PARA knowledge brain — manages documents by PARA (Projects, Areas,
  Resources, Archives) AND weaves them into one connected knowledge graph through
  bi-directional links, MOC hubs, and integrity checks: a logical link layer over
  physical classification, so the brain grows like a network instead of a filing
  cabinet. The brain is a shared Postgres-backed STORE reached only through the
  remote `engram` MCP server's brain_* tools — searched rather than grepped, with a
  revision history per document. Use to manage, organize, search or save documents;
  save meeting notes; archive a project; import a repo's scattered docs; connect
  notes; find orphans; fix broken links; update MOCs; raise neural density; review
  what the brain gained this session; read a document's history; or check the brain
  connection. Matches intent in any language — e.g. "문서 정리", "회의록 저장",
  "노트 연결", "링크 점검", "고아 문서", "MOC 업데이트", "브레인 리뷰", "브레인 검색",
  "저장소에서 찾아줘", "이 문서 이력", "누가 언제 바꿨어", "브레인 연결 확인",
  "organize docs", "save this as a note", "connect notes", "find orphans",
  "search the brain", "document history", "is the brain connected".
---

# engram — networked PARA knowledge brain

Manage documents with the PARA method while weaving them into **one connected
knowledge network** through bi-directional links, MOC hubs, and integrity
checks. The core idea is *Networked PARA*: a logical link layer on top of
physical classification.

Two layers:

- **Management (PARA)** — create, move, archive, import, review. Areas own
  governance.
- **Connection (network)** — link documents by context, clear orphans, weave
  lonely spokes into a mesh rather than a star, catch broken links. Links own
  context. The detailed rules are in
  [references/linking-rules.md](references/linking-rules.md); load it before any
  linking work and follow it.

The thing being defended is simple: **a session dies, knowledge should not.** The
measure of success is not investigating the same question twice.

## The store — the only brain

The brain is **not a tree of files**. It is a Postgres-backed service, and a
session reaches it through **one surface: the remote MCP server registered as
`engram`**. Its tools:

| Tool | What it does |
|---|---|
| `brain_search` | ranked chunks with their heading path, not whole files; answers in tiers |
| `brain_get` | one document: body, `sha256`, outgoing links, backlinks, recent history |
| `brain_feedback` | say a document answered (or was noise) — the ranking learns from it |
| `brain_revisions` | who changed a document, when, and the note on why |
| `brain_integrity` | broken links, orphans, weak nodes, and their counts |
| `brain_put` | create or wholesale-replace a document (upsert; `note` required) |
| `brain_patch` | change part of a document — by section, anchor or line range |
| `brain_move` | move, reclassify, archive — the old path stays as an alias |

In a session they appear as `mcp__engram__brain_search` and so on. **There is
no CLI, no local copy of the brain and no fallback.** This skill ships no
scripts. If the `brain_*` tools are missing, the machine is not connected — see
**Connection** below; do not improvise another route to the brain.

**Search the store before Grep. Always.** It returns chunks with their heading
path instead of whole documents, so an answer costs a fraction of what a file
sweep does.

**Scope is derived, not chosen.** Every path is
`<owner>/<repo>/<area>/<name>.md`, and **you** supply it in full — take owner and
repo from the working repo's `git remote get-url origin`, never invent them. The
store admits only its configured owner groups; any other owner is refused with
**403**. That is the confidentiality boundary, enforced by the service rather
than by anyone remembering. A 403 means this repo's knowledge does not belong in
this brain — and there is nowhere else for this skill to put it. Say so, rather
than writing it under an owner it does not belong to.

**Your byline is your login.** Every revision is stamped with the person the
session logged in as; the `author` argument is ignored. There is nothing to
configure.

**If the store is unreachable, reading and writing both fail on the spot.** There
is no cache and no queue: a stale answer and a spool sitting somewhere both
manufacture the belief that it worked, and that belief outlives the outage. No
store means no brain — say so, rather than answering from something older than
the question.

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

Full contract: [references/store.md](references/store.md).

## Connection

Connecting a machine is a **deterministic act**, not something to improvise:

1. `claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp`
2. `/mcp` → engram → **Authenticate** (a browser login, once per machine).
3. `/mcp__engram__setup` in a session — it checks the registration, installs
   the plugin and the `engram` binary the capture hooks run, and reports what
   it fixed.

To check a connection: `claude mcp list` shows `engram` as an HTTP server and
connected; a `brain_search` answers. A registration that is a local command
(`engram mcp`) is the pre-remote client and no longer works — re-add it as
above. **Never ask the user to paste a token into the chat.** Details:
`docs/troubleshooting.md` in the engram repository.

## Quick reference

| Category | Path | Purpose | Lifespan |
|---|---|---|---|
| **Projects** | `<owner>/<repo>/projects/` | active work with a goal and a deadline | temporary — archive on completion |
| **Areas** | `<owner>/<repo>/areas/` | ongoing responsibility, no end date | persistent — review periodically |
| **Resources** | `<owner>/<repo>/resources/` | reference material and collected knowledge | persistent — update as it changes |
| **Archives** | `<owner>/<repo>/archives/` | anything above that is finished | permanent, read-only in practice |

PARA areas own one axis (actionability); the `<repo>` coordinate owns the other.
Domain knowledge stays shallow under a MOC rather than in deep folders.
`<owner>/shared/` plays "belongs to no single repo". Retiring a repo archives
only its `projects/`; its reusable knowledge stays.

## Brain boundary — what goes in, what stays out

The brain holds **thinking and knowledge** — everything you link to and revisit.
Under PARA that includes active **planning, spec and strategy documents**: they
are Projects, and the highest-value nodes, where knowledge gets applied. Keep
them in; do not leave them out for looking "output-like".

Keep a set of documents **out of the brain** (in its repo, next to the code)
only when it has its own **external delivery lifecycle** — a workflow, repo or
timeline outside your thinking network: a published manual's source, generated
reports, a blog. The test: *is this linked and re-read as part of thinking
(→ brain), or an output with its own external lifecycle (→ its repo)?*

- **Default to putting documents in the brain.** Leaving them out severs the
  project↔knowledge links that make Networked PARA worth anything, so it has to
  earn its place.
- A document outside the brain sits **outside the link network and the integrity
  check**. The brain may mention it by repo path; it must not depend on it.
- **Separation is the one call you do NOT make alone.** Make every other call
  autonomously; keep a document set out only on explicit request.

## Create Workflow

**When**: the user wants a new document.

1. **Category** — Projects (deadline/goal), Areas (ongoing), or Resources
   (reference). Unsure? Load `references/para-categories.md`.
2. **Structure** — a single document for one topic, or a `kebab-case/` group of
   documents for multi-deliverable work.
3. **Path** — `<owner>/<repo>/<area>/<name>.md`, `kebab-case`; date-prefix
   time-sensitive items (`YYYY-MM-DD-topic.md`). First `brain_search` for the
   topic — updating the document that already exists beats writing a second one.
4. **Write** — plain markdown, starting with an H1. No frontmatter, no `---`
   rules. Title and headings in the words someone would actually ask; the
   conclusion first in each section; identifiers verbatim. Save with `brain_put`
   (body plus `note`; `dryRun: true` first for a new document or a large
   replacement). `brain_put` is for a NEW document or a wholesale replacement;
   changing part of one that already exists is `brain_patch`.
   The note lands in the revision history, which is what replaces `git log` for
   the brain. A **403** means the owner is not admitted — tell the user rather
   than rerouting it.
5. **Connect** — secure at least one inbound link, weave contextual
   `[[wikilinks]]` into the prose, add it to the curating MOC where one exists,
   and ground a `resources/` doc to an `areas/` or `projects/` one. Follow
   [references/linking-rules.md](references/linking-rules.md).
6. **Report** — full path, PARA category, a brief summary, and what now links to it.

Templates and full per-step detail:
[references/create-workflow.md](references/create-workflow.md).

## Editing an existing document

`brain_get` first — it returns the body and its `sha256`. Then `brain_patch`
with the smallest address that fits (`section`, a unique `anchor`, or a line
range), `expect` copied from what you read, and `baseSha256` from the read. One
link is a one-line change; re-sending the whole document with `brain_put` costs
the document's size, not the edit's, and races anyone else editing it. A refused
patch (`expect` mismatch, stale `baseSha256`) means the document moved under
you — read it again, never force it.

## Move Workflow

**When**: relocating a document between PARA categories or repo coordinates.

Common moves: `projects/`→`archives/` (completed), `areas/`→`archives/` (ended),
`resources/`→`archives/` (outdated), `archives/`→`projects/` (reactivated),
`<owner>/<repo>/`→`<owner>/shared/` (promotion).

`brain_move` with the current path and the destination (`dryRun: true` to
preview). The store records the old path as an **alias**, so `[[old-name]]`
written elsewhere keeps resolving, and edges point at an immutable document id.

Steps: find the source (`brain_search` or `brain_get`) → determine the target
category (ask or infer) → move → report `source → destination` and the alias →
close with the Integrity Check.

**Never delete documents. Move them to archives.** There is no delete tool:
deletion removes a document from search; archiving reclassifies it.

## Classify & Import Workflow

**When**: a repo the store admits holds scattered, *unclassified* knowledge
documents (`docs/notes/`, design memos, meeting logs) that belong in the brain.

Six steps: **Scan** (Glob `**/*.md`, `**/*.txt` in the repo, excluding code
docs, `.git/`, `node_modules/`, root metadata) → **Classify** (read each doc,
assign an area and a `<repo>` coordinate via `references/para-categories.md` and
the routing test above, flag the unclear) → **Present a plan** (a
Classified/Manual/Skipped table with target store paths; name the collisions
with documents the store already has — `brain_search` each title) → **Confirm**
(execute only after approval) → **Execute** (`brain_put` each, with a `note`
naming the source file) → **Report**, then Link & Connect and the Integrity Check.

Whether the repo copies are then removed is the user's call, never yours — a
repo doc may be load-bearing (linked from code, CI, a README).

Full detail, plan and report templates, exclusion and classification heuristics:
[references/import-patterns.md](references/import-patterns.md).

## Search Workflow

- **Search** — `brain_search` with **the user's question, verbatim**. Ask it as a
  question, not as keywords: the ranking is tuned on natural questions. Pass the
  working repo as `boostRepo`. Report the returned chunks as
  `path ¶ heading_path`.
  - **Tiers.** The default page (tier 1) is up to four documents as snippets,
    one chunk each — a quarter of the tokens of a full page, same recall. If
    the answer is not on it, call again with the **same question** and the tier
    the result's `next` names: 2 is full chunks, 3 adds archives and widens.
    Raise the tier before rephrasing; a new phrasing is a new search, and it
    breaks the attribution a vote needs.
  - **Vote when a document answered.** `brain_feedback` with the path (the
    session's last search is attached by itself). The ranking learns from it:
    the document comes first for that question and ones like it. `noise` is the
    other button. Opening a hit with `brain_get` is a weaker vote recorded
    without any call. A vote is a bonus, never a filter.
  - **Do not fall back to Grep when the store is down.** There is nothing local
    to grep, and grepping a repo's own source to answer a brain question yields a
    confident wrong answer. Say the store is down.
  - `onlyRepos` isolates repos. Do not reach for it by default — `boostRepo`
    already lifts this repo without hiding what other repos solved.
- **Browse** — the store has no listing tool by design. Navigate from the hubs:
  `brain_get <owner>/<repo>/README.md` (the repo hub MOC) and each area's MOC,
  following their links and backlinks. A person browses the same store in the
  web viewer.

## Review Workflow

**When**: a documentation review or periodic checkup. Load
`references/review-checklist.md` for the procedure and report format.

Walk the hub MOCs, read each document's age from `brain_revisions`, flag
archival candidates (projects completed or stale >30 days; areas and resources
outdated), present the findings, and **suggest** archives — **never
auto-archive**. Execute moves only after confirmation.

## Link & Connect Workflow

**When**: the user wants to connect notes, tidy the graph, link orphans, or
update MOCs.

First load [references/linking-rules.md](references/linking-rules.md).

1. **Assess** — `brain_integrity` for orphans and broken links.
2. **Connect orphans** — `brain_get` each orphan, find semantically related
   documents with `brain_search` (its title and its central claim, as
   questions), and either **weave a contextual wikilink into that document's
   prose** or add a line in the curating MOC. Every edit is `brain_patch` —
   see "Editing an existing document".
3. **Tidy MOCs** — check each hub MOC actually ties its documents together, and
   fill in what is missing.
4. **Re-check** — `brain_integrity` again, confirm the counts moved, and report.

**Do not force connections.** Linking unrelated documents is over-structuring and
muddies the signal. If there is no related note, leave the orphan and say so.
When orphans are zero but the graph is a **star** (everything hanging off its
MOC), the next move is the Weave Workflow, not more MOCs.

## Weave Workflow — raise neural density

**When**: orphans are handled but the brain is a star, not a mesh (many
`weak_nodes`). Link & Connect removes orphans (≥1 inbound); this removes *lonely
spokes* (earns a contextual, cross-folder inbound).

Work from `brain_integrity`'s `weak_nodes` list: for each spoke, `brain_search`
its title and core idea to find documents that already **mention** it without
linking it (the cheapest spoke-dissolver), and look across results for a
**concept** that recurs in several areas with no note of its own. Judge which
are *real*, weave them with `brain_patch` where the mention already sits, then
re-run `brain_integrity` to confirm `counts.weak` fell. Full procedure:
[references/weave-workflow.md](references/weave-workflow.md).

## Integrity Check Workflow

**When**: checking link integrity or hunting orphans and broken links. Also the
closing check of Create, Move, Classify & Import and Review.

`brain_integrity` (optionally `limit`) reports `broken_links`, `orphans` and
**`weak_nodes`** separately, with `counts` (including `by_kind`), because the
store keeps a `kind` on every edge: `wiki` is a contextual link woven into prose,
`md` is a structural link from a MOC. That distinction is what makes
`references/linking-rules.md` machine-checkable — *the orphan check is the floor,
not the goal*, and a document reachable only from a MOC is a lonely spoke even
though it passes. Clear it by weaving a contextual wikilink into related prose;
another MOC line does not.

Handling results:

- **broken_links** — a link whose target does not exist. Fix the path, create the
  target, or fix the typo. If the target is **permanently gone**, **de-link it**:
  turn `[text](dead/path.md)` into a plain code span `` `text` `` so the
  reference survives without a broken link. Do not turn it into a `[[wikilink]]`,
  which falsely implies an intended future note. An unresolved wikilink may be an
  intended future note — fix typos, leave intentions alone, and you may ask
  whether to create them.
- **orphans** — connect inbound links (Link & Connect). The fastest fix is
  structural: orphans cluster by area, so one hub MOC clears a whole group at
  once. Build MOCs before hunting individual links. **Gotcha:** a MOC full of
  `` `filename.md` `` in backticks explains why its group is still all orphans —
  code spans are not links. Rewrite them as real links.
- **weak_nodes** — advisory, never blocking. They are lonely spokes. Do not fix
  them with more MOC links, which deepens the star; route to the Weave Workflow.

It never blocks work — report and fix together. `truncated: true` means a list
was cut at `limit`; raise it or work in passes.

## Capture loop — keep the brain fed

Durable thinking that stays in the chat and never lands in the brain is lost.
The bundled hooks are triggers and backstops, **not** the engine — judging what
is worth keeping (and what a question really wants) is the model's job.

0. **Recall** (`SessionStart` hook) — a rule injected once when a session
   opens: a knowledge question searches the brain before it greps the working
   tree. It performs no search — the time to search is when there is a question.

Three write-side triggers:

1. **Capture-as-you-go (primary)** — record a durable concept, decision or trap
   *when it crystallizes*, via the Create Workflow. Do not wait for the end of
   the session. Stay selective.
2. **Wrap-up** (`UserPromptSubmit` hook) — a sign-off ("wrap up", "수고했어", …)
   injects a reflect-and-save instruction; act on it before replying.
3. **Backstop** (`Stop` hook) — a throttled nudge (default 30 min) for long
   sessions with no sign-off. If nothing is worth keeping, say so in one line —
   no filler.

The read-side counterpart: when a brain document **answered** something in the
session, say so with `brain_feedback` before it ends. That is what makes the
next session's first page right more often — the write side keeps the brain
fed, the vote keeps its search honest.

The hooks run `engram hook`, speak in any repo with a git origin (narrow it with
`ENGRAM_CAPTURE_OWNERS`) and are silent everywhere else. They never block. Tune
with `ENGRAM_CAPTURE_COOLDOWN_MIN` and `ENGRAM_CAPTURE_PHRASES`; disable with
`ENGRAM_CAPTURE_DISABLE=1`. Details:
[references/capture-loop.md](references/capture-loop.md).

## Session Update Review Workflow

**When**: the user asks what the brain gained *this session*. Command-triggered,
not a hook — the read-back counterpart to the capture loop.

Load [references/session-review.md](references/session-review.md). In short:
reconcile your **session memory** (notes, links, MOCs touched) with a
**cross-check** (`brain_revisions` on each path you wrote), run the Integrity
Check as the closing check, and present the report. If nothing landed, say so in
one line.

## Roadmap — designed, not yet built

A **Publish / Export** workflow: extract a curated, portable subset of the brain
(opt-in by MOC or path list) into a separate artifact — a directory, a single
file, or a static site — resolving wikilinks and stripping unpublished notes,
with the store left untouched. The design is fixed even though the code is not
written: [references/roadmap.md](references/roadmap.md). If the user asks to
"publish", "export the brain", or "build a doc bundle", follow that design rather
than improvising one.

## Rules

1. **Store only**: the brain is the store, reached through the `brain_*` MCP
   tools. There is no local brain and no CLI. Never write brain content as files
   in a repo "for now".
2. **Never delete**: documents are never deleted — there is no delete tool.
   Inactive items move to `archives/`. If the user explicitly asks to delete,
   explain that archiving is the contract and archive after they confirm.
3. **Hybrid structure**: an item is either a single document (`topic.md`) or a
   group (`topic/*.md`). Choose by expected deliverables.
4. **Naming**: `kebab-case` for documents and groups. Date prefixes
   (`YYYY-MM-DD-`) for time-sensitive documents. No spaces, no uppercase.
5. **Plain markdown**: no frontmatter, no special syntax. Start with an H1.
   **Never use horizontal rules (`---`)** — a parser may read one as a
   frontmatter delimiter.
6. **Archive confirmation**: moving to archives always requires explicit
   confirmation. Present candidates and wait.
7. **User language**: respond in the user's language, and write documents in
   their preference. PARA area names are always English.
8. **Paths are the store's three coordinates**: `<owner>/<repo>/<area>/<name>.md`
   — e.g. `acme/shared/resources/foo.md` (crosses repos) or
   `acme/webapp/resources/foo.md` (repo-specific). Report them in full: owner
   and repo are not decoration, they say whose knowledge it is and who may read
   it. Both are **derived from the git remote, never chosen by hand.**
9. **Bulk safety**: any operation that writes or moves many documents (Classify &
   Import, a batch of archives) — show the full plan first and execute only
   after approval. Never auto-migrate.
10. **No orphans**: every newly created document must receive at least one
    inbound link. Unlinked knowledge gets lost.
11. **Contextual links**: weave links into the prose. Do not dump a "related
    links" list at the bottom, and do not force connections.
12. **MOC as hub**: a hub MOC (`<owner>/<repo>/README.md`, an area's `README.md`)
    is its group's entry point. When you add or move a document a MOC curates,
    update that MOC — and remember a MOC line is structural: it clears an
    orphan, never a weak node.
13. **Store first, and no silent substitute**: any read of brain content goes
    through `brain_search`/`brain_get` before Grep/Read. If the store is down or
    the tools are missing, **say so in your answer** — never present an answer
    from the working tree as if the brain had given it.
14. **Integrity is advisory**: `brain_integrity` never blocks work. Unresolved
    wikilinks may be intended future notes.
