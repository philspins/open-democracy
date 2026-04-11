"""
Votes scraper for CivicTracker.

Scrapes:
1. ourcommons.ca votes index → list of all recorded divisions
2. Individual division detail page → how each MP voted
3. Sitting calendar page → which days parliament is in session
"""

from __future__ import annotations

import logging
import re
from typing import Any, Optional

import requests
from bs4 import BeautifulSoup

from crawler.utils import (
    build_session,
    extract_bill_id,
    extract_division_id,
    extract_member_id,
    parse_date,
    polite_get,
    now_iso,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

VOTES_INDEX_URL = "https://www.ourcommons.ca/Members/en/votes"
SITTING_CALENDAR_URL = "https://www.ourcommons.ca/en/sitting-calendar"

# Parliament / session constants — update when a new parliament opens
CURRENT_PARLIAMENT = 45
CURRENT_SESSION = 1

# ---------------------------------------------------------------------------
# Votes index
# ---------------------------------------------------------------------------


def crawl_votes_index(
    url: str = VOTES_INDEX_URL,
    parliament: int = CURRENT_PARLIAMENT,
    session: int = CURRENT_SESSION,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape the ourcommons votes index table.

    Returns a list of division stubs:
        id, parliament, session, number, date, description,
        yeas, nays, result, detail_url
    """
    if session_http is None:
        session_http = build_session()

    logger.info("Fetching votes index: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch votes index: %s", exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    divisions: list[dict[str, Any]] = []

    table = soup.select_one("table.table, table#votes-table, table")
    if not table:
        logger.warning("Could not find votes table on %s", url)
        return []

    for row in table.select("tbody tr"):
        cols = row.select("td")
        if len(cols) < 4:
            continue

        try:
            vote_number = int(re.sub(r"\D", "", cols[0].get_text(strip=True)) or 0)
        except ValueError:
            continue

        date_text = cols[1].get_text(strip=True)
        description = cols[2].get_text(strip=True)

        try:
            yeas = int(re.sub(r"\D", "", cols[3].get_text(strip=True)) or 0)
        except ValueError:
            yeas = 0

        try:
            nays = int(re.sub(r"\D", "", cols[4].get_text(strip=True)) or 0) if len(cols) > 4 else 0
        except ValueError:
            nays = 0

        result = cols[5].get_text(strip=True) if len(cols) > 5 else None

        # Extract detail URL
        link_el = row.select_one("a[href*='votes']")
        detail_url = None
        if link_el:
            href = link_el.get("href", "")
            if href.startswith("http"):
                detail_url = href
            else:
                detail_url = f"https://www.ourcommons.ca{href}"

        division_id = extract_division_id(parliament, session, vote_number)

        divisions.append(
            {
                "id": division_id,
                "parliament": parliament,
                "session": session,
                "number": vote_number,
                "date": parse_date(date_text),
                "description": description,
                "yeas": yeas,
                "nays": nays,
                "paired": 0,
                "result": result,
                "chamber": "commons",
                "detail_url": detail_url,
                "last_scraped": now_iso(),
            }
        )

    logger.info("Found %d divisions on votes index", len(divisions))
    return divisions


# ---------------------------------------------------------------------------
# Division detail
# ---------------------------------------------------------------------------


def crawl_division_detail(
    division_id: str,
    url: str,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape an individual division detail page.

    Returns a list of member-vote records:
        division_id, member_id, vote
    """
    if session_http is None:
        session_http = build_session()

    logger.debug("Scraping division detail: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch division detail %s: %s", url, exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    votes: list[dict[str, Any]] = []

    # The page has sections for Yea, Nay, Paired (and sometimes Abstain)
    vote_sections = {
        "Yea": [
            ".vote-yea .member-name a",
            "[class*='Yea'] .member-name a",
            "section.agreed-to li a",
            "ul.yea li a",
        ],
        "Nay": [
            ".vote-nay .member-name a",
            "[class*='Nay'] .member-name a",
            "section.negatived li a",
            "ul.nay li a",
        ],
        "Paired": [
            ".vote-paired .member-name a",
            "[class*='Paired'] .member-name a",
            "ul.paired li a",
        ],
    }

    for vote_type, selectors in vote_sections.items():
        found_any = False
        for selector in selectors:
            member_els = soup.select(selector)
            if member_els:
                found_any = True
                for el in member_els:
                    href = el.get("href", "")
                    member_id = extract_member_id(href)
                    if member_id:
                        votes.append(
                            {
                                "division_id": division_id,
                                "member_id": member_id,
                                "vote": vote_type,
                            }
                        )
                break  # stop trying other selectors for this vote type
        if not found_any:
            logger.debug("No '%s' voters found for division %s", vote_type, division_id)

    logger.debug("Division %s: %d member votes parsed", division_id, len(votes))
    return votes


# ---------------------------------------------------------------------------
# Sitting calendar
# ---------------------------------------------------------------------------


def crawl_sitting_calendar(
    url: str = SITTING_CALENDAR_URL,
    parliament: int = CURRENT_PARLIAMENT,
    session: int = CURRENT_SESSION,
    session_http: Optional[requests.Session] = None,
) -> list[str]:
    """
    Scrape the ourcommons sitting calendar.

    Returns a list of ISO-8601 date strings for all scheduled sitting days.
    """
    if session_http is None:
        session_http = build_session()

    logger.info("Fetching sitting calendar: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch sitting calendar: %s", exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    sitting_dates: list[str] = []

    # The calendar page marks sitting days with specific CSS classes
    for el in soup.select(
        "[data-date], td.sitting, td[class*='sitting'], [class*='sitting-day']"
    ):
        # Try data-date attribute first (most reliable)
        raw_date = el.get("data-date") or el.get("datetime")
        if not raw_date:
            raw_date = el.get_text(strip=True)

        parsed = parse_date(raw_date)
        if parsed:
            sitting_dates.append(parsed)

    # De-duplicate and sort
    sitting_dates = sorted(set(sitting_dates))
    logger.info("Found %d sitting dates", len(sitting_dates))
    return sitting_dates


def parliament_is_sitting(
    sitting_dates: list[str],
    today: Optional[str] = None,
) -> bool:
    """
    Return True if *today* (ISO-8601) is in the list of sitting dates.
    Defaults to today's date if *today* is not provided.
    """
    from crawler.utils import today_iso
    check_date = today or today_iso()
    return check_date in sitting_dates
