# Networked Knowledge — Linking Rules

The rules for the **logical link layer** engram lays on top of PARA folders
(physical classification). They grow the store into a brain-like network of
connected documents. Apply them whenever creating, moving, or reviewing docs.

## Core idea: Networked PARA

PARA folders alone isolate information inside folders. Adding bi-directional
links combines **physical classification (folders)** with **logical
relationships (links)**.

- **Folders (physical)**: own the document's lifecycle and governance (who edits
  it). Projects / Areas / Resources / Archives.
- **Links (logical)**: connect documents by context regardless of which folder
  they live in, forming a web that crosses folder boundaries.

### Connected vs woven — the orphan check is the floor, not the goal

Passing the orphan check (≥1 inbound link) only proves a node is *connected*. It
does **not** prove the network is brain-like. The most common failure is a **star
topology**: every document hangs off its folder's MOC and nothing else, so a
single MOC link clears the orphan check while the graph stays a tree drawn with
link lines. In a graph view this looks like a few big hubs with radial spokes, not
a mesh.

A node is **woven** (not merely connected) when a *content document* — not just a
MOC — links it, ideally across a folder boundary. Multi-path reachability is what
makes retrieval associative; an inbound link from the node's own folder MOC just
restates where the file already lives.

`brain_integrity` quantifies this: the `weak_nodes` list — documents whose
*only* inbound links are structural, typically from a MOC (lonely spokes) — and
`counts` (`weak`, and `by_kind`, which splits edges into contextual `wiki` and
structural `md`). Drive the weak count down and the share of contextual,
cross-folder links up; the **Weave Workflow** finds the concrete links to add.

## Linking rules

### 1. Bi-directional linking

- The store records **both** forms as edges and tags them: a wikilink is `kind=wiki`
  (contextual), a markdown relative link is `kind=md` (structural — this is what MOC
  entries produce). `brain_integrity` uses the tag to separate orphans from
  **weak nodes**, so "linked from the MOC" no longer looks the same as "woven into the
  network". Only a wikilink in prose clears a weak node.
- Use `[[filename]]` (Obsidian wikilink) or `[display text](relative/path.md)`
  (markdown relative link). Prefer wikilinks so nodes are recognized in Graph
  View and backlinks activate.
- Wikilinks resolve by filename (stem), so moving a folder does not break them.
  They are fragile to filename changes, so keep names stable.
- **Match the dominant style of the documents around it.** Both forms count as an
  inbound link, so when an existing group already uses one style consistently (e.g.
  markdown relative links throughout), follow it rather than mixing in wikilinks —
  but remember only a wikilink in prose counts as *woven*.

### 2. Prefer contextual links

Avoid dumping a "related documents" list at the bottom of a file. Weave links
naturally into the prose.

- Bad: a trailing `Related: [[foo]]` list.
- Good: "the encryption module must follow the national standard
  [[fips-140-validation]]" — the link sits inside the sentence.

### 3. MOC (Map of Content) = hub node

A repo's hub (`<owner>/<repo>/README.md`) and each group's `README.md` act as
MOCs: entry dashboards that tie individual notes together by logical
order/category. When you add a document to a group a MOC curates, add a one-line
link to it from that MOC so it is reachable from the entry point.

- **MOC entries must be REAL links, not backtick filenames.** A table listing
  `` `00-overview.md` `` in code spans looks like an index, but the store strips
  inline code before counting links — so those docs stay orphans. Always write
  `[00-overview.md](00-overview.md)`. This is the #1 silent reason a group with a
  fully populated MOC still shows every doc as an orphan.
- **MOCs are orphan-exempt.** `brain_integrity` never reports a `README.md` as
  an orphan or a weak node — MOCs are structural hubs. So a MOC needs no inbound link of its own to be
  "connected," though linking it from a parent MOC is still good navigation hygiene.
- **A per-group MOC is the highest-leverage orphan fix.** Orphans cluster
  by group; one MOC that links every doc in its group clears that whole
  group's orphans at once — far faster than hunting one contextual link per
  orphan. Build MOCs first, then weave the genuinely cross-cutting contextual links.

## Knowledge-network management rules

1. **Atomic note (one point)**: a document covers exactly one concept, rule, or
   project. When a topic expands, create a new document and link to it. Oversized
   documents blur the propagation power of links.

   But **atomic means "one linkable idea," not "the smallest possible fragment."**
   Discernment test: *would you ever link to or reuse this concept independently
   from somewhere else?* If yes, it deserves its own note; if it only ever appears
   in one context, keep it inline. Shattering everything into tiny stubs is
   over-structuring (see Pitfalls) — it adds navigation overhead and loses
   cohesion. Some documents are legitimately one concept even when long: a single
   spec, a meeting log (one event, chronological), a reference table.

   **Migrate opportunistically, never big-bang.** Don't rewrite the whole brain at
   once (it invites link rot and wasted effort). Apply atomicity to new notes, and
   split an existing bloated doc only when you're already editing it and notice it
   packs several independently-linkable concepts — then split and wire the links.

2. **No orphan nodes**: every newly added document must receive at least one
   inbound link from an existing MOC (`README.md`) or a related document.
   Unlinked knowledge gets lost. (`brain_integrity` detects orphans.)

3. **No lonely spokes (earn the second link)**: a MOC link is *necessary but not
   sufficient*. Beyond the structural MOC inbound, a content document should earn
   at least one **contextual inbound from another content document**, preferably
   **across a folder boundary** — that second link is what turns a folder spoke
   into a woven node. Don't force it: if a document genuinely relates to nothing
   else, leave it as an acknowledged leaf rather than inventing a link (forcing is
   over-structuring). The lever for *non-leaf* spokes is the Weave Workflow —
   promote a recurring concept to a shared node and route the spokes through it,
   or add the missing link where one doc already mentions another by name.

4. **Ground reference material**: when citing external research (`resources/`),
   link it to an `areas/`/`projects/` document that holds your own
   interpretation, grounding the external knowledge in your network.

## Pitfalls (what practitioners warn about)

- **Collector's fallacy**: mistaking *collecting* information for *knowing* it.
  Material that is only collected has not been read. Inboxes (seeds, ideas, etc.)
  become a dead pile without a periodic ritual to digest and promote them.
- **Over-structuring**: when maintaining the system becomes the goal and actual
  thinking/output stops. Do not pile on excessive rules, tags, or numbers.
- **Link rot**: links breaking due to filename changes. Prevent it with stable
  naming, `brain_move` (which leaves an alias) rather than re-creating a
  document under a new name, and periodic `brain_integrity` checks.
- **Star topology (folder-replicated links)**: clearing orphans with MOC links
  alone and stopping there. The orphan count hits zero but every doc is a lonely
  spoke, so the "network" just redraws the folder tree — connected but not woven
  (see "Connected vs woven"). Watch `weak_nodes`, not just orphans.

## Order of operations (when working on docs)

1. After creating or moving a document, weave contextual links into the prose —
   reach for at least one link **across a folder boundary**, not only within the
   same folder.
2. Add a one-line entry to the curating MOC to avoid orphans.
3. Before finishing, run `brain_integrity` to find and repair broken links and
   orphans, and to read `weak_nodes` and `counts`.
4. Periodically (or when `counts.weak` is high) run the **Weave Workflow** to
   dissolve lonely spokes into woven nodes.
