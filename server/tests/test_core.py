"""Rules that need no database: addresses, chunking, lexemes, link extraction."""
import pytest

from core import (MAX_DEPTH, PathRejected, chunk, lexemes, link_source,
                  parse_text, path_parts, split_path, validate_path)


# -- addresses ---------------------------------------------------------------

@pytest.mark.parametrize("path", [
    "acme/webapp/README.md",                                   # repo hub MOC — no area, and legal
    "acme/shared/resources/git-conventions.md",                # the ordinary shape
    "acme/webapp/areas/backend/architecture/schema-design.md",  # 4 levels
    "acme/webapp/areas/a/b/c/d.md",                            # 5 levels — the ceiling
    "acme/shared/resources/dbml/schema.dbml",                  # dbml is a text document too
])
def test_valid_paths(path):
    validate_path(path)


@pytest.mark.parametrize("path,why", [
    ("acme/webapp/areas/a/b/c/d/e.md", "6 levels — over the ceiling"),
    ("acme/webapp/notes/x.md", "notes is not a PARA area"),
    ("acme/webapp/README.txt", "not a text document the store takes"),
    ("acme/README.md", "half a document root — the repo is missing"),
    ("README.md", "no document root at all"),
    ("", "empty"),
])
def test_rejected_paths(path, why):
    with pytest.raises(PathRejected):
        validate_path(path)


def test_depth_is_measured_below_the_document_root():
    """owner/repo are coordinates — columns in the store — not directory levels.
    Counting them as depth is what makes a legal 4-level document look like a
    6-level one and get refused."""
    assert len(path_parts("acme/webapp/README.md")[1]) == 1
    assert len(path_parts("acme/shared/resources/x.md")[1]) == 2
    assert len(path_parts("acme/webapp/areas/backend/architecture/x.md")[1]) == 4


def test_repo_hub_readme_has_no_area():
    """The area slot holding a filename means this is a repo hub. split_path
    derives; only validate_path refuses — keeping that in one place is why the
    hub is readable at all."""
    assert split_path("acme/webapp/README.md") == ("acme", "webapp", "root")
    assert split_path("acme/shared/resources/x.md") == ("acme", "shared", "resources")


def test_no_owner_is_invented_for_a_rootless_path():
    """An empty owner is refused by the allow-list, which is the safe direction.
    Guessing one would route a document into a group nobody chose."""
    owner, _, _ = split_path("README.md")
    assert owner == ""


def test_depth_ceiling_matches_the_documented_constant():
    validate_path("acme/webapp/" + "/".join(["areas"] + ["x"] * (MAX_DEPTH - 2)) + "/y.md")


# -- chunking ----------------------------------------------------------------

def test_a_code_fence_is_never_split():
    """A fragment of a fence is neither runnable nor quotable, so size limits
    give way to the fence boundary."""
    fence = "```python\n" + "\n".join(f"line_{i} = {i}" for i in range(400)) + "\n```"
    md = "# Title\n\n" + fence + "\n"
    chunks = chunk(md)
    joined = "\n".join(c.body for c in chunks)
    assert joined.count("```") == 2
    holding = [c for c in chunks if "```python" in c.body]
    assert len(holding) == 1
    assert holding[0].body.rstrip().endswith("```")


def test_heading_path_is_carried_on_every_chunk():
    """A hit is a chunk, and without its heading path the reader cannot tell
    where in the document it was cut from."""
    md = "# Guide\n\n## Setup\n\ntext one\n\n### Token\n\ntext two\n"
    heads = {c.heading_path for c in chunk(md)}
    assert "Guide > Setup > Token" in heads


# -- lexemes -----------------------------------------------------------------

def test_identifiers_are_kept_whole_and_split():
    """Postgres's default parser breaks identifiers apart. Keeping the whole
    token AND its parts means a query from either direction reaches the document."""
    lex = lexemes("http_client_pool failed")
    assert "http_client_pool" in lex
    assert "client" in lex


def test_cjk_is_indexed_as_syllable_bigrams():
    """No morphological extension is in the image; 2-grams keep the whole thing
    on stock Postgres, which is what keeps the deployment one compose file."""
    lex = lexemes("검색 실패")
    assert "검색" in lex and "실패" in lex


def test_lexemes_are_deduplicated_in_order():
    assert lexemes("alpha alpha beta") == ["alpha", "beta"]


# -- links -------------------------------------------------------------------

def test_code_is_stripped_before_links_are_counted():
    """Without this, documentation that SHOWS wikilink syntax, a bash
    `[[ -n "$x" ]]`, and a regex all register as broken links."""
    md = (
        "# Doc\n\n"
        "A real link to [[actual-note]].\n\n"
        "Inline syntax example: `[[not-a-link]]`.\n\n"
        "```bash\nif [[ -n \"$x\" ]]; then echo [[also-not-a-link]]; fi\n```\n"
    )
    parsed = parse_text("acme/webapp/resources/doc.md", md)
    assert parsed["links"] == ["actual-note"]
    assert "not-a-link" not in link_source(md)


def test_relative_markdown_links_count_as_links():
    """MOC files have used markdown links from the beginning. Counting only
    wikilinks makes every document in a folder look like an orphan."""
    md = "# MOC\n\n- [Logging](logging.md)\n- [External](https://example.com/x.md)\n"
    parsed = parse_text("acme/webapp/resources/README.md", md)
    assert parsed["rel_links"] == ["logging.md"]


def test_title_comes_from_the_first_heading_and_falls_back_to_the_filename():
    assert parse_text("acme/webapp/resources/x.md", "# Real Title\n\nbody")["title"] == "Real Title"
    assert parse_text("acme/webapp/resources/x.md", "\n\nno heading")["title"] == "no heading"
    # A non-markdown text document starts with a comment, which cannot be a title.
    assert parse_text("acme/shared/resources/schema.dbml", "// a comment\n")["title"] == "schema.dbml"


def test_front_matter_does_not_become_the_title():
    """A document opening with front matter has `---` as its first non-blank
    line. Taking that literally names every such document "---"."""
    p = "acme/webapp/areas/x.md"
    declared = "---\ntitle: 감사 추적 체계 설계\nstatus: draft\n---\n\n본문\n"
    assert parse_text(p, declared)["title"] == "감사 추적 체계 설계"

    # No title key: the heading after the block is the name.
    heading = "---\nstatus: draft\n---\n\n# Real Title\n\nbody\n"
    assert parse_text(p, heading)["title"] == "Real Title"

    # Quoted values are unwrapped.
    quoted = '---\ntitle: "Quoted: with a colon"\n---\n\nbody\n'
    assert parse_text(p, quoted)["title"] == "Quoted: with a colon"

    # A horizontal rule further down is not front matter.
    rule = "# Heading\n\nsome text\n\n---\n\nmore\n"
    assert parse_text(p, rule)["title"] == "Heading"

    # Front matter and nothing else falls back to the filename.
    assert parse_text(p, "---\nstatus: draft\n---\n")["title"] == "x"


# -- query construction ------------------------------------------------------

def test_query_drops_function_words_but_the_index_keeps_them():
    """A query ORs its lexemes with equal weight and ts_rank carries no
    corpus-level IDF, so a function word matching in a long document outranks the
    rare term that actually discriminates. It is worst in the title/path channel,
    weighted 1.6x on the assumption that a title match is high signal.

    Measured: without this, 'what value is RRF_K set to' returned six documents,
    none of them the one that defines RRF_K — while searching 'RRF_K' alone
    ranked it first. recall@5 over the bench corpus went 65% -> 97% when the
    query stopped carrying these words.
    """
    from search import STOPWORDS, to_tsquery

    tsq = to_tsquery("what value is RRF_K set to")
    assert "'rrf_k'" in tsq
    for noise in ("'what'", "'is'", "'to'"):
        assert noise not in tsq

    # The INDEX still holds them: dropping them there would make an identifier
    # containing one unfindable.
    assert "is" in lexemes("this is a test")
    assert "is" in STOPWORDS


def test_a_query_of_only_function_words_still_searches():
    """Better a weak answer than none. Emptying the query entirely would turn a
    clumsy question into zero results with no explanation."""
    from search import to_tsquery

    tsq = to_tsquery("what is it")
    assert tsq and tsq != "'zzzz'"


def test_cjk_queries_are_untouched_by_the_stopword_list():
    """The list is English function words only. CJK grammar attaches to the word
    and is indexed as syllable bigrams, so nothing here may filter it."""
    from search import to_tsquery

    tsq = to_tsquery("검색 실패")
    assert "'검색'" in tsq and "'실패'" in tsq
