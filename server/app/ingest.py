"""Canonical writes — save, delete, re-derive.

The store IS the canonical copy, which adds one rule: **the previous body is
written to `revisions` before it is overwritten.** Whatever git used to do
(what changed, when, why, and how to go back) has nowhere else to happen.

Derived data (chunks, tsv, links) is rebuilt for THAT DOCUMENT ONLY, never
wholesale. Since the canonical copy lives here, dropping and rebuilding is not
re-indexing, it is deleting knowledge — and there is no reason for it either
way: a Postgres GIN index follows a changed row inside the same transaction.
"""
from __future__ import annotations

import os
import posixpath
import sys
import time
from pathlib import Path

import psycopg

sys.path.insert(0, str(Path(__file__).parent))
from core import (TEXT_SUFFIXES, PathRejected, lexemes,  # noqa: E402,F401
                  parse_text, validate_path)

SCHEMA = Path(__file__).parent.parent / "sql" / "schema.sql"

# The owner groups this store admits. **The confidentiality boundary is here,
# not in a column.**
#
# owner/repo columns serve relevance (what ranks first); they cannot protect
# confidentiality (what can be read), because reads are open — whatever the
# column says, it is readable. So what must not be read is never let IN. That is
# enforced by this list, not by anyone remembering.
#
# It is a list of GROUPS, not repos, on purpose: enumerate repos and the list
# falls behind, and one day something wrong slips in. "This group only" keeps
# being correct when a new repo appears under it.
#
# The default is empty, deliberately — a deployment that forgot to configure it
# closes rather than opens.
ALLOWED_OWNERS = {s.strip() for s in
                  os.environ.get("ENGRAM_OWNERS", "").split(",") if s.strip()}


class ScopeDenied(ValueError):
    """A write was attempted under an owner this store does not admit."""


def check_scope(owner: str, repo: str) -> None:
    if not ALLOWED_OWNERS:
        raise ScopeDenied("ENGRAM_OWNERS is empty — this store admits nothing and "
                          "every write is refused. Set the groups it should accept "
                          "in the server's .env (e.g. ENGRAM_OWNERS=acme).")
    if owner not in ALLOWED_OWNERS:
        raise ScopeDenied(
            f"owner {owner!r} is not admitted by this store (the first path segment). "
            f"Admitted: {', '.join(sorted(ALLOWED_OWNERS))}. "
            "Knowledge from other repos stays in a local file brain.")


def ensure_schema(conn: psycopg.Connection) -> None:
    """Idempotent. Running it on every boot never touches existing data."""
    with conn.cursor() as cur:
        cur.execute(SCHEMA.read_text(encoding="utf-8"))
    conn.commit()


def _path_words(path: str) -> str:
    return path.replace("/", " ").replace("-", " ").replace("_", " ")


def rederive(cur: psycopg.Cursor, doc_id: int, d: dict) -> int:
    """Rebuild one document's derived data. It IS derived, so deleting and
    recreating is the right move here — unlike the body, which has no other copy."""
    cur.execute("DELETE FROM chunks WHERE doc_id = %s", (doc_id,))
    cur.execute("DELETE FROM links  WHERE src    = %s", (doc_id,))

    rows = [(doc_id, c.ord, c.heading_path, c.body, len(c.body),
             lexemes(f"{d['title']} {c.heading_path}"), lexemes(c.body))
            for c in d["chunks"]]
    if rows:
        cur.executemany(
            "INSERT INTO chunks (doc_id,ord,heading_path,body,chars,tsv)"
            " VALUES (%s,%s,%s,%s,%s,"
            "  setweight(array_to_tsvector(%s),'A') || setweight(array_to_tsvector(%s),'B'))",
            rows)

    # Relative markdown links resolve against the document's own location, which
    # makes them exact — no stem guessing, unlike a wikilink.
    here = posixpath.dirname(d["path"])
    for rel in d.get("rel_links", []):
        tgt = posixpath.normpath(posixpath.join(here, rel))
        cur.execute("INSERT INTO links (src,dst_name,dst,kind) VALUES (%s,%s,"
                    " COALESCE("
                    "  (SELECT id FROM docs WHERE deleted_at IS NULL AND path = %s),"
                    "  (SELECT a.doc_id FROM aliases a JOIN docs t ON t.id = a.doc_id"
                    "    WHERE t.deleted_at IS NULL AND a.path = %s LIMIT 1)),'md')"
                    " ON CONFLICT DO NOTHING", (doc_id, tgt, tgt, tgt))

    # Wikilinks. One pointing at a document that does not exist yet is KEPT with
    # dst=NULL — dropping it would hide the broken link and destroy the record
    # that lets the edge reconnect if that document is later created.
    for name in d["links"]:
        key = name.split("/")[-1]
        # Stem collision order: **same repo -> shared -> anything else.**
        # Several repos in one store can each have a logging.md. Without a
        # defined order the link silently points into somebody else's repo.
        cur.execute(
            "INSERT INTO links (src,dst_name,dst,kind) VALUES (%s,%s,"
            " COALESCE("
            "  (SELECT id FROM docs WHERE deleted_at IS NULL"
            "     AND (path = %s OR path LIKE %s)"
            "   ORDER BY (repo = %s) DESC, (repo = 'shared') DESC, path LIMIT 1),"
            # No direct hit: try the old paths. This is where a link keeps
            # reaching a document that has since moved.
            "  (SELECT a.doc_id FROM aliases a JOIN docs t ON t.id = a.doc_id"
            "    WHERE t.deleted_at IS NULL AND (a.path = %s OR a.path LIKE %s) LIMIT 1)"
            " ),'wiki') ON CONFLICT DO NOTHING",
            (doc_id, name, name, f"%/{key}.md", d["repo"], name, f"%/{key}.md"))
    return len(rows)


def _relink_incoming(cur: psycopg.Cursor, doc_id: int, path: str) -> int:
    """Connect the DANGLING links that named this document. When a document is
    created, the ``[[name]]`` somebody else wrote first finally gets a target."""
    stem = Path(path).stem
    # Only dangling links. A new document must never steal an edge that already
    # resolved — a same-stem document appearing later is not a reason to move
    # somebody else's link.
    cur.execute("UPDATE links SET dst = %s WHERE dst IS NULL"
                " AND (dst_name = %s OR dst_name = %s OR dst_name LIKE %s)",
                (doc_id, path, stem, f"%/{stem}"))
    return cur.rowcount


def write_doc(conn: psycopg.Connection, path: str, body: str,
              author: str = "", note: str = "", updated_at: str | None = None) -> dict:
    """Save one document, creating it if absent. An unchanged body does nothing.

    updated_at: a bulk import passes the ORIGINAL modification time (the git
    commit date). Without it, two hundred imported documents all carry the
    moment of the import and "recently changed" means nothing.
    """
    validate_path(path)
    d = parse_text(path, body)
    check_scope(d["owner"], d["repo"])
    with conn.cursor() as cur:
        # Timestamps are not compared as Python strings. The database renders
        # '2026-08-28 13:53' while an incoming ISO value reads
        # '2026-08-28T09:12+09:00', and the separator alone (space vs T) flips
        # the comparison for the same day. timestamptz lets the database decide.
        cur.execute("SELECT id, body, sha256, deleted_at, updated_at,"
                    "  (updated_at > %s::timestamptz) AS store_newer,"
                    "  (date_trunc('second', updated_at)"
                    "     IS DISTINCT FROM date_trunc('second', %s::timestamptz)) AS ts_differs"
                    " FROM docs WHERE path = %s", (updated_at, updated_at, path))
        row = cur.fetchone()

        # **An older version must not overwrite a newer one.** A bulk import is
        # a one-off act but the tool goes on existing, and re-running it is how
        # store-only edits get replaced by the contents of stale files. When the
        # caller carries an updated_at (only imports do) and the store's copy is
        # newer with a different body, leave it alone and report.
        if (row and updated_at and row[3] is None and row[2] != d["sha256"]
                and row[5]):
            return {"path": path, "status": "skipped_newer", "doc_id": row[0],
                    "store_updated_at": str(row[4])[:19], "incoming": str(updated_at)[:19]}

        if row and row[2] == d["sha256"] and row[3] is None:
            # Same body, different timestamp: fix the metadata without inventing
            # a revision. Re-running an import to repair dates takes this path.
            if updated_at and row[6]:
                cur.execute("UPDATE docs SET updated_at = %s WHERE id = %s",
                            (updated_at, row[0]))
                conn.commit()
                return {"path": path, "status": "touched", "doc_id": row[0]}
            return {"path": path, "status": "unchanged", "doc_id": row[0]}

        if row:
            doc_id, prev_body, prev_sha = row[0], row[1], row[2]
            # Keep the previous body before overwriting. This is what stands in
            # for git log.
            cur.execute("INSERT INTO revisions (doc_id,path,body,sha256,author,note)"
                        " VALUES (%s,%s,%s,%s,%s,%s)",
                        (doc_id, path, prev_body, prev_sha, author, note))
            cur.execute("UPDATE docs SET title=%s, area=%s, owner=%s, repo=%s, body=%s,"
                        " sha256=%s, chars=%s, updated_at=COALESCE(%s, now()),"
                        " deleted_at=NULL, tsv=array_to_tsvector(%s) WHERE id=%s",
                        (d["title"], d["area"], d["owner"], d["repo"], body, d["sha256"],
                         d["chars"], updated_at,
                         lexemes(d["title"] + " " + _path_words(path)), doc_id))
            status = "updated"
        else:
            cur.execute("INSERT INTO docs"
                        " (path,title,area,owner,repo,body,sha256,chars,created_at,"
                        "  updated_at,tsv)"
                        " VALUES (%s,%s,%s,%s,%s,%s,%s,%s,COALESCE(%s,now()),"
                        "  COALESCE(%s,now()),array_to_tsvector(%s)) RETURNING id",
                        (path, d["title"], d["area"], d["owner"], d["repo"], body,
                         d["sha256"], d["chars"], updated_at, updated_at,
                         lexemes(d["title"] + " " + _path_words(path))))
            doc_id = cur.fetchone()[0]
            status = "created"

        n_chunks = rederive(cur, doc_id, d)
        relinked = _relink_incoming(cur, doc_id, path)
    conn.commit()
    return {"path": path, "status": status, "doc_id": doc_id,
            "chunks": n_chunks, "relinked": relinked}


def delete_doc(conn: psycopg.Connection, path: str,
               author: str = "", note: str = "") -> dict:
    """Soft delete. The body stays in revisions; only derived data is cleared."""
    with conn.cursor() as cur:
        cur.execute("SELECT id, body, sha256 FROM docs"
                    " WHERE path = %s AND deleted_at IS NULL", (path,))
        row = cur.fetchone()
        if not row:
            return {"path": path, "status": "absent"}
        doc_id, body, sha = row
        cur.execute("INSERT INTO revisions (doc_id,path,body,sha256,author,note)"
                    " VALUES (%s,%s,%s,%s,%s,%s)",
                    (doc_id, path, body, sha, author, note or "deleted"))
        cur.execute("UPDATE docs SET deleted_at = now() WHERE id = %s", (doc_id,))
        cur.execute("DELETE FROM chunks WHERE doc_id = %s", (doc_id,))
        # Outgoing edges go too. They are derived, so restore rebuilds them, and
        # leaving them would let a dead document contribute edges that pollute
        # the broken-link count.
        cur.execute("DELETE FROM links WHERE src = %s", (doc_id,))
        cur.execute("UPDATE links SET dst = NULL WHERE dst = %s", (doc_id,))
    conn.commit()
    return {"path": path, "status": "deleted", "doc_id": doc_id}


def restore_doc(conn: psycopg.Connection, path: str, author: str = "") -> dict:
    """Undo a soft delete. This function is the reason deletes are soft."""
    with conn.cursor() as cur:
        cur.execute("SELECT id, body FROM docs WHERE path = %s AND deleted_at IS NOT NULL",
                    (path,))
        row = cur.fetchone()
        if not row:
            return {"path": path, "status": "absent"}
        doc_id, body = row
        cur.execute("UPDATE docs SET deleted_at = NULL, updated_at = now() WHERE id = %s",
                    (doc_id,))
        rederive(cur, doc_id, parse_text(path, body))
        _relink_incoming(cur, doc_id, path)
    conn.commit()
    return {"path": path, "status": "restored", "doc_id": doc_id}


def import_docs(conn: psycopg.Connection, raw_docs: list[dict], author: str = "import",
                note: str = "") -> dict:
    """Bulk import — seed the store from a tree of markdown files.

    **Upsert only. It never deletes.** A drop-and-rebuild was safe when the
    canonical copy lived in files; doing it now would destroy every document
    born here and its whole history. Documents absent from the payload are left
    exactly as they are.
    """
    t0 = time.time()
    ensure_schema(conn)
    counts = {"created": 0, "updated": 0, "unchanged": 0, "touched": 0,
              "skipped_newer": 0, "denied": 0}
    denied: list[str] = []
    skipped: list[str] = []
    for raw in raw_docs:
        try:
            r = write_doc(conn, raw["path"], raw["body"], author=author,
                          note=note or "bulk import",
                          updated_at=raw.get("updated_at"))
        except ScopeDenied:
            # One refused document does not stop the import. But **what was
            # refused is always reported** — dropped quietly, it would be assumed
            # to have landed.
            counts["denied"] += 1
            denied.append(raw["path"])
            continue
        counts[r["status"]] = counts.get(r["status"], 0) + 1
        if r["status"] == "skipped_newer":
            skipped.append(r["path"])

    with conn.cursor() as cur:
        cur.execute("SELECT count(*) FROM docs WHERE deleted_at IS NULL")
        n_docs = cur.fetchone()[0]
        cur.execute("SELECT count(*) FROM chunks")
        n_chunks = cur.fetchone()[0]
        cur.execute("SELECT count(*) FROM links WHERE dst IS NULL")
        broken = cur.fetchone()[0]
        cur.executemany(
            "INSERT INTO meta (k,v) VALUES (%s,%s)"
            " ON CONFLICT (k) DO UPDATE SET v = EXCLUDED.v",
            [("imported_at", time.strftime("%Y-%m-%dT%H:%M:%S%z")),
             ("import_note", note)])
    conn.commit()
    return dict(**counts, docs=n_docs, chunks=n_chunks, broken_links=broken,
                denied_paths=denied[:20], skipped_paths=skipped[:20],
                seconds=round(time.time() - t0, 2))


def rederive_all(conn: psycopg.Connection) -> dict:
    """Rebuild the derived data (chunks, lexemes, edges) of every live document
    from its body.

    **Bodies and history are untouched.** This is the path to take after
    changing the chunking or link rules — "full re-index" became a dangerous
    phrase once the canonical copy moved into the database, but rebuilding only
    what is derived is still safe and still necessary.
    """
    t0 = time.time()
    with conn.cursor() as cur:
        cur.execute("SELECT id, path, body FROM docs WHERE deleted_at IS NULL ORDER BY id")
        rows = cur.fetchall()
        cur.execute("DELETE FROM links")
        for doc_id, path, body in rows:
            rederive(cur, doc_id, parse_text(path, body))
        # Link resolution depends on document order, so make one more pass to
        # connect what could not be connected yet.
        cur.execute("""
            UPDATE links l SET dst = d.id FROM docs d
            WHERE l.dst IS NULL AND d.deleted_at IS NULL
              AND (d.path = l.dst_name
                   OR split_part(d.path,'/',array_length(string_to_array(d.path,'/'),1))
                      = l.dst_name || '.md')""")
        cur.execute("SELECT count(*), count(*) FILTER (WHERE dst IS NULL) FROM links")
        total, broken = cur.fetchone()
    conn.commit()
    return {"docs": len(rows), "links": total, "broken_links": broken,
            "seconds": round(time.time() - t0, 2)}


def move_doc(conn: psycopg.Connection, path: str, new_path: str,
             author: str = "", note: str = "") -> dict:
    """Move a document — **without breaking links.**

    The old path is kept in `aliases`, so the ``[[old name]]`` somebody already
    wrote, and relative markdown links, keep reaching it. In a file vault the
    only options were editing every referring document or leaving the links
    broken.

    The body does not change, so no revision is created. The fact of the move is
    recorded by the alias.
    """
    validate_path(new_path)
    d_new = parse_text(new_path, "")
    check_scope(d_new["owner"], d_new["repo"])
    with conn.cursor() as cur:
        cur.execute("SELECT id, body FROM docs WHERE path = %s AND deleted_at IS NULL",
                    (path,))
        row = cur.fetchone()
        if not row:
            return {"path": path, "status": "absent"}
        cur.execute("SELECT 1 FROM docs WHERE path = %s", (new_path,))
        if cur.fetchone():
            return {"path": path, "status": "target_exists", "to": new_path}
        doc_id, body = row

        d = parse_text(new_path, body)
        cur.execute("UPDATE docs SET path=%s, title=%s, area=%s, owner=%s, repo=%s,"
                    " tsv=array_to_tsvector(%s) WHERE id=%s",
                    (new_path, d["title"], d["area"], d["owner"], d["repo"],
                     lexemes(d["title"] + " " + _path_words(new_path)), doc_id))
        cur.execute("INSERT INTO aliases (path, doc_id) VALUES (%s,%s)"
                    " ON CONFLICT (path) DO UPDATE SET doc_id = EXCLUDED.doc_id",
                    (path, doc_id))
        rederive(cur, doc_id, d)
        # Connect links that named the old name and were dangling.
        stem = Path(path).stem
        cur.execute("UPDATE links SET dst = %s WHERE dst IS NULL"
                    " AND (dst_name = %s OR dst_name = %s)", (doc_id, path, stem))
        relinked = cur.rowcount
    conn.commit()
    return {"path": path, "to": new_path, "status": "moved", "doc_id": doc_id,
            "relinked": relinked}
