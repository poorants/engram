#!/bin/bash
# Logical backup of the store. It is not optional: the canonical copy lives in
# this database and there is no copy of it anywhere else.
#
# The dump this makes sits **on the same disk**. That protects against a logical
# accident (a document deleted by mistake) and not at all against a physical one
# (the disk dies). It becomes a real backup only once something pulls it off the
# machine — see the note at the bottom.
set -euo pipefail

DIR=${ENGRAM_BACKUP_DIR:-/srv/engram/backups}
KEEP=${ENGRAM_BACKUP_KEEP:-14}
CONTAINER=${ENGRAM_DB_CONTAINER:-engram-db-1}
STAMP=$(date +%Y%m%d-%H%M%S)
OUT="$DIR/engram-$STAMP.sql.gz"

mkdir -p "$DIR"
# pg_dump is run INSIDE the container: no postgres client has to be installed on
# the host, and the dump tool can never be a different version from the server.
docker exec "$CONTAINER" pg_dump -U engram -d engram --clean --if-exists \
  | gzip -6 > "$OUT.part"
mv "$OUT.part" "$OUT"      # named last, so a partial file is never mistaken for a backup

# Check the dump actually has content. Empty dumps accumulating quietly is the
# worst outcome — the failure is invisible until the day it is needed.
if [ "$(gzip -dc "$OUT" | grep -c 'CREATE TABLE')" -lt 4 ]; then
  echo "the dump contains no tables — treating this as a failure: $OUT" >&2
  exit 1
fi

find "$DIR" -name 'engram-*.sql.gz' -mtime +"$KEEP" -delete
echo "$OUT ($(du -h "$OUT" | cut -f1)) · keeping ${KEEP} days · $(ls -1 "$DIR"/engram-*.sql.gz | wc -l) on disk"

# Off-machine copy: add whatever your environment uses as a second ExecStart in
# engram-backup.service (rsync, rclone, aws s3 cp, …). Keeping it out of this
# script is deliberate — the dump must succeed and be reported even when the
# upload fails.
