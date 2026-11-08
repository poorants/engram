#!/usr/bin/env bash
# Off-machine copy of the engram store dump — the step that turns a file on the
# same disk into an actual backup.
#
# It runs as a SECOND ExecStart, after deploy/backup.sh, and that order is the
# point: the dump has to succeed and be reported even when the upload fails.
# The second ExecStart is a drop-in (engram-backup.service.d/upload.conf), not a
# line in engram-backup.service, so re-copying the shipped unit cannot quietly
# drop the upload step.
#
# The remote is any rclone remote (`<remote>:<bucket>[/prefix]`); the reference
# deployment uses Backblaze B2. Settings come from the environment, which the
# drop-in reads from /etc/engram/backup.env (root, 0600) —
# deploy/backup.env.example lists them, README.md "Backups" explains them.
#
# **No client-side encryption, deliberately.** Vaultwarden goes through an
# rclone crypt wrapper because losing that vault is the critical case; engram
# does not, for the same reason jackspot does not. A crypt password is the one
# failure in this setup that cannot be recovered from — lose it and the bytes
# come back from B2 unreadable. That trade is worth making for the password
# vault and not for this. At-rest encryption with keys the provider holds
# (B2's SSE-B2 bucket default) costs nothing to lose and is the recommended
# setting on the bucket.
#
# **Retention lives HERE and nowhere else.** The bucket has no lifecycle rule
# (deliberately empty, like jackspot): the rule is a generation COUNT, and a
# lifecycle can only express age. With daily backups the two nearly agree —
# which is the trap. They diverge exactly when the host was down for a few days,
# so the script would be holding N generations spanning more than N days while
# the lifecycle quietly hid the older ones. Two authorities only disagree on the
# days something else already went wrong.
#
# What a lifecycle rule MAY do is the last step: on B2 a pruned generation is
# only hidden (hard_delete=false), and `daysFromHidingToDeleting` on the bucket
# can purge hidden versions after a grace period. That decides nothing about
# WHICH generations survive, so it is not a second authority — and it lets the
# job run with a key that cannot delete (writeFiles hides; only deleteFiles
# destroys), so a stolen key cannot destroy the backups either.
set -euo pipefail

DIR=${ENGRAM_BACKUP_DIR:-/srv/engram/backups}
# No default: a built-in bucket is somebody else's bucket.
REMOTE=${ENGRAM_BACKUP_REMOTE:-}
# Generations, not days (the same names restic's `forget --keep-daily /
# --keep-monthly` uses): the newest N, plus the newest of each of the last M
# calendar months that have one, the current month included. 0 monthly (the
# default) keeps the dailies only. ENGRAM_BACKUP_KEEP_REMOTE is the pre-0.16
# name of KEEP_DAILY.
KEEP=${ENGRAM_BACKUP_KEEP_DAILY:-${ENGRAM_BACKUP_KEEP_REMOTE:-14}}
KEEP_MONTHLY=${ENGRAM_BACKUP_KEEP_MONTHLY:-0}
# true: purge the hidden versions right away (`rclone cleanup`, needs a key
# with deleteFiles). false: leave them to the bucket's daysFromHidingToDeleting
# rule — the setting for a key without deleteFiles.
PURGE_HIDDEN=${ENGRAM_BACKUP_PURGE_HIDDEN:-true}

[ -n "$REMOTE" ] || { echo "ENGRAM_BACKUP_REMOTE is not set (e.g. in /etc/engram/backup.env) — nothing was uploaded" >&2; exit 1; }
case "$KEEP" in ''|*[!0-9]*|0) echo "ENGRAM_BACKUP_KEEP_DAILY must be a positive integer, got '$KEEP'" >&2; exit 1 ;; esac
case "$KEEP_MONTHLY" in ''|*[!0-9]*) echo "ENGRAM_BACKUP_KEEP_MONTHLY must be a non-negative integer, got '$KEEP_MONTHLY'" >&2; exit 1 ;; esac

case "$PURGE_HIDDEN" in true|false) ;; *) echo "ENGRAM_BACKUP_PURGE_HIDDEN must be true or false, got '$PURGE_HIDDEN'" >&2; exit 1 ;; esac

# --immutable refuses to overwrite a name that already exists, so a clock skew
# or a re-run can never silently replace yesterday's snapshot with today's.
# --max-age 7d: a day whose upload failed is caught up by the next run, while a
# dump older than the daily window is never re-uploaded only to be pruned.
rclone copy "$DIR" "$REMOTE" \
  --include "engram-*.sql.gz" \
  --max-age 7d \
  --immutable \
  --stats-one-line \
  --log-level NOTICE

# The filename is engram-YYYYmmdd-HHMMSS.sql.gz, so a lexicographic sort IS
# chronological order, and characters 8-13 are the month.
ALL=$(rclone lsf "$REMOTE" --files-only --include "engram-*.sql.gz" | sort)

# What survives: the newest KEEP, plus (when KEEP_MONTHLY > 0) the newest of
# each of the newest KEEP_MONTHLY months. Everything else is retired.
KEEPSET=$(
  {
    printf '%s\n' "$ALL" | tail -n "$KEEP"
    if [ "$KEEP_MONTHLY" -gt 0 ]; then
      # Newest first, so the first file seen of a month is its newest.
      printf '%s\n' "$ALL" | sort -r \
        | awk -v k="$KEEP_MONTHLY" 'NF { m = substr($0, 8, 6); if (!(m in seen) && n < k) { seen[m] = 1; n++; print } }'
    fi
  } | sed '/^$/d' | sort -u
)
OLD=$(comm -23 <(printf '%s\n' "$ALL" | sed '/^$/d') <(printf '%s\n' "$KEEPSET"))

# Trimming runs only after a successful upload (set -e): a failed upload must
# never cost a generation.
if [ -n "$OLD" ]; then
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    echo "retiring: $f"
    rclone deletefile "$REMOTE/$f"
  done <<< "$OLD"
  # hard_delete is false, so deletefile only HIDES. Either this purges the
  # hidden versions now, or the bucket's daysFromHidingToDeleting rule does
  # later; with neither they accumulate forever.
  if [ "$PURGE_HIDDEN" = true ]; then
    rclone cleanup "$REMOTE"
  fi
fi

echo "on the remote: $(rclone lsf "$REMOTE" --files-only --include "engram-*.sql.gz" | wc -l) generation(s)," \
     "keeping ${KEEP} newest + newest of the last ${KEEP_MONTHLY} month(s)"
