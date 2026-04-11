"""
Tests for crawler/utils.py
"""

import pytest
from crawler.utils import (
    extract_bill_id,
    extract_member_id,
    extract_division_id,
    extract_bill_number_from_id,
    extract_parliament_session_from_bill_id,
    parse_date,
)
from crawler.scrapers.bills import _find_date_in_text


class TestExtractBillId:
    def test_standard_legisinfo_url(self):
        url = "https://www.parl.ca/legisinfo/en/bill/45-1/c-47"
        assert extract_bill_id(url) == "45-1-c-47"

    def test_senate_bill(self):
        url = "https://www.parl.ca/legisinfo/en/bill/45-1/s-209"
        assert extract_bill_id(url) == "45-1-s-209"

    def test_url_without_bill_path(self):
        assert extract_bill_id("https://www.parl.ca/legisinfo/en/bills/rss") is None

    def test_empty_string(self):
        assert extract_bill_id("") is None

    def test_uppercase_bill_number(self):
        url = "https://www.parl.ca/legisinfo/en/bill/45-1/C-47"
        result = extract_bill_id(url)
        assert result == "45-1-c-47"


class TestExtractMemberId:
    def test_standard_url(self):
        url = "https://www.ourcommons.ca/Members/en/123006"
        assert extract_member_id(url) == "123006"

    def test_url_with_tab(self):
        url = "https://www.ourcommons.ca/Members/en/123006?tab=votes"
        assert extract_member_id(url) == "123006"

    def test_relative_path(self):
        assert extract_member_id("/Members/en/99999") == "99999"

    def test_non_matching_url(self):
        assert extract_member_id("https://www.ourcommons.ca/en/") is None

    def test_empty_string(self):
        assert extract_member_id("") is None


class TestExtractDivisionId:
    def test_builds_correct_id(self):
        assert extract_division_id(45, 1, 892) == "45-1-892"

    def test_different_values(self):
        assert extract_division_id(44, 2, 100) == "44-2-100"


class TestExtractBillNumberFromId:
    def test_commons_bill(self):
        assert extract_bill_number_from_id("45-1-c-47") == "C-47"

    def test_senate_bill(self):
        assert extract_bill_number_from_id("45-1-s-209") == "S-209"

    def test_too_short(self):
        assert extract_bill_number_from_id("45-1") is None


class TestExtractParliamentSession:
    def test_valid_id(self):
        assert extract_parliament_session_from_bill_id("45-1-c-47") == (45, 1)

    def test_invalid_id(self):
        parliament, session = extract_parliament_session_from_bill_id("invalid")
        assert parliament is None
        assert session is None

    def test_empty_string(self):
        parliament, session = extract_parliament_session_from_bill_id("")
        assert parliament is None
        assert session is None


class TestParseDate:
    def test_iso_format(self):
        assert parse_date("2024-04-03") == "2024-04-03"

    def test_long_month_format(self):
        assert parse_date("April 3, 2024") == "2024-04-03"

    def test_day_month_year(self):
        assert parse_date("3 April 2024") == "2024-04-03"

    def test_abbreviated_month(self):
        assert parse_date("Apr 3, 2024") == "2024-04-03"

    def test_slash_format(self):
        assert parse_date("2024/04/03") == "2024-04-03"

    def test_none_input(self):
        assert parse_date(None) is None

    def test_empty_string(self):
        assert parse_date("") is None

    def test_unrecognised_format(self):
        assert parse_date("not-a-date") is None

    def test_whitespace_stripped(self):
        assert parse_date("  2024-04-03  ") == "2024-04-03"


class TestFindDateInText:
    def test_finds_iso_date(self):
        assert _find_date_in_text("Passed on 2024-04-03 in committee") == "2024-04-03"

    def test_finds_long_month(self):
        assert _find_date_in_text("Reading on April 3, 2024 was agreed to") == "2024-04-03"

    def test_no_date(self):
        assert _find_date_in_text("No date here at all") is None
