# The four PARA areas

The area answers one question: **how actionable is this, and when does it stop
being so?** It is orthogonal to the repo coordinate in [[document-addressing]],
which answers whose knowledge it is. Merging the two loses both.

| Area | What lives there | Lifespan |
|---|---|---|
| `projects` | active work with a goal and an end | temporary — archived on completion |
| `areas` | an ongoing responsibility with no end date | persistent — reviewed periodically |
| `resources` | reference material and collected knowledge | persistent — updated as it changes |
| `archives` | anything from the three above that is finished | permanent, read-only in practice |

## Archives are excluded from search by default

A search does not return archived documents unless it asks for them. That is the
point of the area: an archived runbook for a system that no longer exists is not
wrong, it is just no longer the answer, and having it compete with the current
one is worse than not having it.

## Moving between areas

Reclassification is a move, not an edit, and the store keeps the old path as an
alias so nothing that linked to the document breaks. The common transitions:

- `projects` → `archives` when the work finishes
- `areas` → `archives` when a responsibility ends
- `resources` → `archives` when the knowledge is superseded
- `archives` → `projects` when something is picked back up

**Archiving is how a document is retired; there is no delete.** The reasoning is
in [[revisions-and-aliases]].

A repo hub MOC (`<owner>/<repo>/README.md`) has no area at all. It is the entry
point to a repo's documents, which makes it structural rather than actionable.
