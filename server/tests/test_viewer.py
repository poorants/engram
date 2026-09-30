"""Viewer rendering that needs no database: markdown, anchors, the tree."""
import importlib
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))


@pytest.fixture
def web(monkeypatch):
    monkeypatch.setenv("ENGRAM_TOKEN", "test-token")
    monkeypatch.setenv("ENGRAM_DSN", "postgresql://engram:x@127.0.0.1:1/engram")
    sys.modules.pop("web", None)
    return importlib.import_module("web")


def test_a_wikilink_inside_code_stays_literal(web):
    out = web.render_markdown("`[[a]]`\n\n```\n[[b]]\n```\n", {"a": "x/y/resources/a.md"})
    assert "/doc/" not in out
    assert "[[a]]" in out and "[[b]]" in out


def test_a_broken_wikilink_is_marked_not_escaped(web):
    out = web.render_markdown("see [[nowhere]]", {})
    assert "<span class='broken'" in out
    assert "&lt;span" not in out


def test_a_broken_link_name_is_still_escaped(web):
    out = web.render_markdown("[[<script>]]", {})
    assert "<script>" not in out


def test_task_lists_render_as_checkboxes(web):
    out = web.render_markdown("- [x] done\n- [ ] todo\n", {})
    assert out.count("type='checkbox'") == 2
    assert "checked" in out


def test_heading_anchors_are_unique_and_keep_hangul(web):
    html_, toc = web.with_toc(web.render_markdown("## 등록 절차\n\n## 등록 절차\n", {}))
    assert [h["id"] for h in toc] == ["등록-절차", "등록-절차-1"]
    assert 'id="등록-절차-1"' in html_


def test_the_tree_nests_by_folder_under_the_repo(web):
    rows = [{"path": "o/r/resources/a.md", "title": "A"},
            {"path": "o/r/resources/sub/b.md", "title": "B"},
            {"path": "o/r/projects/c.md", "title": "C"},
            {"path": "o/r/README.md", "title": "Hub"}]
    t = web.build_tree(rows, "o/r/")
    assert t["count"] == 4
    assert [d["name"] for d in t["dirs"]] == ["projects", "resources"]   # PARA order
    res = t["dirs"][1]
    assert res["count"] == 2 and res["dirs"][0]["name"] == "sub"
    assert [f["fname"] for f in t["files"]] == ["README.md"]
