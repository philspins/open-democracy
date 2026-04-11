"""
Tests for crawler/scrapers/members.py

Uses mock HTTP responses so no real network calls are made.
"""

from __future__ import annotations

import textwrap
from unittest.mock import MagicMock, patch

import pytest

from crawler.scrapers.members import (
    crawl_member_profile,
    crawl_members_list,
    _normalise_vote,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _mock_response(html: str):
    resp = MagicMock()
    resp.text = html
    resp.status_code = 200
    resp.from_cache = False
    resp.raise_for_status = MagicMock()
    return resp


# ---------------------------------------------------------------------------
# _normalise_vote
# ---------------------------------------------------------------------------


class TestNormaliseVote:
    def test_yea_variants(self):
        for raw in ("Yea", "yea", "Yes", "yes", "Pour", "pour", "Oui", "oui"):
            assert _normalise_vote(raw) == "Yea", f"Failed for: {raw!r}"

    def test_nay_variants(self):
        for raw in ("Nay", "nay", "No", "no", "Contre", "contre", "Non", "non"):
            assert _normalise_vote(raw) == "Nay", f"Failed for: {raw!r}"

    def test_paired(self):
        assert _normalise_vote("Paired") == "Paired"
        assert _normalise_vote("paired") == "Paired"

    def test_unknown_becomes_abstain(self):
        assert _normalise_vote("Absent") == "Abstain"
        assert _normalise_vote("") == "Abstain"


# ---------------------------------------------------------------------------
# crawl_members_list
# ---------------------------------------------------------------------------

SAMPLE_MEMBERS_LIST_HTML = textwrap.dedent(
    """
    <html><body>
      <div class="ce-mip-mp-tile">
        <a href="/Members/en/111">
          <span class="ce-mip-mp-name">Jane Doe</span>
        </a>
        <span class="ce-mip-mp-party">Liberal</span>
        <span class="ce-mip-mp-constituency">Ottawa Centre</span>
        <span class="ce-mip-mp-province">Ontario</span>
      </div>
      <div class="ce-mip-mp-tile">
        <a href="/Members/en/222">
          <span class="ce-mip-mp-name">John Smith</span>
        </a>
        <span class="ce-mip-mp-party">Conservative</span>
        <span class="ce-mip-mp-constituency">Calgary East</span>
        <span class="ce-mip-mp-province">Alberta</span>
      </div>
    </body></html>
    """
)


class TestCrawlMembersList:
    def test_parses_member_tiles(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_MEMBERS_LIST_HTML),
        ):
            result = crawl_members_list()

        assert len(result) == 2

    def test_parses_first_member(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_MEMBERS_LIST_HTML),
        ):
            result = crawl_members_list()

        first = result[0]
        assert first["id"] == "111"
        assert first["name"] == "Jane Doe"
        assert first["party"] == "Liberal"
        assert first["riding"] == "Ottawa Centre"
        assert first["province"] == "Ontario"
        assert first["chamber"] == "commons"

    def test_returns_empty_on_error(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            side_effect=Exception("Network error"),
        ):
            result = crawl_members_list()

        assert result == []

    def test_returns_empty_when_no_tiles(self):
        html = "<html><body><p>No members</p></body></html>"
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(html),
        ):
            result = crawl_members_list()

        assert result == []


# ---------------------------------------------------------------------------
# crawl_member_profile
# ---------------------------------------------------------------------------

SAMPLE_PROFILE_HTML = textwrap.dedent(
    """
    <html><body>
      <h1 class="ce-mip-mp-name">Jane Doe</h1>
      <span class="ce-mip-mp-party">Liberal</span>
      <span class="ce-mip-mp-constituency">Ottawa Centre</span>
      <span class="ce-mip-mp-province">Ontario</span>
      <div class="ce-mip-mp-role">Member of Parliament</div>
      <div class="ce-mip-mp-picture">
        <img src="/photo/123006.jpg" alt="Jane Doe photo">
      </div>
      <a href="mailto:jane.doe@parl.gc.ca">jane.doe@parl.gc.ca</a>
    </body></html>
    """
)


class TestCrawlMemberProfile:
    def test_parses_name(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_PROFILE_HTML),
        ):
            result = crawl_member_profile("123006")

        assert result["name"] == "Jane Doe"

    def test_parses_party(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_PROFILE_HTML),
        ):
            result = crawl_member_profile("123006")

        assert result["party"] == "Liberal"

    def test_parses_email(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_PROFILE_HTML),
        ):
            result = crawl_member_profile("123006")

        assert result["email"] == "jane.doe@parl.gc.ca"

    def test_parses_photo_url(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_PROFILE_HTML),
        ):
            result = crawl_member_profile("123006")

        assert result["photo_url"] == "https://www.ourcommons.ca/photo/123006.jpg"

    def test_member_id_preserved(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            return_value=_mock_response(SAMPLE_PROFILE_HTML),
        ):
            result = crawl_member_profile("123006")

        assert result["id"] == "123006"

    def test_returns_stub_on_http_error(self):
        with patch(
            "crawler.scrapers.members.polite_get",
            side_effect=Exception("Network error"),
        ):
            result = crawl_member_profile("123006")

        assert result["id"] == "123006"
        assert "last_scraped" in result
