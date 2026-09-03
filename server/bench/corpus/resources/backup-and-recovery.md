# What a backup protects against

The canonical copy is `docs.body` and `revisions` in Postgres, and there is no
copy of either anywhere else ([[revisions-and-aliases]]). Backups are therefore
not optional.

## The dump

`deploy/backup.sh` runs `pg_dump` INSIDE the database container — so no postgres
client is installed on the host, and the dump tool can never be a different
version from the server. The output is gzipped, written as `.part` and renamed at
the end, so a partial file is never mistaken for a backup.

It then checks the dump actually contains tables. **Empty dumps accumulating
quietly is the worst outcome** — the failure is invisible until the day it is
needed.

## What it does not protect against

The dump sits on the same disk. That covers a logical accident — a document
deleted by mistake, a bad bulk import — and covers nothing at all if the disk
dies. It becomes a real backup only once something copies it off the machine.
That step is deliberately left out of `backup.sh`: the dump must succeed and be
reported even when the upload fails.

Running it is part of [[store-operations]]. The systemd timer uses
`Persistent=true` so a run missed by a reboot is caught up. A backup quietly absent for a few days is the most common way this goes
wrong.

## Export is not a backup

`GET /api/export` returns every live document verbatim. It is the last way a
person gets their text out if the store is gone, and it is worth having for that
alone — but it carries no revisions and no soft-deleted rows, so it cannot
restore the store. There is deliberately no route back in from an export:
re-importing an old dump overwrites edits made since, so the reverse direction is
always wrong.
