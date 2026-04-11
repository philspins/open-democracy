"""
Members (MP) scraper for CivicTracker.

Scrapes:
1. ourcommons.ca MP profile page  → name, party, riding, photo, contact info
2. MP vote-history tab            → list of divisions the MP participated in
"""

from __future__ import annotations

import logging
import re
from typing import Any, Optional

import requests
from bs4 import BeautifulSoup

from crawler.utils import (
    build_session,
    extract_member_id,
    parse_date,
    polite_get,
    now_iso,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

MEMBERS_LIST_URL = "https://www.ourcommons.ca/Members/en/search"
MEMBER_PROFILE_TEMPLATE = "https://www.ourcommons.ca/Members/en/{member_id}"
MEMBER_VOTES_TEMPLATE = "https://www.ourcommons.ca/Members/en/{member_id}?tab=votes"


# ---------------------------------------------------------------------------
# MP list
# ---------------------------------------------------------------------------


def crawl_members_list(
    url: str = MEMBERS_LIST_URL,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape the ourcommons members list page for a stub of every active MP.

    Returns a list of dicts with keys: id, name, party, riding, province
    """
    if session_http is None:
        session_http = build_session()

    logger.info("Fetching members list: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch members list: %s", exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    members: list[dict[str, Any]] = []

    # The search results page renders member cards or a table
    for card in soup.select(
        ".ce-mip-mp-tile, [class*='mp-tile'], [class*='MemberTile'], article.member"
    ):
        link_el = card.select_one("a[href*='/Members/en/']")
        if not link_el:
            continue
        href = link_el.get("href", "")
        member_id = extract_member_id(href)
        if not member_id:
            continue

        name_el = card.select_one(
            ".ce-mip-mp-name, .member-name, [class*='Name'], h2, h3"
        )
        name = name_el.get_text(strip=True) if name_el else link_el.get_text(strip=True)

        party_el = card.select_one(
            ".ce-mip-mp-party, [class*='party'], [class*='Party']"
        )
        party = party_el.get_text(strip=True) if party_el else None

        riding_el = card.select_one(
            ".ce-mip-mp-constituency, [class*='constituency'], [class*='Constituency'], [class*='riding']"
        )
        riding = riding_el.get_text(strip=True) if riding_el else None

        province_el = card.select_one(
            ".ce-mip-mp-province, [class*='province'], [class*='Province']"
        )
        province = province_el.get_text(strip=True) if province_el else None

        members.append(
            {
                "id": member_id,
                "name": name,
                "party": party,
                "riding": riding,
                "province": province,
                "chamber": "commons",
                "active": True,
            }
        )

    logger.info("Members list: found %d MPs", len(members))
    return members


# ---------------------------------------------------------------------------
# MP profile page
# ---------------------------------------------------------------------------


def crawl_member_profile(
    member_id: str,
    session_http: Optional[requests.Session] = None,
) -> dict[str, Any]:
    """
    Scrape the full profile page for a single MP.

    Returns a dict with keys:
        id, name, party, riding, province, role,
        photo_url, email, website, chamber, last_scraped
    """
    if session_http is None:
        session_http = build_session()

    url = MEMBER_PROFILE_TEMPLATE.format(member_id=member_id)
    logger.debug("Scraping member profile: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch member profile %s: %s", member_id, exc)
        return {"id": member_id, "last_scraped": now_iso()}

    soup = BeautifulSoup(resp.text, "lxml")

    # --- Name ---
    name_el = soup.select_one(
        "h1.ce-mip-mp-name, h1[class*='Name'], .mp-name, h1"
    )
    name = name_el.get_text(strip=True) if name_el else ""

    # --- Party ---
    party_el = soup.select_one(
        ".ce-mip-mp-party, [class*='party-name'], [class*='PartyName']"
    )
    party = party_el.get_text(strip=True) if party_el else None

    # --- Riding / constituency ---
    riding_el = soup.select_one(
        ".ce-mip-mp-constituency, [class*='constituency'], [class*='riding']"
    )
    riding = riding_el.get_text(strip=True) if riding_el else None

    # --- Province ---
    province_el = soup.select_one(
        ".ce-mip-mp-province, [class*='province']"
    )
    province = province_el.get_text(strip=True) if province_el else None

    # --- Role ---
    role_el = soup.select_one(
        ".ce-mip-mp-role, [class*='role'], [class*='Role'], .member-role"
    )
    role = role_el.get_text(strip=True) if role_el else "Member of Parliament"

    # --- Photo ---
    photo_el = soup.select_one(
        ".ce-mip-mp-picture img, .member-photo img, img[alt*='photo']"
    )
    photo_url: Optional[str] = None
    if photo_el:
        src = photo_el.get("src", "")
        photo_url = src if src.startswith("http") else f"https://www.ourcommons.ca{src}"

    # --- Email ---
    email_el = soup.select_one("a[href^='mailto:']")
    email: Optional[str] = None
    if email_el:
        email = email_el["href"].replace("mailto:", "").strip()

    # --- Website ---
    website_el = soup.select_one("a[href^='http'][class*='web'], a[href^='http'][title*='website']")
    website: Optional[str] = None
    if website_el:
        website = website_el["href"]

    return {
        "id": member_id,
        "name": name,
        "party": party,
        "riding": riding,
        "province": province,
        "role": role,
        "photo_url": photo_url,
        "email": email,
        "website": website,
        "chamber": "commons",
        "active": True,
        "last_scraped": now_iso(),
    }


# ---------------------------------------------------------------------------
# MP vote history
# ---------------------------------------------------------------------------


def crawl_member_vote_history(
    member_id: str,
    parliament: int,
    session: int,
    session_http: Optional[requests.Session] = None,
) -> list[dict[str, Any]]:
    """
    Scrape the 'Work → Votes' tab on an MP's profile page.

    Returns a list of vote stubs:
        division_id, vote, bill_number, description, date
    """
    if session_http is None:
        session_http = build_session()

    url = MEMBER_VOTES_TEMPLATE.format(member_id=member_id)
    logger.debug("Scraping member vote history: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.warning("Failed to fetch vote history for %s: %s", member_id, exc)
        return []

    soup = BeautifulSoup(resp.text, "lxml")
    votes: list[dict[str, Any]] = []

    # The 'Work' tab vote history table
    table = soup.select_one("table.table, table#vote-history")
    if not table:
        # The page may be JS-rendered; log a warning for Playwright fallback
        logger.info(
            "Vote history table not found for member %s — page may require JS rendering",
            member_id,
        )
        return []

    for row in table.select("tbody tr"):
        cols = row.select("td")
        if len(cols) < 3:
            continue

        # Extract vote number from the link in the first cell
        link_el = row.select_one("a[href*='votes']")
        vote_number_text = re.sub(r"\D", "", cols[0].get_text(strip=True))
        if not vote_number_text:
            continue

        vote_number = int(vote_number_text)
        division_id = f"{parliament}-{session}-{vote_number}"

        date_text = cols[1].get_text(strip=True) if len(cols) > 1 else ""
        description = cols[2].get_text(strip=True) if len(cols) > 2 else ""
        vote_text = cols[3].get_text(strip=True) if len(cols) > 3 else ""

        # Normalise vote text → canonical value
        vote = _normalise_vote(vote_text)

        votes.append(
            {
                "division_id": division_id,
                "member_id": member_id,
                "vote": vote,
                "description": description,
                "date": parse_date(date_text),
            }
        )

    logger.debug("Member %s: found %d historical votes", member_id, len(votes))
    return votes


def _normalise_vote(raw: str) -> str:
    """Map raw vote text to canonical values: Yea | Nay | Paired | Abstain."""
    raw_lower = raw.strip().lower()
    if raw_lower in {"yea", "yes", "pour", "oui"}:
        return "Yea"
    if raw_lower in {"nay", "no", "contre", "non"}:
        return "Nay"
    if raw_lower in {"paired", "apparié"}:
        return "Paired"
    return "Abstain"
