"""Markdown -> chunks, lexemes, links.

The indexer and the search path build lexemes with the SAME function. If they
ever diverge, queries stop reaching the index and nothing announces it.
"""
from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass

TARGET_CHARS = 700      # the size a chunk aims for
MAX_CHARS = 1400        # a hard cut, whatever the structure says

_HANGUL_RUN = re.compile(r"[가-힣]+")
_ASCII_TOK = re.compile(r"[A-Za-z][A-Za-z0-9_.\-]*|-?\d[\d.\-]*")
_SPLIT_ID = re.compile(r"[_.\-]+")
_WIKILINK = re.compile(r"\[\[([^\]|#]+)")
# A relative markdown link is a link too. MOC files (README.md) have used that
# form from the beginning, so counting only wikilinks makes every document in a
# folder look like an orphan — the graph has to follow the convention, not the
# other way round. External URLs and bare anchors are excluded; what remains is
# a relative path to another document in the store.
_MDLINK = re.compile(r"\]\((?!https?://|mailto:|#)([^)\s]+\.(?:md|dbml))(?:#[^)]*)?\)")
_FENCE = re.compile(r"^```")
# Code is stripped before links are counted — fenced blocks and inline spans.
# Without this, a `[[wikilink]]` shown as syntax in documentation, a bash
# `[[ -n "$x" ]]`, and a regex `[[0-9]+` all register as broken links.
_CODE_FENCE_BLOCK = re.compile(r"^```.*?^```[ \t]*$", re.M | re.S)
_INLINE_CODE = re.compile(r"`[^`\n]*`")
_HEADING = re.compile(r"^(#{1,6})\s+(.*)$")


def lexemes(text: str) -> list[str]:
    """The lexemes that go into a tsvector. Postgres's own parser is not used,
    for two reasons.

    - The default parser splits identifiers like ``http_client_pool`` apart.
      Here they are kept whole AND their parts are added, so a query from either
      direction reaches the document.
    - No morphological extension for CJK is in the image. Generating syllable
      2-grams in the indexer keeps the whole thing on stock Postgres, which is
      what makes the deployment a single compose file.
    """
    out: list[str] = []
    for m in _ASCII_TOK.finditer(text):
        tok = m.group(0).lower().strip(".-")
        if not tok or len(tok) > 60:
            continue
        out.append(tok)
        if _SPLIT_ID.search(tok):
            out.extend(p for p in _SPLIT_ID.split(tok) if len(p) > 1)
    for m in _HANGUL_RUN.finditer(text):
        run = m.group(0)
        if len(run) == 1:
            out.append(run)
        else:
            out.extend(run[i:i + 2] for i in range(len(run) - 1))
    # Deduplicate, preserving order. array_to_tsvector sorts and dedupes anyway;
    # this keeps the payload small.
    seen, uniq = set(), []
    for t in out:
        if t not in seen:
            seen.add(t)
            uniq.append(t)
    return uniq


@dataclass
class Chunk:
    ord: int
    heading_path: str
    body: str


def chunk(md: str) -> list[Chunk]:
    """Prefer heading boundaries, then cut by size within them. A code fence is
    never split — a fragment of a fence is not runnable and not quotable."""
    lines = md.splitlines()
    stack: list[str] = []
    chunks: list[Chunk] = []
    buf: list[str] = []
    buf_head = ""
    in_fence = False

    def flush() -> None:
        nonlocal buf
        body = "\n".join(buf).strip()
        if body:
            chunks.append(Chunk(len(chunks), buf_head, body))
        buf = []

    for line in lines:
        if _FENCE.match(line):
            in_fence = not in_fence
            buf.append(line)
            continue
        if not in_fence:
            h = _HEADING.match(line)
            if h:
                flush()
                depth = len(h.group(1))
                stack = stack[: depth - 1] + [h.group(2).strip()]
                buf_head = " > ".join(stack)
                continue
            if sum(len(x) + 1 for x in buf) >= TARGET_CHARS and not line.strip():
                flush()
                continue
        buf.append(line)
        if sum(len(x) + 1 for x in buf) >= MAX_CHARS and not in_fence:
            flush()
    flush()
    return chunks


@dataclass
class Heading:
    line: int           # 0-indexed line number of the heading itself
    depth: int          # 1..6
    text: str           # the heading without its hashes
    path: str           # "A > B > this one" — the same string chunks carry


def headings(md: str) -> list[Heading]:
    """The document's heading structure, fences excluded.

    Split out so partial writes address a section with the SAME grammar the
    chunker indexes one by. Two copies of "what counts as a heading" would mean
    an edit could land on a line search never treated as a heading — and the
    disagreement would only ever show up as a wrong edit.
    """
    out: list[Heading] = []
    stack: list[str] = []
    in_fence = False
    for i, line in enumerate(md.splitlines()):
        if _FENCE.match(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        h = _HEADING.match(line)
        if not h:
            continue
        depth = len(h.group(1))
        text = h.group(2).strip()
        stack = stack[: depth - 1] + [text]
        out.append(Heading(line=i, depth=depth, text=text, path=" > ".join(stack)))
    return out


PARA = ("projects", "areas", "resources", "archives")
SHARED = "shared"          # knowledge that belongs to no single repo. Reserved as a repo name.

# The store holds TEXT documents, not only markdown. A .dbml schema is canonical
# prose about a database; leaving it outside means it is neither searched nor
# covered by the store's backups. Binaries are not accepted.
TEXT_SUFFIXES = {".md", ".dbml"}

# How deep a document may sit BELOW the document root (``<owner>/<repo>``). The
# area is level 1. The ceiling exists to stop the deep folder tree of a file
# vault from growing back — depth is replaced by MOCs and links.
MAX_DEPTH = 5


class PathRejected(ValueError):
    """The address does not follow the rules. Distinct from a scope refusal
    (403): this one is a 400, because the request itself is malformed."""


def path_parts(rel: str) -> tuple[list[str], list[str]]:
    """path -> (document root segments, segments below it). Depth is the LENGTH
    OF THE SECOND HALF.

        acme/webapp / areas/backend/architecture/schema-design.md
        └ doc root ─┘  1      2       3            4

    owner/repo are coordinates, not depth. In a file vault they were directory
    levels; in the store they are columns — which is why they drop out of the
    depth calculation.
    """
    parts = [p for p in rel.split("/") if p]
    return parts[:2], parts[2:]


def validate_path(rel: str) -> None:
    """The address rules. The store is the authority here and the client mirrors
    this function so a malformed path comes back with the rule, not a bare 4xx.

    Four rules:
      1. the extension is in TEXT_SUFFIXES
      2. the document root ``<owner>/<repo>`` comes first (which owners are
         allowed is check_scope's business, not this function's)
      3. level 1 is either the root document (``README.md`` — the repo hub MOC)
         or a PARA area
      4. depth is at most MAX_DEPTH

    Rule 3 is the point. Written as a MINIMUM segment count — the tempting
    shape — it rejects ``acme/webapp/README.md``, a repo hub the store indexes
    and serves happily. What is needed is a depth CEILING measured from the
    document root, not a floor on segments.
    """
    rel = (rel or "").strip()
    if not rel:
        raise PathRejected("the path is empty")
    if not any(rel.endswith(x) for x in TEXT_SUFFIXES):
        raise PathRejected(f"unsupported extension: {rel} "
                           f"(allowed: {', '.join(sorted(TEXT_SUFFIXES))})")
    root, rest = path_parts(rel)
    if len(root) < 2:
        raise PathRejected(
            f"no document root in {rel} — a path starts with <owner>/<repo>/ "
            "(e.g. acme/webapp/README.md, acme/shared/resources/git-conventions.md)")
    if not rest:
        # Only two segments, so the last one is the filename — the root is half
        # there (``acme/README.md``: the repo is missing).
        raise PathRejected(
            f"the document root is only half there: {rel} — the form is "
            "<owner>/<repo>/<document> (e.g. acme/webapp/README.md)")
    if len(rest) > MAX_DEPTH:
        raise PathRejected(
            f"too deep ({len(rest)} levels, ceiling {MAX_DEPTH}): {rel} — at most "
            f"{MAX_DEPTH} levels below the document root (<owner>/<repo>). "
            "Use links instead of depth")
    if len(rest) > 1 and rest[0] not in PARA:
        raise PathRejected(
            f"the area (first segment below the document root) must be one of "
            f"{'|'.join(PARA)}: {rest[0]!r} — only a repo hub README comes with no area")


def split_path(rel: str) -> tuple[str, str, str]:
    """path -> (owner, repo, area). The convention is always three coordinates.

        <owner>/<repo>/<area>/…      acme/webapp/resources/logging.md
        <owner>/shared/<area>/…      acme/shared/resources/error-codes.md

    The three are DERIVED from the path but every query reads the columns. If
    they were only ever read back out of the path, reclassifying a document
    would change its address and break every wikilink pointing at it.

    owner is separate because the confidentiality boundary lives there. An
    allow-list of repos falls behind the moment someone creates one; "this group
    only" keeps being correct as repos come and go.

    The area is the FIRST segment below the document root. When that slot holds
    a filename instead — a repo hub such as ``acme/webapp/README.md`` — the area
    is ``root``. This function only derives; rejecting is validate_path's job,
    in one place.
    """
    root, rest = path_parts(rel)
    area = rest[0] if rest and rest[0] in PARA else "root"
    if len(root) == 2 and rest:
        return root[0], root[1], area
    # Without a document root, no owner is invented. An empty owner is refused
    # by the allow-list, which is the safe direction.
    return "", SHARED, "root"


def link_source(md: str) -> str:
    """The text links are counted from — fenced blocks and inline code blanked.
    Chunking and indexing use the original body untouched."""
    return _INLINE_CODE.sub("", _CODE_FENCE_BLOCK.sub("", md))


# A YAML front-matter block: three dashes, the block, three dashes, all at the
# very start. Anything else that merely contains a `---` line is a horizontal
# rule and must not match.
_FRONT_MATTER = re.compile(r"\A---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|\Z)", re.DOTALL)
_FM_TITLE = re.compile(r"^title:[ \t]*(.+?)[ \t]*$", re.MULTILINE)


def derive_title(md: str) -> str:
    """The document's name, from its own text.

    Front matter is handled rather than ignored. A document that opens with one
    has `---` as its first non-blank line, and taking that literally produced a
    store where thirteen career notes were all called "---" — sorted together,
    indistinguishable in search results, and each one's real title sitting
    unread on the next line. So: if the block declares a title, that is the
    title; otherwise the search continues after the block, where the heading is.
    """
    m = _FRONT_MATTER.match(md)
    if m:
        declared = _FM_TITLE.search(m.group(1))
        if declared:
            return declared.group(1).strip().strip("\"'")
        md = md[m.end():]
    first = next((l for l in md.splitlines() if l.strip()), "")
    return first.lstrip("# ").strip()


def parse_text(rel: str, md: str) -> dict:
    """Interpret a document from its path string and body alone.

    No file access is the point: the service receives bodies over HTTP, so it
    keeps no documents on disk and needs neither a checkout nor git credentials.
    """
    name = rel.rsplit("/", 1)[-1]
    if name.endswith(".md"):
        title = derive_title(md) or name.removesuffix(".md")
    else:
        # A non-markdown text document (dbml and friends) starts with a comment,
        # which cannot serve as a title. The filename is the name.
        title = name
    owner, repo, area = split_path(rel)
    return dict(
        path=rel,
        body=md,
        owner=owner,
        repo=repo,
        title=title,
        area=area,
        sha256=hashlib.sha256(md.encode()).hexdigest(),
        chars=len(md),
        chunks=chunk(md),
        links=sorted({m.group(1).strip() for m in _WIKILINK.finditer(link_source(md))}),
        # A relative link needs the document's own location to resolve, so it is
        # passed through raw and the indexer resolves it.
        rel_links=sorted({m.group(1).strip() for m in _MDLINK.finditer(link_source(md))}),
    )
