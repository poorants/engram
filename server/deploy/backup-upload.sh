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
# The remote is any rclone remote; the reference deployment uses Backblaze B2
# (`<remote>:<bucket>`). Settings come from the environment, which the drop-in
# reads from /etc/engram/backup.env (root, 0600) — see README.md, "Backups".
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
set -euo pipefail

DIR=${ENGRAM_BACKUP_DIR:-/srv/engram/backups}
REMOTE=${ENGRAM_BACKUP_REMOTE:-b2:poorants-engram-backup}
KEEP=${ENGRAM_BACKUP_KEEP_REMOTE:-14}          # newest N generations: two weeks of dailies
# Also keep the FIRST generation of each of the last N calendar months, on top
# of the dailies. 0 (the default) keeps the dailies only.
KEEP_MONTHLY=${ENGRAM_BACKUP_KEEP_MONTHLY:-0}

case "$KEEP" in ''|*[!0-9]*|0) echo "ENGRAM_BACKUP_KEEP_REMOTE must be a positive integer, got '$KEEP'" >&2; exit 1 ;; esac
case "$KEEP_MONTHLY" in ''|*[!0-9]*) echo "ENGRAM_BACKUP_KEEP_MONTHLY must be a non-negative integer, got '$KEEP_MONTHLY'" >&2; exit 1 ;; esac

# --max-age keeps the scan to what this run just produced; --immutable refuses
# to overwrite a name that already exists, so a clock skew or a re-run can never
# silently replace yesterday's snapshot with today's.
rclone copy "$DIR" "$REMOTE" \
  --include "engram-*.sql.gz" \
  --max-age 6h \
  --immutable \
  --stats-one-line \
  --log-level NOTICE

# The filename is engram-YYYYmmdd-HHMMSS.sql.gz, so a lexicographic sort IS
# chronological order, and characters 8-13 are the month.
ALL=$(rclone lsf "$REMOTE" --files-only --include "engram-*.sql.gz" | sort)

# What survives: the newest KEEP, plus (when KEEP_MONTHLY > 0) the oldest of
# each of the newest KEEP_MONTHLY months. Everything else is retired.
KEEPSET=$(
  {
    printf '%s\n' "$ALL" | tail -n "$KEEP"
    if [ "$KEEP_MONTHLY" -gt 0 ]; then
      printf '%s\n' "$ALL" | awk 'NF { m = substr($0, 8, 6); if (!(m in seen)) { seen[m] = 1; print } }' \
        | tail -n "$KEEP_MONTHLY"
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
  # hard_delete is false, so deletefile only HIDES. Without this the hidden
  # versions accumulate forever now that the bucket has no lifecycle rule to
  # sweep them.
  rclone cleanup "$REMOTE"
fi

echo "on the remote: $(rclone lsf "$REMOTE" --files-only --include "engram-*.sql.gz" | wc -l) generation(s)," \
     "keeping ${KEEP} newest + first of the last ${KEEP_MONTHLY} month(s)"
