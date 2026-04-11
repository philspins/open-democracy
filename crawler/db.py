"""
Database layer for CivicTracker.

Handles schema initialisation and upsert helpers for SQLite.
Designed so the connection string can be swapped to Postgres later by
replacing the sqlite3 calls with psycopg2 / asyncpg equivalents.
"""

from __future__ import annotations

import sqlite3
from pathlib import Path
from typing import Any

# Default DB path — override via DB_PATH environment variable or pass explicitly
DEFAULT_DB_PATH = Path(__file__).parent.parent / "civictracker.db"

# ---------------------------------------------------------------------------
# DDL
# ---------------------------------------------------------------------------

_CREATE_MEMBERS = """
CREATE TABLE IF NOT EXISTS members (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    party        TEXT,
    riding       TEXT,
    province     TEXT,
    role         TEXT,
    photo_url    TEXT,
    email        TEXT,
    website      TEXT,
    chamber      TEXT DEFAULT 'commons',
    active       INTEGER DEFAULT 1,
    last_scraped TIMESTAMP
);
"""

_CREATE_BILLS = """
CREATE TABLE IF NOT EXISTS bills (
    id                  TEXT PRIMARY KEY,
    parliament          INTEGER,
    session             INTEGER,
    number              TEXT,
    title               TEXT,
    short_title         TEXT,
    bill_type           TEXT,
    chamber             TEXT,
    sponsor_id          TEXT REFERENCES members(id),
    current_stage       TEXT,
    current_status      TEXT,
    category            TEXT,
    summary_ai          TEXT,
    summary_lop         TEXT,
    full_text_url       TEXT,
    legisinfo_url       TEXT,
    introduced_date     DATE,
    last_activity_date  DATE,
    last_scraped        TIMESTAMP
);
"""

_CREATE_DIVISIONS = """
CREATE TABLE IF NOT EXISTS divisions (
    id           TEXT PRIMARY KEY,
    parliament   INTEGER,
    session      INTEGER,
    number       INTEGER,
    date         DATE,
    bill_id      TEXT REFERENCES bills(id),
    description  TEXT,
    yeas         INTEGER,
    nays         INTEGER,
    paired       INTEGER DEFAULT 0,
    result       TEXT,
    chamber      TEXT DEFAULT 'commons',
    sitting_url  TEXT,
    last_scraped TIMESTAMP
);
"""

_CREATE_MEMBER_VOTES = """
CREATE TABLE IF NOT EXISTS member_votes (
    division_id TEXT REFERENCES divisions(id),
    member_id   TEXT REFERENCES members(id),
    vote        TEXT,
    PRIMARY KEY (division_id, member_id)
);
"""

_CREATE_BILL_STAGES = """
CREATE TABLE IF NOT EXISTS bill_stages (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_id TEXT REFERENCES bills(id),
    stage   TEXT,
    chamber TEXT,
    date    DATE,
    notes   TEXT
);
"""

_CREATE_SITTING_CALENDAR = """
CREATE TABLE IF NOT EXISTS sitting_calendar (
    parliament INTEGER,
    session    INTEGER,
    date       DATE,
    PRIMARY KEY (parliament, session, date)
);
"""

_INDICES = [
    "CREATE INDEX IF NOT EXISTS idx_divisions_bill    ON divisions(bill_id);",
    "CREATE INDEX IF NOT EXISTS idx_member_votes_member ON member_votes(member_id);",
    "CREATE INDEX IF NOT EXISTS idx_bills_stage        ON bills(current_stage);",
    "CREATE INDEX IF NOT EXISTS idx_bills_category     ON bills(category);",
    "CREATE INDEX IF NOT EXISTS idx_bill_stages_bill   ON bill_stages(bill_id);",
]

_SCHEMA_STATEMENTS = [
    _CREATE_MEMBERS,
    _CREATE_BILLS,
    _CREATE_DIVISIONS,
    _CREATE_MEMBER_VOTES,
    _CREATE_BILL_STAGES,
    _CREATE_SITTING_CALENDAR,
    *_INDICES,
]

# ---------------------------------------------------------------------------
# Connection helpers
# ---------------------------------------------------------------------------


def get_connection(db_path: str | Path = DEFAULT_DB_PATH) -> sqlite3.Connection:
    """Return an open SQLite connection with foreign-key enforcement enabled."""
    conn = sqlite3.connect(str(db_path))
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON;")
    conn.execute("PRAGMA journal_mode = WAL;")
    return conn


def init_db(db_path: str | Path = DEFAULT_DB_PATH) -> sqlite3.Connection:
    """Create all tables and indices if they don't already exist."""
    conn = get_connection(db_path)
    with conn:
        for stmt in _SCHEMA_STATEMENTS:
            conn.execute(stmt)
    return conn


# ---------------------------------------------------------------------------
# Upsert helpers
# ---------------------------------------------------------------------------


def upsert_member(conn: sqlite3.Connection, member: dict[str, Any]) -> None:
    """Insert or replace a member record."""
    conn.execute(
        """
        INSERT INTO members
            (id, name, party, riding, province, role, photo_url, email, website,
             chamber, active, last_scraped)
        VALUES
            (:id, :name, :party, :riding, :province, :role, :photo_url, :email,
             :website, :chamber, :active, :last_scraped)
        ON CONFLICT(id) DO UPDATE SET
            name         = excluded.name,
            party        = excluded.party,
            riding       = excluded.riding,
            province     = excluded.province,
            role         = excluded.role,
            photo_url    = excluded.photo_url,
            email        = excluded.email,
            website      = excluded.website,
            chamber      = excluded.chamber,
            active       = excluded.active,
            last_scraped = excluded.last_scraped
        """,
        {
            "id": member["id"],
            "name": member.get("name", ""),
            "party": member.get("party"),
            "riding": member.get("riding"),
            "province": member.get("province"),
            "role": member.get("role"),
            "photo_url": member.get("photo_url"),
            "email": member.get("email"),
            "website": member.get("website"),
            "chamber": member.get("chamber", "commons"),
            "active": int(member.get("active", True)),
            "last_scraped": member.get("last_scraped"),
        },
    )


def upsert_bill(conn: sqlite3.Connection, bill: dict[str, Any]) -> None:
    """Insert or replace a bill record."""
    conn.execute(
        """
        INSERT INTO bills
            (id, parliament, session, number, title, short_title, bill_type,
             chamber, sponsor_id, current_stage, current_status, category,
             summary_ai, summary_lop, full_text_url, legisinfo_url,
             introduced_date, last_activity_date, last_scraped)
        VALUES
            (:id, :parliament, :session, :number, :title, :short_title,
             :bill_type, :chamber, :sponsor_id, :current_stage, :current_status,
             :category, :summary_ai, :summary_lop, :full_text_url, :legisinfo_url,
             :introduced_date, :last_activity_date, :last_scraped)
        ON CONFLICT(id) DO UPDATE SET
            parliament         = excluded.parliament,
            session            = excluded.session,
            number             = excluded.number,
            title              = excluded.title,
            short_title        = excluded.short_title,
            bill_type          = excluded.bill_type,
            chamber            = excluded.chamber,
            sponsor_id         = excluded.sponsor_id,
            current_stage      = excluded.current_stage,
            current_status     = excluded.current_status,
            category           = excluded.category,
            summary_ai         = COALESCE(excluded.summary_ai, bills.summary_ai),
            summary_lop        = COALESCE(excluded.summary_lop, bills.summary_lop),
            full_text_url      = excluded.full_text_url,
            legisinfo_url      = excluded.legisinfo_url,
            introduced_date    = excluded.introduced_date,
            last_activity_date = excluded.last_activity_date,
            last_scraped       = excluded.last_scraped
        """,
        {
            "id": bill["id"],
            "parliament": bill.get("parliament"),
            "session": bill.get("session"),
            "number": bill.get("number"),
            "title": bill.get("title", ""),
            "short_title": bill.get("short_title"),
            "bill_type": bill.get("bill_type"),
            "chamber": bill.get("chamber"),
            "sponsor_id": bill.get("sponsor_id"),
            "current_stage": bill.get("current_stage"),
            "current_status": bill.get("current_status"),
            "category": bill.get("category"),
            "summary_ai": bill.get("summary_ai"),
            "summary_lop": bill.get("summary_lop"),
            "full_text_url": bill.get("full_text_url"),
            "legisinfo_url": bill.get("legisinfo_url"),
            "introduced_date": bill.get("introduced_date"),
            "last_activity_date": bill.get("last_activity_date"),
            "last_scraped": bill.get("last_scraped"),
        },
    )


def upsert_division(conn: sqlite3.Connection, division: dict[str, Any]) -> None:
    """Insert or replace a division (recorded vote) record."""
    conn.execute(
        """
        INSERT INTO divisions
            (id, parliament, session, number, date, bill_id, description,
             yeas, nays, paired, result, chamber, sitting_url, last_scraped)
        VALUES
            (:id, :parliament, :session, :number, :date, :bill_id, :description,
             :yeas, :nays, :paired, :result, :chamber, :sitting_url, :last_scraped)
        ON CONFLICT(id) DO UPDATE SET
            parliament   = excluded.parliament,
            session      = excluded.session,
            number       = excluded.number,
            date         = excluded.date,
            bill_id      = excluded.bill_id,
            description  = excluded.description,
            yeas         = excluded.yeas,
            nays         = excluded.nays,
            paired       = excluded.paired,
            result       = excluded.result,
            chamber      = excluded.chamber,
            sitting_url  = excluded.sitting_url,
            last_scraped = excluded.last_scraped
        """,
        {
            "id": division["id"],
            "parliament": division.get("parliament"),
            "session": division.get("session"),
            "number": division.get("number"),
            "date": division.get("date"),
            "bill_id": division.get("bill_id"),
            "description": division.get("description"),
            "yeas": division.get("yeas", 0),
            "nays": division.get("nays", 0),
            "paired": division.get("paired", 0),
            "result": division.get("result"),
            "chamber": division.get("chamber", "commons"),
            "sitting_url": division.get("sitting_url"),
            "last_scraped": division.get("last_scraped"),
        },
    )


def upsert_member_vote(
    conn: sqlite3.Connection, division_id: str, member_id: str, vote: str
) -> None:
    """Insert or replace a single MP's vote on a division."""
    conn.execute(
        """
        INSERT INTO member_votes (division_id, member_id, vote)
        VALUES (?, ?, ?)
        ON CONFLICT(division_id, member_id) DO UPDATE SET vote = excluded.vote
        """,
        (division_id, member_id, vote),
    )


def upsert_bill_stage(conn: sqlite3.Connection, stage: dict[str, Any]) -> None:
    """Insert a bill stage record (idempotent by bill_id + stage + date)."""
    conn.execute(
        """
        INSERT OR IGNORE INTO bill_stages (bill_id, stage, chamber, date, notes)
        VALUES (:bill_id, :stage, :chamber, :date, :notes)
        """,
        {
            "bill_id": stage["bill_id"],
            "stage": stage.get("stage"),
            "chamber": stage.get("chamber"),
            "date": stage.get("date"),
            "notes": stage.get("notes"),
        },
    )


def upsert_sitting_date(
    conn: sqlite3.Connection, parliament: int, session: int, date: str
) -> None:
    """Insert a sitting calendar date (idempotent)."""
    conn.execute(
        """
        INSERT OR IGNORE INTO sitting_calendar (parliament, session, date)
        VALUES (?, ?, ?)
        """,
        (parliament, session, date),
    )


def division_exists(conn: sqlite3.Connection, division_id: str) -> bool:
    """Return True if a division record already exists in the database."""
    row = conn.execute(
        "SELECT 1 FROM divisions WHERE id = ?", (division_id,)
    ).fetchone()
    return row is not None
