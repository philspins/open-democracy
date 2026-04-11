"""
CivicTracker crawler — entry point.

Usage:
    python -m crawler.main                 # run all crawlers once
    python -m crawler.main --bills         # bills RSS + detail only
    python -m crawler.main --votes         # Commons votes only
    python -m crawler.main --members       # MP profiles only
    python -m crawler.main --senate        # Senate votes only
    python -m crawler.main --calendar      # Sitting calendar only

Run with APScheduler for scheduled nightly execution:
    python -m crawler.scheduler
"""

from __future__ import annotations

import argparse
import logging
import time
from pathlib import Path
from typing import Optional

from crawler.db import (
    division_exists,
    init_db,
    upsert_bill,
    upsert_bill_stage,
    upsert_division,
    upsert_member,
    upsert_member_vote,
    upsert_sitting_date,
)
from crawler.scrapers.bills import (
    crawl_bill_detail,
    crawl_bills_rss,
    crawl_lop_summary,
)
from crawler.scrapers.votes import (
    CURRENT_PARLIAMENT,
    CURRENT_SESSION,
    crawl_division_detail,
    crawl_sitting_calendar,
    crawl_votes_index,
    parliament_is_sitting,
)
from crawler.scrapers.members import (
    crawl_member_profile,
    crawl_members_list,
)
from crawler.scrapers.senate import (
    crawl_senate_division_detail,
    crawl_senate_votes_index,
)
from crawler.utils import (
    build_session,
    extract_bill_number_from_id,
    extract_parliament_session_from_bill_id,
    now_iso,
)

# Re-export for convenience (used by scheduler)

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Per-domain crawl functions
# ---------------------------------------------------------------------------


def crawl_all_bills(conn, session_http=None, delay: float = 0.5) -> None:
    """Fetch all bills from RSS and then enrich each with its detail page."""
    if session_http is None:
        session_http = build_session()

    bills = crawl_bills_rss()
    logger.info("Processing %d bills from RSS", len(bills))

    with conn:
        for bill in bills:
            bill_id = bill["id"]
            parl, sess = extract_parliament_session_from_bill_id(bill_id)
            bill_number = extract_bill_number_from_id(bill_id)

            # Fetch detail page
            detail = crawl_bill_detail(
                bill_id, bill["legisinfo_url"], session_http=session_http
            )

            # Try LoP summary (preferred over AI-generated)
            lop_summary: Optional[str] = None
            if parl and sess and bill_number:
                lop_summary = crawl_lop_summary(
                    bill_number, parl, sess, session_http=session_http
                )

            record = {
                "id": bill_id,
                "parliament": parl,
                "session": sess,
                "number": bill_number,
                "title": bill.get("title", ""),
                "legisinfo_url": bill.get("legisinfo_url"),
                "last_activity_date": bill.get("last_activity_date"),
                "current_stage": detail.get("current_stage"),
                "current_status": detail.get("current_status"),
                "sponsor_id": detail.get("sponsor_id"),
                "bill_type": detail.get("bill_type"),
                "full_text_url": detail.get("full_text_url"),
                "introduced_date": detail.get("introduced_date"),
                "summary_lop": lop_summary,
                "last_scraped": now_iso(),
            }
            upsert_bill(conn, record)

            # Persist stage timeline
            for stage in detail.get("stages", []):
                upsert_bill_stage(
                    conn,
                    {
                        "bill_id": bill_id,
                        "stage": stage.get("stage"),
                        "chamber": stage.get("chamber"),
                        "date": stage.get("date"),
                        "notes": None,
                    },
                )

            time.sleep(delay)

    logger.info("Bill crawl complete")


def crawl_all_votes(conn, session_http=None, delay: float = 0.5) -> None:
    """Fetch the Commons votes index, then fill in any new division details."""
    if session_http is None:
        session_http = build_session()

    divisions = crawl_votes_index(session_http=session_http)

    with conn:
        for div in divisions:
            division_id = div["id"]
            is_new = not division_exists(conn, division_id)
            upsert_division(conn, div)

            # Only fetch per-MP detail for divisions we haven't processed before
            if is_new and div.get("detail_url"):
                member_votes = crawl_division_detail(
                    division_id, div["detail_url"], session_http=session_http
                )
                for mv in member_votes:
                    upsert_member_vote(conn, mv["division_id"], mv["member_id"], mv["vote"])
                time.sleep(delay)

    logger.info("Commons votes crawl complete")


def crawl_all_senate_votes(conn, session_http=None, delay: float = 0.5) -> None:
    """Fetch Senate votes index and then per-division detail for new divisions."""
    if session_http is None:
        session_http = build_session()

    divisions = crawl_senate_votes_index(session_http=session_http)

    with conn:
        for div in divisions:
            division_id = div["id"]
            is_new = not division_exists(conn, division_id)
            upsert_division(conn, div)

            if is_new and div.get("detail_url"):
                member_votes = crawl_senate_division_detail(
                    division_id, div["detail_url"], session_http=session_http
                )
                for mv in member_votes:
                    upsert_member_vote(conn, mv["division_id"], mv["member_id"], mv["vote"])
                time.sleep(delay)

    logger.info("Senate votes crawl complete")


def crawl_all_members(conn, session_http=None, delay: float = 0.5) -> None:
    """Fetch the MP list and then enrich each profile."""
    if session_http is None:
        session_http = build_session()

    stubs = crawl_members_list(session_http=session_http)
    logger.info("Processing %d MP stubs", len(stubs))

    with conn:
        for stub in stubs:
            profile = crawl_member_profile(stub["id"], session_http=session_http)
            # Merge stub data as fallback for fields not on the detail page
            for key in ("party", "riding", "province"):
                if not profile.get(key) and stub.get(key):
                    profile[key] = stub[key]
            upsert_member(conn, profile)
            time.sleep(delay)

    logger.info("Members crawl complete")


def crawl_calendar(conn, session_http=None) -> list[str]:
    """Fetch the sitting calendar and store all dates in the DB."""
    if session_http is None:
        session_http = build_session()

    sitting_dates = crawl_sitting_calendar(session_http=session_http)

    with conn:
        for d in sitting_dates:
            upsert_sitting_date(conn, CURRENT_PARLIAMENT, CURRENT_SESSION, d)

    logger.info("Sitting calendar crawl complete (%d dates)", len(sitting_dates))
    return sitting_dates


# ---------------------------------------------------------------------------
# Full crawl
# ---------------------------------------------------------------------------


def run_full_crawl(db_path=None, delay: float = 0.5) -> None:
    """Run all crawlers in sequence — intended for nightly scheduled runs."""
    from crawler.db import DEFAULT_DB_PATH

    db_path = db_path or DEFAULT_DB_PATH
    conn = init_db(db_path)
    session_http = build_session()

    logger.info("=== CivicTracker nightly crawl starting ===")
    crawl_calendar(conn, session_http)
    crawl_all_bills(conn, session_http, delay=delay)
    crawl_all_members(conn, session_http, delay=delay)
    crawl_all_votes(conn, session_http, delay=delay)
    crawl_all_senate_votes(conn, session_http, delay=delay)
    logger.info("=== CivicTracker nightly crawl finished ===")


def run_frequent_vote_check(db_path=None, delay: float = 0.5) -> None:
    """
    Check for new Commons divisions — run every 4 hours on sitting days.
    Only fetches the votes index; skips if parliament is not sitting.
    """
    from crawler.db import DEFAULT_DB_PATH

    db_path = db_path or DEFAULT_DB_PATH
    conn = init_db(db_path)
    session_http = build_session()

    # Fetch current sitting dates from DB
    rows = conn.execute(
        "SELECT date FROM sitting_calendar WHERE parliament = ? AND session = ?",
        (CURRENT_PARLIAMENT, CURRENT_SESSION),
    ).fetchall()
    sitting_dates = [row["date"] for row in rows]

    if not parliament_is_sitting(sitting_dates):
        logger.info("Parliament is not sitting today — skipping frequent vote check")
        return

    logger.info("Parliament is sitting — running frequent vote check")
    crawl_all_votes(conn, session_http, delay=delay)


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------


def _configure_logging(verbose: bool = False) -> None:
    level = logging.DEBUG if verbose else logging.INFO
    logging.basicConfig(
        level=level,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(
        description="CivicTracker data crawler",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--bills", action="store_true", help="Crawl bills only")
    parser.add_argument("--votes", action="store_true", help="Crawl Commons votes only")
    parser.add_argument("--senate", action="store_true", help="Crawl Senate votes only")
    parser.add_argument("--members", action="store_true", help="Crawl MP profiles only")
    parser.add_argument("--calendar", action="store_true", help="Crawl sitting calendar only")
    parser.add_argument("--db", metavar="PATH", help="Path to SQLite database file")
    parser.add_argument("--delay", type=float, default=0.5, metavar="SECS",
                        help="Seconds between requests (default: 0.5)")
    parser.add_argument("-v", "--verbose", action="store_true", help="Verbose logging")

    args = parser.parse_args(argv)
    _configure_logging(args.verbose)

    from crawler.db import DEFAULT_DB_PATH

    db_path = Path(args.db) if args.db else DEFAULT_DB_PATH
    conn = init_db(db_path)
    session_http = build_session()

    # If no specific flag, run everything
    run_all = not any([args.bills, args.votes, args.senate, args.members, args.calendar])

    if args.calendar or run_all:
        crawl_calendar(conn, session_http)
    if args.bills or run_all:
        crawl_all_bills(conn, session_http, delay=args.delay)
    if args.members or run_all:
        crawl_all_members(conn, session_http, delay=args.delay)
    if args.votes or run_all:
        crawl_all_votes(conn, session_http, delay=args.delay)
    if args.senate or run_all:
        crawl_all_senate_votes(conn, session_http, delay=args.delay)

    logger.info("Done.")


if __name__ == "__main__":
    main()
