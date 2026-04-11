"""
Senate votes scraper for CivicTracker.

Mirrors the Commons votes scraper but targets sencanada.ca.
"""

from __future__ import annotations

import logging
import re
from typing import Any, Optional

import requests
from bs4 import BeautifulSoup

from crawler.utils import (
    build_session,
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

SENATE_VOTES_URL = "https://sencanada.ca/en/in-the-chamber/votes"

# Parliament / session constants — update when a new parliament opens
CURRENT_PARLIAMENT = 45
CURRENT_SESSION = 1


# ---------------------------------------------------------------------------
# Senate votes index
# ---------------------------------------------------------------------------


def crawl_senate_votes_index(
    url: str = SENATE_VOTES_URL,
    parliament: int = CURRENT_PARLIAMENT,
    session: int = CURRENT_SESSION,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape the sencanada.ca votes index page.

    Returns a list of division stubs:
        id, parliament, session, number, date, description,
        yeas, nays, result, detail_url, chamber='senate'
    """
    if session_http is None:
        session_http = build_session()

    logger.info("Fetching Senate votes index: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch Senate votes index: %s", exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    divisions: list[dict[str, Any]] = []

    table = soup.select_one("table.table, table")
    if not table:
        logger.warning("Could not find Senate votes table on %s", url)
        return []

    for row in table.select("tbody tr"):
        cols = row.select("td")
        if len(cols) < 3:
            continue

        vote_number_text = re.sub(r"\D", "", cols[0].get_text(strip=True))
        if not vote_number_text:
            continue
        vote_number = int(vote_number_text)

        date_text = cols[1].get_text(strip=True) if len(cols) > 1 else ""
        description = cols[2].get_text(strip=True) if len(cols) > 2 else ""

        try:
            yeas = int(re.sub(r"\D", "", cols[3].get_text(strip=True)) or 0) if len(cols) > 3 else 0
        except ValueError:
            yeas = 0

        try:
            nays = int(re.sub(r"\D", "", cols[4].get_text(strip=True)) or 0) if len(cols) > 4 else 0
        except ValueError:
            nays = 0

        result = cols[5].get_text(strip=True) if len(cols) > 5 else None

        link_el = row.select_one("a")
        detail_url: Optional[str] = None
        if link_el:
            href = link_el.get("href", "")
            detail_url = href if href.startswith("http") else f"https://sencanada.ca{href}"

        division_id = f"senate-{extract_division_id(parliament, session, vote_number)}"

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
                "chamber": "senate",
                "detail_url": detail_url,
                "last_scraped": now_iso(),
            }
        )

    logger.info("Found %d Senate divisions", len(divisions))
    return divisions


# ---------------------------------------------------------------------------
# Senate division detail
# ---------------------------------------------------------------------------


def crawl_senate_division_detail(
    division_id: str,
    url: str,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape an individual Senate division detail page.

    Returns a list of member-vote records:
        division_id, member_id, vote
    """
    if session_http is None:
        session_http = build_session()

    logger.debug("Scraping Senate division detail: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch Senate division detail %s: %s", url, exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    votes: list[dict[str, Any]] = []

    vote_sections = {
        "Yea": [".vote-yea li a", "ul.yea li a", "[class*='Yea'] li a"],
        "Nay": [".vote-nay li a", "ul.nay li a", "[class*='Nay'] li a"],
        "Abstain": [".vote-abstain li a", "ul.abstain li a", "[class*='Abstain'] li a"],
    }

    for vote_type, selectors in vote_sections.items():
        for selector in selectors:
            member_els = soup.select(selector)
            if member_els:
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
                break

    logger.debug(
        "Senate division %s: %d member votes parsed", division_id, len(votes)
    )
    return votes
