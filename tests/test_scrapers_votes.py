"""
Tests for crawler/scrapers/votes.py

Uses mock HTTP responses so no real network calls are made.
"""

from __future__ import annotations

import textwrap
from unittest.mock import MagicMock, patch

import pytest

from crawler.scrapers.votes import (
    crawl_votes_index,
    crawl_division_detail,
    crawl_sitting_calendar,
    parliament_is_sitting,
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
# Votes index
# ---------------------------------------------------------------------------

SAMPLE_VOTES_HTML = textwrap.dedent(
    """
    <html><body>
      <table class="table">
        <thead><tr>
          <th>#</th><th>Date</th><th>Description</th>
          <th>Yeas</th><th>Nays</th><th>Result</th>
        </tr></thead>
        <tbody>
          <tr>
            <td><a href="/Members/en/votes/892">892</a></td>
            <td>April 3, 2024</td>
            <td>Motion on C-47</td>
            <td>172</td>
            <td>148</td>
            <td>Agreed to</td>
          </tr>
          <tr>
            <td><a href="/Members/en/votes/891">891</a></td>
            <td>April 2, 2024</td>
            <td>Motion on S-209</td>
            <td>100</td>
            <td>90</td>
            <td>Negatived</td>
          </tr>
        </tbody>
      </table>
    </body></html>
    """
)


class TestCrawlVotesIndex:
    def test_parses_division_rows(self):
        with patch("crawler.scrapers.votes.polite_get", return_value=_mock_response(SAMPLE_VOTES_HTML)):
            result = crawl_votes_index(parliament=45, session=1)

        assert len(result) == 2

    def test_parses_first_division_correctly(self):
        with patch("crawler.scrapers.votes.polite_get", return_value=_mock_response(SAMPLE_VOTES_HTML)):
            result = crawl_votes_index(parliament=45, session=1)

        first = result[0]
        assert first["id"] == "45-1-892"
        assert first["number"] == 892
        assert first["yeas"] == 172
        assert first["nays"] == 148
        assert first["result"] == "Agreed to"
        assert first["date"] == "2024-04-03"
        assert first["chamber"] == "commons"

    def test_builds_detail_url(self):
        with patch("crawler.scrapers.votes.polite_get", return_value=_mock_response(SAMPLE_VOTES_HTML)):
            result = crawl_votes_index(parliament=45, session=1)

        assert result[0]["detail_url"] == "https://www.ourcommons.ca/Members/en/votes/892"

    def test_returns_empty_on_http_error(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            side_effect=Exception("Network error"),
        ):
            result = crawl_votes_index()

        assert result == []

    def test_handles_missing_table(self):
        html = "<html><body><p>No table here</p></body></html>"
        with patch("crawler.scrapers.votes.polite_get", return_value=_mock_response(html)):
            result = crawl_votes_index()

        assert result == []


# ---------------------------------------------------------------------------
# Division detail
# ---------------------------------------------------------------------------

SAMPLE_DIVISION_HTML = textwrap.dedent(
    """
    <html><body>
      <div class="vote-yea">
        <ul>
          <li class="member-name"><a href="/Members/en/111">Alice Smith</a></li>
          <li class="member-name"><a href="/Members/en/222">Bob Jones</a></li>
        </ul>
      </div>
      <div class="vote-nay">
        <ul>
          <li class="member-name"><a href="/Members/en/333">Carol Brown</a></li>
        </ul>
      </div>
    </body></html>
    """
)


class TestCrawlDivisionDetail:
    def test_parses_yea_votes(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            return_value=_mock_response(SAMPLE_DIVISION_HTML),
        ):
            result = crawl_division_detail("45-1-892", "https://example.com/vote/892")

        yea_votes = [v for v in result if v["vote"] == "Yea"]
        assert len(yea_votes) == 2
        member_ids = {v["member_id"] for v in yea_votes}
        assert "111" in member_ids
        assert "222" in member_ids

    def test_parses_nay_votes(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            return_value=_mock_response(SAMPLE_DIVISION_HTML),
        ):
            result = crawl_division_detail("45-1-892", "https://example.com/vote/892")

        nay_votes = [v for v in result if v["vote"] == "Nay"]
        assert len(nay_votes) == 1
        assert nay_votes[0]["member_id"] == "333"

    def test_all_votes_have_division_id(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            return_value=_mock_response(SAMPLE_DIVISION_HTML),
        ):
            result = crawl_division_detail("45-1-892", "https://example.com/vote/892")

        assert all(v["division_id"] == "45-1-892" for v in result)

    def test_returns_empty_on_error(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            side_effect=Exception("Network error"),
        ):
            result = crawl_division_detail("45-1-892", "https://example.com/vote/892")

        assert result == []


# ---------------------------------------------------------------------------
# Sitting calendar
# ---------------------------------------------------------------------------

SAMPLE_CALENDAR_HTML = textwrap.dedent(
    """
    <html><body>
      <table>
        <tbody>
          <tr>
            <td class="sitting" data-date="2024-04-03">3</td>
            <td class="sitting" data-date="2024-04-04">4</td>
            <td data-date="2024-04-06">6</td>
          </tr>
        </tbody>
      </table>
    </body></html>
    """
)


class TestCrawlSittingCalendar:
    def test_parses_sitting_dates(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            return_value=_mock_response(SAMPLE_CALENDAR_HTML),
        ):
            result = crawl_sitting_calendar()

        assert "2024-04-03" in result
        assert "2024-04-04" in result

    def test_dates_are_unique_and_sorted(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            return_value=_mock_response(SAMPLE_CALENDAR_HTML),
        ):
            result = crawl_sitting_calendar()

        assert result == sorted(set(result))

    def test_returns_empty_on_error(self):
        with patch(
            "crawler.scrapers.votes.polite_get",
            side_effect=Exception("Network error"),
        ):
            result = crawl_sitting_calendar()

        assert result == []


# ---------------------------------------------------------------------------
# parliament_is_sitting
# ---------------------------------------------------------------------------


class TestParliamentIsSitting:
    def test_returns_true_when_date_in_list(self):
        assert parliament_is_sitting(["2024-04-03", "2024-04-04"], today="2024-04-03")

    def test_returns_false_when_date_not_in_list(self):
        assert not parliament_is_sitting(["2024-04-03", "2024-04-04"], today="2024-04-05")

    def test_returns_false_for_empty_list(self):
        assert not parliament_is_sitting([], today="2024-04-03")
