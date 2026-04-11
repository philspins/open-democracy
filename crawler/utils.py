"""
Shared utilities for the CivicTracker crawler.

Provides:
- HTTP session factory with rate-limiting, caching, and a polite User-Agent
- URL / ID extraction helpers
- Date parsing helpers
"""

from __future__ import annotations

import re
import time
from datetime import datetime, date, timezone
from typing import Optional
from urllib.parse import urlparse, parse_qs

import requests
import requests_cache

# ---------------------------------------------------------------------------
# HTTP helpers
# ---------------------------------------------------------------------------

APP_USER_AGENT = (
    "CivicTracker/1.0 (civic-app.ca; contact@civic-app.ca)"
)

HEADERS = {"User-Agent": APP_USER_AGENT}

# Cache detail pages for 6 hours — bills / votes don't change every minute
_CACHE_TTL_SECONDS = 6 * 60 * 60

# Seconds to sleep between outbound HTTP requests (politeness)
DEFAULT_REQUEST_DELAY = 0.5


def build_session(cache_name: str = "civictracker_cache") -> requests.Session:
    """
    Return a requests.Session (or CachedSession) with:
    - Our User-Agent header pre-set
    - A 6-hour SQLite-backed cache to avoid hammering government servers
    """
    session = requests_cache.CachedSession(
        cache_name=cache_name,
        expire_after=_CACHE_TTL_SECONDS,
        backend="sqlite",
    )
    session.headers.update(HEADERS)
    return session


def polite_get(
    session: requests.Session,
    url: str,
    delay: float = DEFAULT_REQUEST_DELAY,
    timeout: int = 15,
) -> requests.Response:
    """
    Perform a GET request and then sleep for *delay* seconds.

    The sleep happens **after** the request (and cache miss) so cached
    responses don't incur any delay.
    """
    resp = session.get(url, timeout=timeout)
    resp.raise_for_status()
    # Only sleep when we actually hit the network (cache miss)
    if not getattr(resp, "from_cache", False):
        time.sleep(delay)
    return resp


# ---------------------------------------------------------------------------
# URL / ID extraction helpers
# ---------------------------------------------------------------------------

_LEGISINFO_BILL_RE = re.compile(
    r"/legisinfo/en/bill/(\d+)-(\d+)/([A-Za-z]+-?\d+)", re.IGNORECASE
)

_OURCOMMONS_MEMBER_RE = re.compile(
    r"/Members/en/(\d+)", re.IGNORECASE
)

_OURCOMMONS_VOTE_RE = re.compile(
    r"/Members/en/votes/(\d+)", re.IGNORECASE
)


def extract_bill_id(url: str) -> Optional[str]:
    """
    Extract a canonical bill ID from a LEGISinfo URL.

    Example:
        https://www.parl.ca/legisinfo/en/bill/45-1/c-47
        → "45-1-c-47"
    """
    m = _LEGISINFO_BILL_RE.search(url)
    if m:
        parliament, session, bill_number = m.groups()
        return f"{parliament}-{session}-{bill_number.lower()}"
    return None


def extract_parliament_session_from_bill_id(
    bill_id: str,
) -> tuple[Optional[int], Optional[int]]:
    """Return (parliament, session) parsed from a bill ID like '45-1-c-47'."""
    parts = bill_id.split("-")
    try:
        return int(parts[0]), int(parts[1])
    except (IndexError, ValueError):
        return None, None


def extract_bill_number_from_id(bill_id: str) -> Optional[str]:
    """Return the bill number portion of a bill ID, e.g. 'c-47' → 'C-47'."""
    parts = bill_id.split("-", 2)
    if len(parts) == 3:
        return parts[2].upper()
    return None


def extract_member_id(url: str) -> Optional[str]:
    """
    Extract a member ID from an ourcommons.ca member URL.

    Example:
        https://www.ourcommons.ca/Members/en/123006
        → "123006"
    """
    m = _OURCOMMONS_MEMBER_RE.search(url)
    return m.group(1) if m else None


def extract_division_id(parliament: int, session: int, number: int) -> str:
    """Build a canonical division ID from its components."""
    return f"{parliament}-{session}-{number}"


def extract_vote_number_from_url(url: str) -> Optional[str]:
    """Extract the vote number from an ourcommons vote URL."""
    m = _OURCOMMONS_VOTE_RE.search(url)
    return m.group(1) if m else None


# ---------------------------------------------------------------------------
# Date helpers
# ---------------------------------------------------------------------------

_DATE_FORMATS = [
    "%Y-%m-%d",
    "%B %d, %Y",   # e.g. "April 3, 2024"
    "%d %B %Y",    # e.g. "3 April 2024"
    "%b %d, %Y",   # e.g. "Apr 3, 2024"
    "%Y/%m/%d",
]


def parse_date(text: str) -> Optional[str]:
    """
    Try to parse a date string into ISO-8601 format (YYYY-MM-DD).
    Returns None if no known format matches.
    """
    if not text:
        return None
    text = text.strip()
    for fmt in _DATE_FORMATS:
        try:
            return datetime.strptime(text, fmt).strftime("%Y-%m-%d")
        except ValueError:
            continue
    return None


def today_iso() -> str:
    """Return today's date as an ISO-8601 string."""
    return date.today().isoformat()


def now_iso() -> str:
    """Return the current UTC datetime as an ISO-8601 string."""
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "")
