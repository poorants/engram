"""Tiers, snippets and the dynamic cut — the parts of search that decide what
a caller pays, tested without a database.

The ranking itself is measured by the bench (bench/eval_index.py); what is
pinned here is the budget arithmetic around it, because a mistake there is
silent: a snippet that cuts mid-word or a cutoff that drops the only answer
still returns 200.
"""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "app"))

from feedback import KINDS, WEIGHT, saturate  # noqa: E402
from search import (RRF_K, TIERS, UTIL_BONUS, REPO_BONUS, Hit, apply_cutoff,  # noqa: E402
                    next_tier, query_lexemes, snippet)


def hit(score: float, body: str = "body", path: str = "acme/shared/resources/x.md") -> Hit:
    return Hit(chunk_id=1, doc_id=1, ord=0, path=path, title="x", area="resources",
               owner="acme", repo="shared", heading_path="X > Y", body=body,
               score=score, lex_rank=1, vec_rank=None)


# -- tiers ---------------------------------------------------------------------

def test_tiers_widen_monotonically():
    """Each tier must show at least as much as the one below it, or raising the
    tier could hide an answer the lower one showed."""
    assert TIERS[1].limit <= TIERS[2].limit <= TIERS[3].limit
    assert TIERS[1].pool <= TIERS[2].pool <= TIERS[3].pool
    assert not TIERS[1].archives and TIERS[3].archives


def test_tier_one_is_snippets_and_tier_two_is_full_chunks():
    assert TIERS[1].snippet > 0
    assert TIERS[2].snippet == 0
    assert TIERS[3].snippet > 0


def test_only_the_first_tier_cuts_by_score():
    """The cut is what makes tier 1 cheap when the answer is obvious. Above it
    the caller has already said the first page was not enough, so nothing is
    withheld."""
    assert TIERS[1].cutoff > 0
    assert TIERS[2].cutoff == 0 and TIERS[3].cutoff == 0


def test_the_top_tier_drops_the_repo_boost():
    assert TIERS[1].repo_boost and TIERS[2].repo_boost and not TIERS[3].repo_boost


def test_snippet_tiers_collapse_to_one_chunk_per_document():
    """A page of snippets is for recognising the right document; the tier that
    sends text may show two chunks of the same one."""
    assert TIERS[1].collapse and TIERS[3].collapse and not TIERS[2].collapse


def test_the_first_tier_cut_is_gentler_than_a_double_match():
    """RRF scores a document matched in two channels at roughly twice one
    matched in one. A cut at half of the top would drop every single-channel
    match whenever a double match exists — measured: 94% -> 65% recall."""
    assert 0 < TIERS[1].cutoff < 0.5


def test_next_tier_points_up_and_stops_at_the_top():
    assert next_tier(1, 3, 10)["tier"] == 2
    assert next_tier(2, 6, 10)["tier"] == 3
    assert next_tier(3, 12, 30) is None


def test_no_candidates_skips_straight_to_the_widest_tier():
    """Tier 2 is the same pool with more of it shown; with nothing in the pool
    it cannot help, and sending a caller there is a wasted call."""
    assert next_tier(1, 0, 0)["tier"] == 3


# -- the dynamic cut ----------------------------------------------------------

def test_cutoff_drops_hits_far_below_the_top():
    hits = [hit(0.05), hit(0.04), hit(0.01), hit(0.009)]
    kept = apply_cutoff(hits, 0.5)
    assert [h.score for h in kept] == [0.05, 0.04]


def test_the_top_hit_always_survives_the_cut():
    assert apply_cutoff([hit(0.03)], 0.5) == [hit(0.03)]


def test_a_zero_cutoff_keeps_everything():
    hits = [hit(0.05), hit(0.001)]
    assert apply_cutoff(hits, 0.0) == hits


def test_cutoff_on_nothing_is_nothing():
    assert apply_cutoff([], 0.5) == []


# -- snippets --------------------------------------------------------------------

LONG = ("Intro sentence about nothing in particular. " * 6
        + "The constant RRF_K is set to 60 in search.py and never tuned by hand. "
        + "Trailing sentence that goes on and on. " * 8)


def test_snippet_centres_on_the_first_query_term():
    s = snippet(LONG, "what value is RRF_K set to", 160)
    assert "RRF_K" in s
    assert len(s) <= 160 + 2          # the two ellipsis marks
    assert s.startswith("…") and s.endswith("…")


def test_snippet_without_a_term_shows_the_opening():
    s = snippet(LONG, "zzzz nothing here", 100)
    assert s.startswith("Intro sentence")
    assert s.endswith("…")


def test_snippet_returns_a_short_body_whole():
    assert snippet("short body\nwith a newline", "anything", 240) == "short body with a newline"


def test_snippet_folds_whitespace():
    s = snippet("a\n\n\n  b\t\tc " * 50, "b", 60)
    assert "\n" not in s and "  " not in s


def test_snippet_does_not_cut_mid_word_at_the_end():
    s = snippet(LONG, "RRF_K", 120)
    inner = s.strip("…")
    assert not inner.endswith(" ")
    # the last token is a whole word from the text
    assert inner.split()[-1] in LONG


def test_snippet_finds_cjk_terms():
    body = "앞부분 설명이 길게 이어진다. " * 10 + "토큰 회전 절차는 여기 적혀 있다. " + "뒷부분. " * 20
    s = snippet(body, "토큰 회전은 어떻게 하나", 80)
    assert "토큰" in s


# -- the compact hit --------------------------------------------------------------

def test_compact_hit_carries_only_what_an_agent_acts_on():
    h = hit(0.0421, body=LONG)
    c = h.compact("RRF_K", 100)
    assert set(c) == {"path", "heading_path", "score", "snippet"}
    assert c["score"] == 0.0421
    assert "RRF_K" in c["snippet"]


def test_compact_hit_with_no_snippet_budget_sends_the_body():
    h = hit(0.1, body=LONG)
    c = h.compact("RRF_K", 0)
    assert set(c) == {"path", "heading_path", "score", "body"}
    assert c["body"] == LONG


# -- feedback arithmetic ------------------------------------------------------------

def test_query_lexemes_drop_function_words_but_never_everything():
    assert "is" not in query_lexemes("what value is RRF_K set to")
    assert query_lexemes("the is a") == ["the", "is", "a"]


def test_every_kind_has_a_weight_and_the_signs_are_right():
    assert KINDS == {"useful", "noise", "opened"}
    assert WEIGHT["useful"] > 0 > WEIGHT["noise"]
    assert 0 < WEIGHT["opened"] < WEIGHT["useful"]


def test_saturation_is_a_log_curve_with_a_ceiling_and_a_sign():
    assert saturate(0) == 0
    assert 0 < saturate(1) < saturate(3) < saturate(10) == 1.0
    assert saturate(1000) == 1.0
    assert saturate(-2) == -saturate(2)


def test_the_usefulness_bonus_is_a_tie_breaker():
    """The bonus at its ceiling must be worth a few places within one channel
    and never a channel: bigger than the gap between adjacent top ranks (so it
    does something), smaller than a rank-40 match in one channel (so it can
    never lift a document over one that matched better), and well under the
    repo bonus. Measured before this was pinned: one vote, six unrelated
    questions with the voted document put first."""
    one_step = 1.0 / (RRF_K + 1) - 1.0 / (RRF_K + 2)
    assert one_step < UTIL_BONUS < 1.0 / (RRF_K + 40)
    assert UTIL_BONUS < REPO_BONUS / 10
