#!/usr/bin/env bash
# Restore rehearsal: prove that the newest OFF-MACHINE backup restores, and
# that what it restores is the store.
#
# A backup nobody has restored is a hope. This does the restore end to end,
# every month, on the production host, without touching production:
#
#   1. take the newest generation from the remote (or the dump file given as
#      the only argument), check it is a whole gzip, and — when the same file
#      is still on local disk — that the remote copy is byte-identical;
#   2. restore it into a THROWAWAY postgres container (no network, memory
#      capped, removed on exit whatever happens) with ON_ERROR_STOP, so a dump
#      that half-restores fails rather than passing;
#   3. check the restore against itself: every body's sha256, recomputed
#      from the restored text, must match the hash stored beside it;
#   4. compare the restore with the live database:
#      - revisions are append-only, so every restored revision must be in live,
#        identical (id, path and the RECOMPUTED body hash); a live revision
#        inside the dump's id range that the restore lacks is reported (ids are
#        not assigned in commit order, so one committed just after the dump
#        can sit below its highest id);
#      - documents, keyed by id: none of the restored ids may be missing from
#        live, and every live document with no write since the dump must have
#        the same body sha256 in the restore;
#      - the document counts are reported, live and restored.
#
# Any failure exits non-zero, which fails the unit; a failed unit is what the
# host's monitoring reports. It never writes to the live database.
set -euo pipefail

REMOTE=${ENGRAM_BACKUP_REMOTE:-}
DIR=${ENGRAM_BACKUP_DIR:-/srv/engram/backups}
LIVE=${ENGRAM_DB_CONTAINER:-engram-db-1}
IMAGE=${ENGRAM_REHEARSAL_IMAGE:-postgres:17}
MEMORY=${ENGRAM_REHEARSAL_MEMORY:-512m}
# Scratch space for the download and the throwaway database: a dump is about a
# quarter of the database it restores to, so leave room for both.
SCRATCH=${ENGRAM_REHEARSAL_SCRATCH:-/var/tmp}

umask 077
export LC_ALL=C      # comm needs both sides sorted the same way
fail() { echo "rehearsal FAILED: $*" >&2; exit 1; }

WORK=$(mktemp -d "$SCRATCH/engram-rehearsal.XXXXXX")
NAME="engram-rehearsal-$$"
cleanup() {
  docker rm -f -v "$NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# -- 1. the generation -------------------------------------------------------
if [ $# -ge 1 ]; then
  SRC=$1
  [ -f "$SRC" ] || fail "no such dump: $SRC"
  GEN=$(basename "$SRC")
  cp "$SRC" "$WORK/$GEN"
  echo "generation: $GEN (local file)"
else
  [ -n "$REMOTE" ] || fail "ENGRAM_BACKUP_REMOTE is not set (e.g. in /etc/engram/backup.env); pass a dump file to rehearse a local one"
  GEN=$(rclone lsf "$REMOTE" --files-only --include "engram-*.sql.gz" | sort | tail -n 1)
  [ -n "$GEN" ] || fail "no engram-*.sql.gz generation on $REMOTE"
  rclone copyto "$REMOTE/$GEN" "$WORK/$GEN"
  echo "generation: $GEN (from $REMOTE)"
fi
DUMP="$WORK/$GEN"
gzip -t "$DUMP" || fail "$GEN is not a whole gzip stream"
SHA=$(sha256sum "$DUMP" | cut -d' ' -f1)
echo "sha256: $SHA ($(du -h "$DUMP" | cut -f1))"
if [ $# -eq 0 ] && [ -f "$DIR/$GEN" ]; then
  LOCAL_SHA=$(sha256sum "$DIR/$GEN" | cut -d' ' -f1)
  [ "$SHA" = "$LOCAL_SHA" ] || fail "the remote copy of $GEN differs from the local one ($LOCAL_SHA)"
  echo "remote copy is byte-identical to $DIR/$GEN"
fi

# -- 2. the throwaway restore ------------------------------------------------
# No network, so trust auth reaches nothing but this script's own docker exec.
# fsync off: durability of a database that is deleted in a minute is not a
# property worth paying for on a small shared disk.
docker run -d --name "$NAME" --network none --memory "$MEMORY" \
  -e POSTGRES_USER=engram -e POSTGRES_DB=engram -e POSTGRES_HOST_AUTH_METHOD=trust \
  "$IMAGE" -c shared_buffers=32MB -c fsync=off -c full_page_writes=off \
  -c synchronous_commit=off -c max_wal_size=1GB >/dev/null

# -h 127.0.0.1: the image's init phase runs a server on the unix socket only,
# so a TCP answer means the real server is up, not the init one.
for _ in $(seq 1 60); do
  docker exec "$NAME" pg_isready -q -h 127.0.0.1 -U engram -d engram 2>/dev/null && break
  sleep 2
done
docker exec "$NAME" pg_isready -q -h 127.0.0.1 -U engram -d engram || fail "the throwaway postgres did not start"

q()  { docker exec -i "$NAME" psql -X -q -At -U engram -d engram -v ON_ERROR_STOP=1 -c "$1"; }
ql() { docker exec -i "$LIVE" psql -X -q -At -U engram -d engram -v ON_ERROR_STOP=1 -c "$1"; }

# The dump assigns ownership to whatever roles own objects in production. A
# fresh cluster has only `engram`, so create the others (no login) first.
ROLES=$(gzip -dc "$DUMP" \
  | grep -oE '(OWNER TO [a-z_][a-z0-9_]*;|^GRANT .* TO [a-z_][a-z0-9_]*;)' \
  | sed -E 's/.* ([a-z_][a-z0-9_]*);$/\1/' \
  | grep -vxE 'engram|postgres|public' | sort -u || true)
for r in $ROLES; do
  q "DO \$\$BEGIN CREATE ROLE $r NOLOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END\$\$" >/dev/null
done

T0=$(date +%s)
gzip -dc "$DUMP" | docker exec -i "$NAME" psql -X -q -U engram -d engram -v ON_ERROR_STOP=1 -o /dev/null \
  || fail "the dump did not restore cleanly"
echo "restored in $(( $(date +%s) - T0 ))s${ROLES:+ (roles created: $(printf '%s' "$ROLES" | tr '\n' ' '))}"

# -- 3. the restore against itself --------------------------------------------
MAXREV=$(q "SELECT coalesce(max(id), 0) FROM revisions")
MAXDOC=$(q "SELECT coalesce(max(id), 0) FROM docs")
[ "$MAXDOC" -gt 0 ] || fail "the restore holds no documents"

BODYHASH="encode(sha256(convert_to(body, 'UTF8')), 'hex')"
BAD=$(q "SELECT (SELECT count(*) FROM docs WHERE $BODYHASH <> sha256)
              + (SELECT count(*) FROM revisions WHERE $BODYHASH <> sha256)")
[ "$BAD" -eq 0 ] || fail "$BAD restored body/bodies do not match their stored sha256"

# -- 4. compare with live ----------------------------------------------------
REVROWS="SELECT id || ' ' || path || ' ' || $BODYHASH FROM revisions WHERE id <= $MAXREV"
q  "$REVROWS" | sort > "$WORK/rev-restored"
ql "$REVROWS" | sort > "$WORK/rev-live"
R_ONLY=$(comm -23 "$WORK/rev-restored" "$WORK/rev-live" | wc -l)
L_ONLY=$(comm -13 "$WORK/rev-restored" "$WORK/rev-live" | wc -l)
[ "$R_ONLY" -eq 0 ] || fail "$R_ONLY restored revision(s) are missing from live or differ from it"
REVS=$(wc -l < "$WORK/rev-restored")
echo "revisions: $REVS up to #$MAXREV, all identical in live (sha256 of the list $(sha256sum "$WORK/rev-restored" | cut -c1-16)…)"
[ "$L_ONLY" -eq 0 ] || echo "note: $L_ONLY live revision(s) at or below #$MAXREV are not in the dump (committed during it)"

q  "SELECT id || ' ' || sha256 FROM docs ORDER BY id" | sort > "$WORK/restored"
ql "SELECT id FROM docs WHERE id <= $MAXDOC ORDER BY id" | sort > "$WORK/live-ids"
# A document written or deleted after the dump has a revision the dump lacks
# (above its highest id, or one of the late commits noted above); a move or an
# undelete changes neither the id nor the body.
LATE=$(comm -13 "$WORK/rev-restored" "$WORK/rev-live" | cut -d' ' -f1 | paste -sd, -)
ql "SELECT id || ' ' || sha256 FROM docs WHERE id <= $MAXDOC AND id NOT IN (
      SELECT doc_id FROM revisions WHERE (id > $MAXREV OR id IN (${LATE:-0})) AND doc_id IS NOT NULL)
    ORDER BY id" | sort > "$WORK/live-stable"

MISSING=$(cut -d' ' -f1 "$WORK/restored" | sort | comm -23 - "$WORK/live-ids" | wc -l)
[ "$MISSING" -eq 0 ] || fail "$MISSING restored document id(s) do not exist in live"
DIFFER=$(comm -23 "$WORK/live-stable" "$WORK/restored" | wc -l)
[ "$DIFFER" -eq 0 ] || fail "$DIFFER document(s) unchanged since the dump have a different body in the restore"

R_DOCS=$(q  "SELECT count(*) FILTER (WHERE deleted_at IS NULL) || ' live, ' || count(*) || ' total' FROM docs")
L_DOCS=$(ql "SELECT count(*) FILTER (WHERE deleted_at IS NULL) || ' live, ' || count(*) || ' total' FROM docs")
STABLE=$(wc -l < "$WORK/live-stable")
echo "documents: restored $R_DOCS; live now $L_DOCS; $STABLE unchanged since the dump, all identical"
echo "rehearsal OK: $GEN"
