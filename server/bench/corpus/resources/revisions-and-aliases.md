# History, moves, and why there is no delete

The store IS the canonical copy. There is no markdown mirror and no file the
documents also live in. That single fact sets the rules below.

## Every write keeps the previous body

Before a body is overwritten, the old one is written to `revisions` with its
author and the `note` explaining why it changed. That note is the commit message,
and the revision list is the git log — there is nowhere else for either to
happen. A revision row duplicates the path so the history survives even if the
document is later removed.

This is also why `note` is required on a write and an empty one is refused. A
history of "updated" tells you nothing a timestamp did not.

## Moving keeps the old address alive

`POST /api/doc/{path}/move` relocates a document and records the old path in
`aliases`. A `[[old-name]]` somebody wrote in another document keeps resolving,
and so do relative markdown links.

**A file vault could never have this.** Renaming a file left two options: edit
every referring document, or leave the links broken. The database version has a
third, because an edge points at an immutable document id rather than at a name —
which is why a move breaks nothing that had already resolved.

## Delete is soft, and not exposed as a tool

`DELETE` marks `deleted_at` and clears the derived data; the body stays in
`revisions` and `restore` brings it back. Even so, no agent-facing tool exposes
it. The contract is **never delete, move to archives** ([[para-areas]]), because
deleting removes a document from search while archiving reclassifies it — and the
usual meaning of "remove this from the brain" is the second one.

Removing something from a brain almost always means "I do not look at this any
more", not "this never existed".

An export of the bodies is no substitute for either the history or the aliases,
which is one of the reasons the [[markdown-mirror]] was retired.

## The consequence for backups

With no copy anywhere else, a lost database is lost knowledge. See
[[backup-and-recovery]].
