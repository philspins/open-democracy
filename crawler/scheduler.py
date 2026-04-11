"""
APScheduler-based scheduler for CivicTracker.

Jobs:
- Nightly full crawl at 02:00 UTC
- Frequent vote check every 4 hours (skips if parliament not sitting)

Run with:
    python -m crawler.scheduler
"""

from __future__ import annotations

import logging
from datetime import datetime

from apscheduler.schedulers.blocking import BlockingScheduler

from crawler.main import run_frequent_vote_check, run_full_crawl

logger = logging.getLogger(__name__)

scheduler = BlockingScheduler(timezone="UTC")


@scheduler.scheduled_job("cron", hour=2, minute=0, id="nightly_full_crawl")
def nightly_full_crawl() -> None:
    """Full crawl of all data sources — runs at 02:00 UTC nightly."""
    logger.info("[%s] Starting nightly full crawl", datetime.utcnow().isoformat())
    try:
        run_full_crawl()
    except Exception:
        logger.exception("Nightly crawl failed")


@scheduler.scheduled_job("cron", hour="*/4", id="frequent_vote_check")
def frequent_vote_check() -> None:
    """
    Check for new Commons divisions every 4 hours.
    Skips automatically if parliament is not sitting today.
    """
    logger.info("[%s] Starting frequent vote check", datetime.utcnow().isoformat())
    try:
        run_frequent_vote_check()
    except Exception:
        logger.exception("Frequent vote check failed")


def main() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    logger.info("Starting CivicTracker scheduler (UTC)...")
    logger.info("  nightly_full_crawl : daily at 02:00 UTC")
    logger.info("  frequent_vote_check: every 4 hours")
    scheduler.start()


if __name__ == "__main__":
    main()
