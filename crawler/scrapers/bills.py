"""
Bills scraper for CivicTracker.

Scrapes:
1. LEGISinfo RSS feed  → list of all active bills
2. LEGISinfo bill detail page → stage timeline, sponsor, status
3. LEGISinfo bill full-text page → URL for the AI summariser
4. Library of Parliament legislative summaries (LoP) → plain-English summaries
"""

from __future__ import annotations

import re
import logging
from typing import Any, Optional

import feedparser
import requests
from bs4 import BeautifulSoup

from crawler.utils import (
    build_session,
    extract_bill_id,
    extract_member_id,
    parse_date,
    polite_get,
    now_iso,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

RSS_URL = "https://www.parl.ca/legisinfo/en/bills/rss"

LEGISINFO_BASE = "https://www.parl.ca/legisinfo/en/bill"
LEGISINFO_FULL_TEXT_TEMPLATE = (
    "https://www.parl.ca/DocumentViewer/en/{parliament}-{session}/bill/{bill_id}/first-reading"
)
LOP_BASE = "https://lop.parl.ca/sites/PublicWebsite/default/en_CA/ResearchPublications/LegislativeSummaries"

# Canonical stage labels — ordered from first to last
STAGE_ORDER = [
    "1st_reading",
    "2nd_reading",
    "committee",
    "3rd_reading",
    "royal_assent",
]

# Mapping of page-anchor substrings → our canonical stage keys
_STAGE_ANCHOR_MAP = {
    "firstreading": "1st_reading",
    "1st-reading": "1st_reading",
    "first-reading": "1st_reading",
    "secondreading": "2nd_reading",
    "2nd-reading": "2nd_reading",
    "second-reading": "2nd_reading",
    "committee": "committee",
    "thirdreading": "3rd_reading",
    "3rd-reading": "3rd_reading",
    "third-reading": "3rd_reading",
    "royalassent": "royal_assent",
    "royal-assent": "royal_assent",
}

# Mapping of bill-number prefixes to chamber
_BILL_CHAMBER_MAP = {
    "C": "commons",
    "S": "senate",
}


def _bill_chamber(bill_number: str) -> str:
    """Determine chamber ('commons' or 'senate') from a bill number like 'C-47'."""
    if bill_number:
        prefix = bill_number.split("-")[0].upper()
        return _BILL_CHAMBER_MAP.get(prefix, "commons")
    return "commons"


# ---------------------------------------------------------------------------
# RSS feed
# ---------------------------------------------------------------------------


def crawl_bills_rss(
    rss_url: str = RSS_URL,
) -> list[dict[str, Any]]:
    """
    Fetch the LEGISinfo RSS feed and return a list of bill stubs.

    Each stub contains:
        id, title, legisinfo_url, last_activity_date
    """
    logger.info("Fetching bills RSS: %s", rss_url)
    feed = feedparser.parse(rss_url)

    bills: list[dict[str, Any]] = []
    for entry in feed.entries:
        link = entry.get("link", "")
        bill_id = extract_bill_id(link)
        if not bill_id:
            logger.debug("Could not extract bill ID from link: %s", link)
            continue

        updated = entry.get("updated", None)
        last_activity = parse_date(updated) if updated else None

        bills.append(
            {
                "id": bill_id,
                "title": entry.get("title", "").strip(),
                "legisinfo_url": link,
                "last_activity_date": last_activity,
            }
        )

    logger.info("RSS feed contained %d bills", len(bills))
    return bills


# ---------------------------------------------------------------------------
# Bill detail page
# ---------------------------------------------------------------------------


def crawl_bill_detail(
    bill_id: str,
    url: str,
    session: Optional[requests.Session] = None,
) -> dict[str, Any]:
    """
    Scrape the LEGISinfo detail page for a single bill.

    Returns a dict with keys:
        current_status, current_stage, stages, sponsor_id,
        bill_type, full_text_url, introduced_date
    """
    if session is None:
        session = build_session()

    logger.debug("Scraping bill detail: %s", url)
    try:
        resp = polite_get(session, url)
    except Exception as exc:
        logger.warning("Failed to fetch bill detail %s: %s", url, exc)
        return {}

    soup = BeautifulSoup(resp.text, "lxml")

    # --- Current status ---
    status_el = soup.select_one(
        ".bill-latest-activity, .bill-progress-current, [class*='latestActivity']"
    )
    current_status = status_el.get_text(strip=True) if status_el else None

    # --- Stage timeline ---
    stages: list[dict[str, Any]] = []
    current_stage: Optional[str] = None

    for anchor_id, canonical in _STAGE_ANCHOR_MAP.items():
        # LEGISinfo marks completed stages with a date alongside the heading
        heading_el = soup.find(
            attrs={"id": re.compile(anchor_id, re.IGNORECASE)}
        ) or soup.find(
            "h2", string=re.compile(anchor_id.replace("-", " "), re.IGNORECASE)
        )
        if heading_el is None:
            continue

        # Try to find a date near this heading
        date_text = _extract_nearby_date(heading_el)
        stages.append(
            {
                "stage": canonical,
                "date": date_text,
                "chamber": _bill_chamber(bill_id.split("-")[-1] if bill_id else ""),
            }
        )
        current_stage = canonical  # last stage found is the furthest-along stage

    # Sort stages by canonical order
    stages.sort(key=lambda s: STAGE_ORDER.index(s["stage"]) if s["stage"] in STAGE_ORDER else 99)
    if stages:
        current_stage = stages[-1]["stage"]

    # --- Sponsor ---
    sponsor_el = soup.select_one(
        ".bill-profile-sponsor a, [class*='sponsor'] a, [class*='Sponsor'] a"
    )
    sponsor_url = sponsor_el["href"] if sponsor_el else None
    sponsor_id = extract_member_id(sponsor_url) if sponsor_url else None

    # --- Bill type ---
    bill_type_el = soup.select_one(
        ".bill-type, [class*='billType'], [class*='BillType']"
    )
    bill_type = bill_type_el.get_text(strip=True) if bill_type_el else None

    # --- Introduced date (first reading) ---
    introduced_date: Optional[str] = None
    first_reading_stages = [s for s in stages if s["stage"] == "1st_reading"]
    if first_reading_stages and first_reading_stages[0].get("date"):
        introduced_date = first_reading_stages[0]["date"]

    # --- Full text URL ---
    # bill_id format: "{parliament}-{session}-{bill_number}" e.g. "45-1-c-47"
    # Split on the first two dashes only so "c-47" stays intact.
    parts = bill_id.split("-", 2)
    full_text_url: Optional[str] = None
    if len(parts) == 3:
        parl, sess = parts[0], parts[1]
        bill_num = parts[2].upper()  # "c-47" → "C-47"
        full_text_url = (
            f"https://www.parl.ca/DocumentViewer/en/{parl}-{sess}/bill/{bill_num}/first-reading"
        )

    return {
        "current_status": current_status,
        "current_stage": current_stage,
        "stages": stages,
        "sponsor_id": sponsor_id,
        "bill_type": bill_type,
        "full_text_url": full_text_url,
        "introduced_date": introduced_date,
        "last_scraped": now_iso(),
    }


def _extract_nearby_date(el: Any) -> Optional[str]:
    """
    Look for a date in the text of *el* or its immediate siblings / parent.
    Returns an ISO-8601 date string or None.
    """
    # Check the element's own text
    text = el.get_text(" ", strip=True)
    found = _find_date_in_text(text)
    if found:
        return found

    # Check next sibling
    sibling = el.find_next_sibling()
    if sibling:
        found = _find_date_in_text(sibling.get_text(" ", strip=True))
        if found:
            return found

    # Check parent's text
    if el.parent:
        found = _find_date_in_text(el.parent.get_text(" ", strip=True))
        if found:
            return found

    return None


_DATE_IN_TEXT_RE = re.compile(
    r"\b(\d{4}-\d{2}-\d{2})"                         # ISO
    r"|\b([A-Z][a-z]+ \d{1,2},? \d{4})\b"            # "April 3, 2024"
    r"|\b(\d{1,2} [A-Z][a-z]+ \d{4})\b"              # "3 April 2024"
)


def _find_date_in_text(text: str) -> Optional[str]:
    m = _DATE_IN_TEXT_RE.search(text)
    if m:
        raw = next(g for g in m.groups() if g)
        return parse_date(raw)
    return None


# ---------------------------------------------------------------------------
# Library of Parliament summary
# ---------------------------------------------------------------------------


def crawl_lop_summary(
    bill_number: str,
    parliament: int,
    session: int,
    session_http: Optional[requests.Session] = None,
) -> Optional[str]:
    """
    Attempt to fetch a plain-English summary from the Library of Parliament.

    Returns the summary text or None if not found.
    """
    if session_http is None:
        session_http = build_session()

    # LoP URL structure: /LegislativeSummaries?ls=C47&Parl=45&Ses=1
    number_slug = bill_number.replace("-", "").replace(" ", "")
    url = (
        f"{LOP_BASE}?ls={number_slug}&Parl={parliament}&Ses={session}"
    )
    logger.debug("Fetching LoP summary: %s", url)
    try:
        resp = polite_get(session_http, url)
    except Exception as exc:
        logger.debug("LoP summary not available for %s: %s", bill_number, exc)
        return None

    soup = BeautifulSoup(resp.text, "lxml")

    # The summary is typically in a <div class="field-item"> or similar
    content_el = soup.select_one(
        ".views-field-body .field-content, .field-item, article .field--type-text-with-summary"
    )
    if content_el:
        paragraphs = content_el.find_all("p")
        if paragraphs:
            return " ".join(p.get_text(" ", strip=True) for p in paragraphs[:3])
        text = content_el.get_text(" ", strip=True)
        if text:
            return text

    return None
