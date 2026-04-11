"""
Tests for crawler/db.py
"""

import sqlite3
from pathlib import Path

import pytest

from crawler.db import (
    init_db,
    upsert_bill,
    upsert_division,
    upsert_member,
    upsert_member_vote,
    upsert_bill_stage,
    upsert_sitting_date,
    division_exists,
)


@pytest.fixture
def db(tmp_path):
    """Return an initialised in-memory-like SQLite connection in a temp file."""
    conn = init_db(tmp_path / "test.db")
    yield conn
    conn.close()


class TestInitDb:
    def test_creates_tables(self, db):
        tables = {
            row[0]
            for row in db.execute(
                "SELECT name FROM sqlite_master WHERE type='table'"
            ).fetchall()
        }
        assert "members" in tables
        assert "bills" in tables
        assert "divisions" in tables
        assert "member_votes" in tables
        assert "bill_stages" in tables
        assert "sitting_calendar" in tables

    def test_creates_indices(self, db):
        indices = {
            row[0]
            for row in db.execute(
                "SELECT name FROM sqlite_master WHERE type='index'"
            ).fetchall()
        }
        assert "idx_divisions_bill" in indices
        assert "idx_member_votes_member" in indices
        assert "idx_bills_stage" in indices
        assert "idx_bills_category" in indices


class TestUpsertMember:
    def test_insert_new_member(self, db):
        upsert_member(
            db,
            {
                "id": "123006",
                "name": "Jane Doe",
                "party": "Liberal",
                "riding": "Ottawa Centre",
                "province": "Ontario",
                "role": "Member of Parliament",
                "photo_url": None,
                "email": "jane.doe@parl.gc.ca",
                "website": None,
                "chamber": "commons",
                "active": True,
                "last_scraped": "2024-04-03T00:00:00",
            },
        )
        db.commit()
        row = db.execute("SELECT * FROM members WHERE id = '123006'").fetchone()
        assert row is not None
        assert row["name"] == "Jane Doe"
        assert row["party"] == "Liberal"

    def test_update_existing_member(self, db):
        base = {
            "id": "123006",
            "name": "Jane Doe",
            "party": "Liberal",
            "last_scraped": "2024-04-03T00:00:00",
        }
        upsert_member(db, base)
        db.commit()

        upsert_member(db, {**base, "party": "NDP"})
        db.commit()

        row = db.execute("SELECT party FROM members WHERE id = '123006'").fetchone()
        assert row["party"] == "NDP"

    def test_insert_multiple_members(self, db):
        for i in range(5):
            upsert_member(
                db,
                {
                    "id": str(i),
                    "name": f"MP {i}",
                    "last_scraped": "2024-01-01T00:00:00",
                },
            )
        db.commit()
        count = db.execute("SELECT COUNT(*) FROM members").fetchone()[0]
        assert count == 5


class TestUpsertBill:
    def test_insert_new_bill(self, db):
        upsert_bill(
            db,
            {
                "id": "45-1-c-47",
                "parliament": 45,
                "session": 1,
                "number": "C-47",
                "title": "Budget Implementation Act",
                "current_stage": "2nd_reading",
                "last_scraped": "2024-04-03T00:00:00",
            },
        )
        db.commit()
        row = db.execute("SELECT * FROM bills WHERE id = '45-1-c-47'").fetchone()
        assert row is not None
        assert row["number"] == "C-47"
        assert row["current_stage"] == "2nd_reading"

    def test_update_preserves_summaries(self, db):
        upsert_bill(
            db,
            {
                "id": "45-1-c-47",
                "title": "Budget Implementation Act",
                "summary_lop": "A bill about the budget.",
                "last_scraped": "2024-04-03T00:00:00",
            },
        )
        db.commit()

        # Update without summary — existing summary should be preserved
        upsert_bill(
            db,
            {
                "id": "45-1-c-47",
                "title": "Budget Implementation Act (amended)",
                "summary_lop": None,
                "last_scraped": "2024-04-04T00:00:00",
            },
        )
        db.commit()

        row = db.execute("SELECT summary_lop FROM bills WHERE id = '45-1-c-47'").fetchone()
        assert row["summary_lop"] == "A bill about the budget."


class TestUpsertDivision:
    def test_insert_new_division(self, db):
        upsert_division(
            db,
            {
                "id": "45-1-892",
                "parliament": 45,
                "session": 1,
                "number": 892,
                "date": "2024-04-03",
                "yeas": 172,
                "nays": 148,
                "result": "Agreed to",
                "chamber": "commons",
                "last_scraped": "2024-04-03T00:00:00",
            },
        )
        db.commit()
        row = db.execute("SELECT * FROM divisions WHERE id = '45-1-892'").fetchone()
        assert row is not None
        assert row["yeas"] == 172
        assert row["nays"] == 148

    def test_update_existing_division(self, db):
        base = {
            "id": "45-1-892",
            "parliament": 45,
            "session": 1,
            "number": 892,
            "yeas": 100,
            "nays": 50,
            "last_scraped": "2024-04-03T00:00:00",
        }
        upsert_division(db, base)
        db.commit()

        upsert_division(db, {**base, "yeas": 172, "nays": 148})
        db.commit()

        row = db.execute("SELECT yeas, nays FROM divisions WHERE id = '45-1-892'").fetchone()
        assert row["yeas"] == 172
        assert row["nays"] == 148


class TestUpsertMemberVote:
    def test_insert_member_vote(self, db):
        # Insert prerequisite member and division
        upsert_member(db, {"id": "123006", "name": "Jane Doe", "last_scraped": "2024-04-03"})
        upsert_division(
            db,
            {"id": "45-1-892", "parliament": 45, "session": 1, "number": 892,
             "last_scraped": "2024-04-03"},
        )
        db.commit()

        upsert_member_vote(db, "45-1-892", "123006", "Yea")
        db.commit()

        row = db.execute(
            "SELECT vote FROM member_votes WHERE division_id = '45-1-892' AND member_id = '123006'"
        ).fetchone()
        assert row["vote"] == "Yea"

    def test_update_member_vote(self, db):
        upsert_member(db, {"id": "123006", "name": "Jane", "last_scraped": "2024-04-03"})
        upsert_division(
            db,
            {"id": "45-1-892", "parliament": 45, "session": 1, "number": 892,
             "last_scraped": "2024-04-03"},
        )
        db.commit()

        upsert_member_vote(db, "45-1-892", "123006", "Nay")
        db.commit()
        upsert_member_vote(db, "45-1-892", "123006", "Yea")
        db.commit()

        row = db.execute(
            "SELECT vote FROM member_votes WHERE division_id = '45-1-892' AND member_id = '123006'"
        ).fetchone()
        assert row["vote"] == "Yea"


class TestUpsertBillStage:
    def test_insert_stage(self, db):
        upsert_bill(db, {"id": "45-1-c-47", "title": "Test", "last_scraped": "2024-04-03"})
        db.commit()

        upsert_bill_stage(
            db,
            {
                "bill_id": "45-1-c-47",
                "stage": "1st_reading",
                "chamber": "commons",
                "date": "2024-01-15",
                "notes": None,
            },
        )
        db.commit()

        row = db.execute(
            "SELECT * FROM bill_stages WHERE bill_id = '45-1-c-47'"
        ).fetchone()
        assert row is not None
        assert row["stage"] == "1st_reading"


class TestUpsertSittingDate:
    def test_insert_sitting_date(self, db):
        upsert_sitting_date(db, 45, 1, "2024-04-03")
        db.commit()
        row = db.execute(
            "SELECT date FROM sitting_calendar WHERE date = '2024-04-03'"
        ).fetchone()
        assert row is not None

    def test_idempotent(self, db):
        upsert_sitting_date(db, 45, 1, "2024-04-03")
        upsert_sitting_date(db, 45, 1, "2024-04-03")
        db.commit()
        count = db.execute(
            "SELECT COUNT(*) FROM sitting_calendar WHERE date = '2024-04-03'"
        ).fetchone()[0]
        assert count == 1


class TestDivisionExists:
    def test_returns_false_when_not_exists(self, db):
        assert not division_exists(db, "45-1-999")

    def test_returns_true_when_exists(self, db):
        upsert_division(
            db,
            {
                "id": "45-1-999",
                "parliament": 45,
                "session": 1,
                "number": 999,
                "last_scraped": "2024-04-03",
            },
        )
        db.commit()
        assert division_exists(db, "45-1-999")
