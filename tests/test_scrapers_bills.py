"""
Tests for crawler/scrapers/bills.py

Uses mock HTTP responses so no real network calls are made.
"""

from __future__ import annotations

import textwrap
import pytest
from unittest.mock import MagicMock, patch

from crawler.scrapers.bills import (
    crawl_bills_rss,
    crawl_bill_detail,
    _bill_chamber,
    STAGE_ORDER,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_rss_feed(entries):
    """Build a minimal feedparser-like feed dict."""
    feed = MagicMock()
    feed.entries = []
    for entry_data in entries:
        e = MagicMock()
        e.link = entry_data.get("link", "")
        e.title = entry_data.get("title", "")
        e.get = lambda k, d=None, ed=entry_data: ed.get(k, d)
        feed.entries.append(e)
    return feed


def _mock_response(html: str, from_cache: bool = False):
    resp = MagicMock()
    resp.text = html
    resp.status_code = 200
    resp.from_cache = from_cache
    resp.raise_for_status = MagicMock()
    return resp


# ---------------------------------------------------------------------------
# _bill_chamber
# ---------------------------------------------------------------------------


class TestBillChamber:
    def test_commons_bill(self):
        assert _bill_chamber("C-47") == "commons"

    def test_senate_bill(self):
        assert _bill_chamber("S-209") == "senate"

    def test_lowercase(self):
        assert _bill_chamber("c-47") == "commons"

    def test_empty_string(self):
        assert _bill_chamber("") == "commons"

    def test_unknown_prefix(self):
        assert _bill_chamber("X-99") == "commons"


# ---------------------------------------------------------------------------
# crawl_bills_rss
# ---------------------------------------------------------------------------


class TestCrawlBillsRss:
    def test_parses_valid_entries(self):
        fake_feed = _make_rss_feed(
            [
                {
                    "link": "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
                    "title": "Budget Implementation Act",
                    "updated": "2024-04-03",
                },
                {
                    "link": "https://www.parl.ca/legisinfo/en/bill/45-1/s-209",
                    "title": "Senate Bill",
                    "updated": "2024-04-01",
                },
            ]
        )
        with patch("crawler.scrapers.bills.feedparser.parse", return_value=fake_feed):
            result = crawl_bills_rss()

        assert len(result) == 2
        assert result[0]["id"] == "45-1-c-47"
        assert result[0]["title"] == "Budget Implementation Act"
        assert result[1]["id"] == "45-1-s-209"

    def test_skips_entries_without_bill_id(self):
        fake_feed = _make_rss_feed(
            [
                {
                    "link": "https://www.parl.ca/legisinfo/en/bills/rss",
                    "title": "RSS root — no bill ID",
                },
            ]
        )
        with patch("crawler.scrapers.bills.feedparser.parse", return_value=fake_feed):
            result = crawl_bills_rss()

        assert result == []

    def test_empty_feed(self):
        fake_feed = _make_rss_feed([])
        with patch("crawler.scrapers.bills.feedparser.parse", return_value=fake_feed):
            result = crawl_bills_rss()

        assert result == []


# ---------------------------------------------------------------------------
# crawl_bill_detail
# ---------------------------------------------------------------------------


SAMPLE_BILL_HTML = textwrap.dedent(
    """
    <html><body>
      <div class="bill-latest-activity">2nd Reading — April 3, 2024</div>
      <div class="bill-type">Government Bill</div>
      <h2 id="SecondReading">Second Reading</h2>
      <p>April 3, 2024</p>
      <div class="bill-profile-sponsor">
        Sponsored by <a href="/Members/en/123006">Jane Doe</a>
      </div>
    </body></html>
    """
)


class TestCrawlBillDetail:
    def test_parses_current_status(self):
        mock_session = MagicMock()
        mock_session.get.return_value = _mock_response(SAMPLE_BILL_HTML)

        with patch("crawler.scrapers.bills.polite_get", return_value=_mock_response(SAMPLE_BILL_HTML)):
            result = crawl_bill_detail(
                "45-1-c-47",
                "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
                session=mock_session,
            )

        assert result["current_status"] == "2nd Reading — April 3, 2024"

    def test_parses_sponsor_id(self):
        with patch("crawler.scrapers.bills.polite_get", return_value=_mock_response(SAMPLE_BILL_HTML)):
            result = crawl_bill_detail(
                "45-1-c-47",
                "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
            )

        assert result["sponsor_id"] == "123006"

    def test_parses_bill_type(self):
        with patch("crawler.scrapers.bills.polite_get", return_value=_mock_response(SAMPLE_BILL_HTML)):
            result = crawl_bill_detail(
                "45-1-c-47",
                "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
            )

        assert result["bill_type"] == "Government Bill"

    def test_builds_full_text_url(self):
        with patch("crawler.scrapers.bills.polite_get", return_value=_mock_response(SAMPLE_BILL_HTML)):
            result = crawl_bill_detail(
                "45-1-c-47",
                "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
            )

        assert result["full_text_url"] == (
            "https://www.parl.ca/DocumentViewer/en/45-1/bill/C-47/first-reading"
        )

    def test_returns_empty_dict_on_http_error(self):
        with patch(
            "crawler.scrapers.bills.polite_get",
            side_effect=Exception("Connection error"),
        ):
            result = crawl_bill_detail(
                "45-1-c-47",
                "https://www.parl.ca/legisinfo/en/bill/45-1/c-47",
            )

        assert result == {}
