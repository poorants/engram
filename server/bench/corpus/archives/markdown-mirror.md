# The markdown mirror (retired)

For a while after the canonical copy moved into the database, an hourly job
exported every document back out as markdown into a git repo. It was retired, and
the reasoning is worth keeping.

## Why it existed

Familiarity. People had grepped and read those files for a year, and the mirror
let that keep working while everything else moved.

## Why it was wrong

**It was mistaken for a backup.** It was not one: the real backup is `pg_dump`
([[backup-and-recovery]]), and the mirror carried no revisions at all, so it
could not restore the store even in principle.

**Edits to it were silently lost.** Anyone who fixed a typo in a mirrored file
had it overwritten by the next export, with no error and no sign anything had
happened.

**Importing it back was destructive in two ways at once.** The mirrored paths
already carried their coordinates, so a re-import applied the prefix twice and
duplicated every document — and it overwrote store-only edits with older bodies.
Both happened for real.

The store now refuses an import carrying the mirror's marker, and the reverse
direction is documented as always wrong.

## What replaced it

The web viewer, for people. `GET /api/export` for the one legitimate need the
mirror served — getting the text out if the store is gone ([[store-api]]).
