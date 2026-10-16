"""A write reports the links in its own body that resolve to nothing.

Needs ENGRAM_TEST_DSN pointing at a database THIS TEST MAY WIPE (see
test_feedback_db.py for a one-line Postgres). Skipped when unset.
"""
import os
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))
os.environ.setdefault("ENGRAM_OWNERS", "acme")

DSN = os.environ.get("ENGRAM_TEST_DSN", "")
pytestmark = pytest.mark.skipif(not DSN, reason="ENGRAM_TEST_DSN is not set")


@pytest.fixture()
def conn():
    import psycopg
    from ingest import ensure_schema, write_doc
    c = psycopg.connect(DSN)
    ensure_schema(c)
    with c.cursor() as cur:
        cur.execute("TRUNCATE feedback, searches, links, aliases, chunks, revisions, docs RESTART IDENTITY CASCADE")
    c.commit()
    write_doc(c, "acme/shared/resources/target.md", "# Target\n\nExists.\n", author="t", note="seed")
    yield c
    c.close()


def test_a_write_names_its_broken_links(conn):
    from ingest import write_doc
    out = write_doc(conn, "acme/shared/resources/src.md",
                    "# Src\n\nSee [[target]] and [[no-such-doc]].\n", author="t", note="n")
    assert out["broken_links"] == [{"to": "no-such-doc", "kind": "wiki"}]
    assert "Fix them now" in out["warning"]


def test_a_clean_write_carries_no_warning(conn):
    from ingest import write_doc
    out = write_doc(conn, "acme/shared/resources/src.md",
                    "# Src\n\nSee [[target]].\n", author="t", note="n")
    assert "broken_links" not in out and "warning" not in out
