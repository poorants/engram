"""Partial writes: addressing, verification, and every way they refuse.

The tests that matter most here are the refusals. A patch that works is easy;
what this module has to guarantee is that a patch which is even slightly wrong
about where it is aimed writes NOTHING, rather than writing somewhere else.
"""
import pytest

from core import headings
from patch import (PatchConflict, PatchRejected, apply_edits, check_base,
                   line_spans, sha256)

DOC = """# Guide

Intro line.

## First

alpha
beta

## Second

gamma

### Second child

delta

## Third

omega
"""


def body_of(edits, doc=DOC):
    return apply_edits(doc, edits).body


# -- line addressing ---------------------------------------------------------

def test_line_range_replaces_exactly_those_lines():
    out = body_of([{"start_line": 7, "end_line": 8, "expect": "alpha", "body": "ALPHA"}])
    assert "ALPHA\nbeta" in out
    assert "alpha" not in out
    # nothing else moved
    assert out.count("\n") == DOC.count("\n")


def test_end_line_is_exclusive_so_equal_bounds_insert():
    out = body_of([{"start_line": 7, "end_line": 7, "body": "zero\n",
                    "expect": ""}])
    assert "zero\nalpha" in out


def test_empty_body_deletes_the_range():
    out = body_of([{"start_line": 7, "end_line": 9, "expect": "alpha\nbeta\n", "body": ""}])
    assert "alpha" not in out and "beta" not in out
    assert "## First" in out


def test_line_range_without_expect_is_refused():
    # A line number is pure position: it carries no evidence about what is
    # there, so the verification layer is not optional for it.
    with pytest.raises(PatchRejected, match="expect"):
        apply_edits(DOC, [{"start_line": 7, "end_line": 8, "body": "x"}])


def test_out_of_range_lines_are_refused():
    with pytest.raises(PatchRejected, match="outside the document"):
        apply_edits(DOC, [{"start_line": 999, "end_line": 1000, "expect": "x", "body": "y"}])


# -- verification ------------------------------------------------------------

def test_expect_mismatch_writes_nothing_and_quotes_reality():
    with pytest.raises(PatchConflict) as e:
        apply_edits(DOC, [{"start_line": 7, "end_line": 8,
                           "expect": "not what is there", "body": "x"}])
    assert e.value.kind == "expect_mismatch"
    assert "actually there" in str(e.value)
    assert "alpha" in str(e.value)


def test_expect_may_omit_trailing_newlines_and_the_range_shrinks_to_match():
    # The caller quoted the section's text but not the blank line after it. The
    # blank line must survive — otherwise a patch silently welds two sections
    # together.
    out = body_of([{"section": "First", "expect": "## First\n\nalpha\nbeta",
                    "body": "## First\n\nALPHA"}])
    assert "ALPHA\n\n## Second" in out


def test_whitespace_difference_is_not_tolerated():
    with pytest.raises(PatchConflict):
        apply_edits(DOC, [{"start_line": 7, "end_line": 8, "expect": "  alpha", "body": "x"}])


# -- section addressing ------------------------------------------------------

def test_section_covers_the_heading_and_its_body():
    out = body_of([{"section": "## First", "body": "## First\n\nnew\n"}])
    assert "alpha" not in out and "beta" not in out
    assert "## Second" in out and "# Guide" in out


def test_section_stops_at_the_next_same_or_shallower_heading():
    # "Second" owns its ### child; it must not swallow "## Third".
    out = body_of([{"section": "Second", "body": "## Second\n\nreplaced\n"}])
    assert "Second child" not in out and "delta" not in out
    assert "## Third" in out and "omega" in out


def test_section_body_only_keeps_the_heading():
    out = body_of([{"section": "First", "include_heading": False, "body": "\nonly\n"}])
    assert "## First\n\nonly" in out


def test_section_matches_by_heading_path():
    out = body_of([{"section": "Guide > Second > Second child", "body": "### X\n\nx\n"}])
    assert "delta" not in out and "### X" in out


def test_ambiguous_section_is_refused_with_candidates():
    doc = "# T\n\n## Notes\n\na\n\n## Other\n\nb\n\n## Notes\n\nc\n"
    with pytest.raises(PatchConflict) as e:
        apply_edits(doc, [{"section": "Notes", "body": "x"}])
    assert e.value.kind == "section_ambiguous"
    assert "line 3" in str(e.value) and "line 11" in str(e.value)


def test_missing_section_lists_what_is_available():
    with pytest.raises(PatchConflict) as e:
        apply_edits(DOC, [{"section": "Nope", "body": "x"}])
    assert e.value.kind == "section_not_found"
    assert "## First" in str(e.value)


def test_a_heading_inside_a_code_fence_is_not_a_heading():
    doc = "# T\n\n```\n## First\n```\n\n## First\n\nreal\n"
    # One real heading, so this resolves instead of being ambiguous.
    out = apply_edits(doc, [{"section": "First", "body": "## First\n\nnew\n"}]).body
    assert "```\n## First\n```" in out
    assert "real" not in out
    assert [h.text for h in headings(doc)] == ["T", "First"]


# -- anchor addressing -------------------------------------------------------

def test_unique_anchor_replaces_in_place():
    out = body_of([{"anchor": "gamma", "body": "GAMMA"}])
    assert "GAMMA" in out and "gamma" not in out


def test_anchor_occurring_twice_is_refused():
    doc = "# T\n\nsame here\n\nand same here\n"
    with pytest.raises(PatchConflict) as e:
        apply_edits(doc, [{"anchor": "same here", "body": "x"}])
    assert e.value.kind == "anchor_ambiguous"
    assert "occurs 2 times" in str(e.value)


def test_anchor_is_literal_not_a_pattern():
    with pytest.raises(PatchConflict) as e:
        apply_edits(DOC, [{"anchor": "al.ha", "body": "x"}])
    assert e.value.kind == "anchor_not_found"


def test_anchor_edits_inside_a_line():
    out = body_of([{"anchor": "Intro line.", "body": "Intro sentence."}])
    assert "Intro sentence." in out


# -- batches -----------------------------------------------------------------

def test_edits_resolve_against_the_original_body():
    # Both edits are written in terms of the document as READ. If the first one
    # were applied before the second resolved, the second's line numbers would
    # have shifted underneath it.
    out = body_of([
        {"start_line": 7, "end_line": 8, "expect": "alpha", "body": "A\nA\nA"},
        {"start_line": 12, "end_line": 13, "expect": "gamma", "body": "G"},
    ])
    assert "A\nA\nA\nbeta" in out and "\nG\n" in out


def test_overlapping_edits_are_refused():
    with pytest.raises(PatchRejected, match="overlap"):
        apply_edits(DOC, [
            {"start_line": 7, "end_line": 9, "expect": "alpha\nbeta\n", "body": "x"},
            {"start_line": 8, "end_line": 9, "expect": "beta", "body": "y"},
        ])


def test_two_edits_at_the_same_point_are_refused():
    with pytest.raises(PatchRejected, match="same point"):
        apply_edits(DOC, [
            {"start_line": 7, "end_line": 7, "expect": "", "body": "x\n"},
            {"start_line": 7, "end_line": 7, "expect": "", "body": "y\n"},
        ])


def test_one_address_per_edit():
    with pytest.raises(PatchRejected, match="ONE address"):
        apply_edits(DOC, [{"section": "First", "anchor": "alpha", "body": "x"}])


def test_no_edits_is_refused():
    with pytest.raises(PatchRejected):
        apply_edits(DOC, [])


def test_emptying_the_document_is_refused():
    with pytest.raises(PatchRejected, match="empty document"):
        apply_edits("# T\n\nonly this\n", [{"start_line": 1, "end_line": 4,
                                            "expect": "# T\n\nonly this\n", "body": ""}])


# -- concurrency -------------------------------------------------------------

def test_base_hash_matching_passes_and_stale_fails():
    check_base(DOC, sha256(DOC))
    check_base(DOC, "sha256:" + sha256(DOC))
    check_base(DOC, None)                      # optional
    with pytest.raises(PatchConflict) as e:
        check_base(DOC, "0" * 64)
    assert e.value.kind == "stale_base"


# -- newline handling --------------------------------------------------------

def test_crlf_input_is_normalized_on_both_sides():
    doc = "# T\r\n\r\nalpha\r\n"
    out = apply_edits(doc, [{"anchor": "alpha", "body": "beta"}]).body
    assert "\r" not in out and "beta" in out


def test_line_spans_do_not_invent_a_final_empty_line():
    assert len(line_spans("a\nb\n")) == 2
    assert len(line_spans("a\nb")) == 2


def test_report_names_the_lines_it_touched():
    applied = apply_edits(DOC, [{"section": "## First", "body": "## First\n\nnew\n"}])
    (one,) = applied.edits
    assert one["start_line"] == 5 and one["end_line"] == 10
    assert "section" in one["how"]
