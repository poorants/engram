"""Partial writes — address a range, prove what is there, replace it.

A whole-document upsert makes the cost of an edit proportional to the DOCUMENT
rather than to the change. Fixing one link in a 9,000-character guide meant
sending 9,000 characters back, and the most common edit in this brain is
exactly that shape: one line, in each of several documents.

The danger of partial writes is the opposite of the danger of upserts. An
upsert cannot land in the wrong place — it replaces everything by definition. A
patch can, and the classic way it happens is a string match that hits somewhere
the caller did not look. So the safety model here is three INDEPENDENT layers,
and keeping them independent is the whole design:

  1. **Addressing — where.** Three forms, all resolving to one ``[start, end)``
     character range: an explicit line range, a heading (section), or an exact
     anchor string. An address that matches more than one place is never
     resolved by taking the first match; it is a conflict that names every
     candidate. This mirrors what an editor does with a "replace in file" that
     refuses to guess.
  2. **Verification — what you expected to find.** ``expect`` is the literal
     current text of the addressed range, compared character for character.
     This is the layer that catches a wrong address, and it is what makes
     matching safe at all: **matching CHOOSES a range, the comparison PROVES
     it.** It is literal text rather than a hash so that a caller who cannot run
     a hash function (a model writing a tool call) can still supply it, and its
     size is proportional to the edit, not to the document.
  3. **Concurrency — which version you read.** ``base_sha256`` is the document
     hash the caller last saw. If the stored document has moved on, nothing is
     applied. Layer 2 catches a misaimed edit; only this layer catches an edit
     aimed correctly at a document that somebody else has since changed.

Deliberately absent: **fuzz.** ``patch(1)`` applies a hunk at an offset when the
surrounding context nearly matches, and "nearly" is precisely how an edit lands
in the neighbouring section. Every mismatch here is a refusal that reports what
is actually there, so the caller re-aims instead of guessing.

Nothing in this module touches the database. It turns (body, edits) into a new
body, which the existing canonical write path then stores — so revisions,
aliases, scope checks and re-indexing all keep working exactly as before.
"""
from __future__ import annotations

import difflib
import hashlib
from dataclasses import dataclass, field

from core import headings


class PatchRejected(ValueError):
    """The request is malformed — a 400. Bad line numbers, overlapping edits,
    a missing ``expect`` where one is mandatory."""


class PatchConflict(ValueError):
    """The request is well formed but the document does not agree with it — a
    409. An ambiguous address, an ``expect`` that does not match, a stale base.

    Distinct from PatchRejected because the remedies differ: a rejection is
    fixed by correcting the call, a conflict by re-reading the document.
    """

    def __init__(self, message: str, kind: str = "conflict"):
        super().__init__(message)
        self.kind = kind


# How much of the document's actual text a conflict quotes back. Enough to
# re-aim from, short enough not to turn every mistake into a full document
# transfer — which is the cost this whole module exists to avoid.
QUOTE_LIMIT = 800


def _quote(text: str) -> str:
    if len(text) <= QUOTE_LIMIT:
        return text
    return text[:QUOTE_LIMIT] + f"… (+{len(text) - QUOTE_LIMIT} more chars)"


def normalize(text: str) -> str:
    """One newline convention inside the store. A document that arrives with
    CRLF and is patched with LF would otherwise never match its own ``expect``,
    and the failure would look like the caller quoting the wrong text."""
    return text.replace("\r\n", "\n").replace("\r", "\n")


@dataclass
class Resolved:
    """One edit, addressed and verified, as character offsets into the body."""
    start: int
    end: int
    replacement: str
    how: str                       # human-readable address, echoed in the result
    index: int                     # position in the caller's list, for messages
    start_line: int = 0            # 1-indexed, filled in by apply_edits
    end_line: int = 0              # 1-indexed, exclusive


@dataclass
class Applied:
    body: str
    edits: list[dict] = field(default_factory=list)


def line_spans(body: str) -> list[tuple[int, int]]:
    """``[(start, end)]`` character offsets per line, the end INCLUDING the
    line's newline. Line n (1-indexed) is ``spans[n - 1]``.

    A trailing newline does not create a phantom final line: "a\\nb\\n" is two
    lines, and an edit addressing line 3 of it is out of range rather than
    silently appending.
    """
    spans: list[tuple[int, int]] = []
    start = 0
    for i, ch in enumerate(body):
        if ch == "\n":
            spans.append((start, i + 1))
            start = i + 1
    if start < len(body):
        spans.append((start, len(body)))
    return spans


def offset_to_line(spans: list[tuple[int, int]], offset: int) -> int:
    """1-indexed line holding this offset (or the line after the last one for an
    offset at end of document)."""
    for n, (s, e) in enumerate(spans, start=1):
        if s <= offset < e:
            return n
    return len(spans) + 1


def _line_range(body: str, spans: list[tuple[int, int]], start_line: int,
                end_line: int) -> tuple[int, int]:
    n = len(spans)
    if start_line < 1 or start_line > n + 1:
        raise PatchRejected(
            f"start_line {start_line} is outside the document (1..{n + 1}; "
            f"{n + 1} means 'after the last line')")
    if end_line < start_line or end_line > n + 1:
        raise PatchRejected(
            f"end_line {end_line} is outside the document or before start_line "
            f"(allowed {start_line}..{n + 1}; end_line is EXCLUSIVE, so a single "
            f"line {start_line} is end_line {start_line + 1}, and "
            f"end_line == start_line inserts without replacing)")
    start = spans[start_line - 1][0] if start_line <= n else len(body)
    end = spans[end_line - 2][1] if end_line - 1 >= 1 and end_line - 1 <= n else start
    if end_line == start_line:
        end = start
    return start, end


def _section_range(body: str, spans: list[tuple[int, int]], query: str,
                   include_heading: bool) -> tuple[int, int, str]:
    """A heading and everything under it, up to the next heading of the same or
    shallower depth.

    The query matches a heading three ways — its text ("옵트아웃 옵션"), the raw
    heading line ("## 옵트아웃 옵션"), or its full heading path ("A > B >
    옵트아웃 옵션"), which is the same string ``brain_search`` prints for every
    hit. **More than one match is a conflict, never a choice.**
    """
    wanted = query.strip()
    bare = wanted.lstrip("#").strip()
    hs = headings(body)
    matches = [h for h in hs
               if h.text == bare or h.path == wanted or h.path == bare
               or f"{'#' * h.depth} {h.text}" == wanted]
    if not matches:
        available = "\n".join(f"  {'#' * h.depth} {h.text}   (line {h.line + 1})"
                              for h in hs) or "  (the document has no headings)"
        raise PatchConflict(
            f"no heading matches {query!r}. The document's headings are:\n{available}",
            kind="section_not_found")
    if len(matches) > 1:
        where = "\n".join(f"  line {h.line + 1}: {h.path}" for h in matches)
        raise PatchConflict(
            f"{query!r} matches {len(matches)} headings — refusing to pick one. "
            f"Address it by its full heading path, or by line range:\n{where}",
            kind="section_ambiguous")

    h = matches[0]
    following = [x for x in hs if x.line > h.line and x.depth <= h.depth]
    end_line_idx = following[0].line if following else len(spans)
    start_line_idx = h.line if include_heading else h.line + 1
    if start_line_idx > end_line_idx:            # an empty section, heading kept
        start_line_idx = end_line_idx
    start = spans[start_line_idx][0] if start_line_idx < len(spans) else len(body)
    end = spans[end_line_idx - 1][1] if end_line_idx >= 1 else start
    if end_line_idx <= start_line_idx:
        end = start
    how = f"section {h.path!r}" + ("" if include_heading else " (body only)")
    return start, end, how


def _anchor_range(body: str, spans: list[tuple[int, int]],
                  anchor: str) -> tuple[int, int, str]:
    """An exact substring that occurs EXACTLY ONCE.

    Uniqueness is the whole contract. Two occurrences is the case where a naive
    find-and-replace quietly edits the wrong one, so it is reported with the
    line of each, and the caller narrows the anchor or switches to a line range.
    """
    if not anchor:
        raise PatchRejected("anchor is empty")
    first = body.find(anchor)
    if first < 0:
        raise PatchConflict(
            f"anchor not found (it is matched literally, not as a pattern): {_quote(anchor)!r}",
            kind="anchor_not_found")
    hits, at = [], first
    while at >= 0:
        hits.append(at)
        at = body.find(anchor, at + 1)
    if len(hits) > 1:
        lines = ", ".join(str(offset_to_line(spans, h)) for h in hits)
        raise PatchConflict(
            f"anchor occurs {len(hits)} times (lines {lines}) — refusing to pick one. "
            "Extend the anchor until it is unique, or address the edit by line range.",
            kind="anchor_ambiguous")
    return first, first + len(anchor), f"anchor at line {offset_to_line(spans, first)}"


def _verify(body: str, start: int, end: int, expect: str | None, how: str,
            index: int, positional: bool) -> tuple[int, int]:
    """Layer 2. Returns the range possibly SHRUNK to exactly what was quoted.

    Trailing newlines are the one tolerated difference, and tolerating them does
    not weaken anything: when ``expect`` omits the newlines the addressed range
    carries, the range is narrowed to what the caller actually quoted, so the
    replacement affects exactly the text they saw and the blank line that
    separates two sections survives. Every other difference — leading
    whitespace, a changed character, a missing line — is a conflict.
    """
    actual = body[start:end]
    if expect is None:
        if positional:
            raise PatchRejected(
                f"edit {index}: a line range carries no evidence of what is there, "
                "so `expect` (the literal current text of those lines) is required. "
                "Section and anchor addressing may omit it.")
        return start, end
    expect = normalize(expect)
    if actual == expect:
        return start, end
    core = expect.rstrip("\n")
    if actual.rstrip("\n") == core:
        return start, start + len(core)
    raise PatchConflict(
        f"edit {index} ({how}): the document does not hold the text you expected — "
        f"nothing was written.\n"
        f"--- expected ---\n{_quote(expect)}\n"
        f"--- actually there ---\n{_quote(actual)}",
        kind="expect_mismatch")


def resolve(body: str, spans: list[tuple[int, int]], edit: dict,
            index: int) -> Resolved:
    forms = [k for k in ("start_line", "section", "anchor") if edit.get(k) is not None]
    if not forms:
        raise PatchRejected(
            f"edit {index}: no address — give one of `start_line`+`end_line`, "
            "`section`, or `anchor`")
    if len(forms) > 1:
        raise PatchRejected(
            f"edit {index}: {' and '.join(forms)} were both given; an edit has ONE address")
    if "body" not in edit or edit["body"] is None:
        raise PatchRejected(
            f"edit {index}: `body` is required (\"\" deletes the addressed range)")
    replacement = normalize(str(edit["body"]))
    expect = edit.get("expect")
    positional = False

    if edit.get("start_line") is not None:
        try:
            start_line = int(edit["start_line"])
            end_line = int(edit["end_line"]) if edit.get("end_line") is not None else -1
        except (TypeError, ValueError):
            raise PatchRejected(f"edit {index}: start_line/end_line must be integers")
        if end_line < 0:
            raise PatchRejected(
                f"edit {index}: end_line is required with start_line and is EXCLUSIVE — "
                f"one line {start_line} is end_line {start_line + 1}, and "
                f"end_line == start_line inserts before that line")
        start, end = _line_range(body, spans, start_line, end_line)
        how = (f"lines {start_line}..{end_line - 1}" if end_line > start_line
               else f"insert before line {start_line}")
        positional = True
    elif edit.get("section") is not None:
        include = edit.get("include_heading", True)
        start, end, how = _section_range(body, spans, str(edit["section"]), bool(include))
    else:
        start, end, how = _anchor_range(body, spans, normalize(str(edit["anchor"])))

    start, end = _verify(body, start, end, expect, how, index, positional)
    return Resolved(start=start, end=end, replacement=replacement, how=how, index=index)


def apply_edits(body: str, edits: list[dict]) -> Applied:
    """Resolve every edit against the ORIGINAL body, then splice.

    Resolving against the original — never against the partly-edited text — is
    what makes a batch deterministic and its overlap check meaningful. It is the
    same rule LSP puts on a TextEdit array, and for the same reason: edits
    resolved one after another silently shift each other's coordinates.
    """
    if not isinstance(edits, list) or not edits:
        raise PatchRejected("edits is empty — a patch with no edits changes nothing")
    if len(edits) > 50:
        raise PatchRejected(f"{len(edits)} edits in one call (ceiling 50)")

    body = normalize(body)
    spans = line_spans(body)
    resolved = [resolve(body, spans, e, i + 1) for i, e in enumerate(edits)]

    ordered = sorted(resolved, key=lambda r: (r.start, r.end))
    for a, b in zip(ordered, ordered[1:]):
        if b.start < a.end:
            raise PatchRejected(
                f"edits {a.index} ({a.how}) and {b.index} ({b.how}) overlap — "
                "nothing was written. Split them into separate calls or widen one "
                "to cover both changes.")
        if b.start == a.start and b.end == a.end:
            raise PatchRejected(
                f"edits {a.index} and {b.index} address the same point ({a.how}); "
                "their order would be undefined. Combine them into one edit.")

    for r in resolved:
        r.start_line = offset_to_line(spans, r.start)
        r.end_line = offset_to_line(spans, max(r.end - 1, r.start)) + (1 if r.end > r.start else 0)

    out = body
    for r in sorted(resolved, key=lambda r: r.start, reverse=True):
        out = out[:r.start] + r.replacement + out[r.end:]

    if not out.strip():
        raise PatchRejected(
            "the result would be an empty document — an empty body never overwrites "
            "a document. To retire it, move it to archives instead.")

    report = [{
        "edit": r.index,
        "how": r.how,
        "start_line": r.start_line,
        "end_line": r.end_line,
        "chars_removed": r.end - r.start,
        "chars_added": len(r.replacement),
    } for r in sorted(resolved, key=lambda r: r.start)]
    return Applied(body=out, edits=report)


def sha256(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


def check_base(body: str, base: str | None) -> None:
    """Layer 3. ``base_sha256`` is the hash ``brain_get`` handed the caller.

    Optional, because a caller that addresses by section and quotes ``expect``
    is already protected against editing text it never saw. What only this
    check catches is the edit that is correct about its own range and wrong
    about the document: someone else rewrote the rest of it in between.
    """
    if not base:
        return
    want = base.split(":", 1)[-1].strip().lower()
    have = sha256(body)
    if want != have:
        raise PatchConflict(
            f"the document has changed since you read it (base_sha256 {want[:12]}…, "
            f"now {have[:12]}…) — nothing was written. Re-read it and re-aim the edit.",
            kind="stale_base")


def diff(before: str, after: str, path: str) -> str:
    return "".join(difflib.unified_diff(
        before.splitlines(keepends=True), after.splitlines(keepends=True),
        fromfile=f"a/{path}", tofile=f"b/{path}", n=2))
