// Package db provides the SQLite schema and upsert helpers for Open Democracy.
//
// Schema can be migrated to Postgres later by swapping the driver; all SQL
// is standard ANSI with SQLite-compatible ON CONFLICT clauses.
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// DefaultPath is the SQLite database file used when no path is provided.
const DefaultPath = "open-democracy.db"

// Open returns an initialised *sql.DB with WAL mode and FK enforcement.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		path = DefaultPath
	}
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db %q: %w", path, err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate creates all tables and indices if they do not already exist.
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS members (
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
			last_scraped TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS bills (
			id                 TEXT PRIMARY KEY,
			parliament         INTEGER,
			session            INTEGER,
			number             TEXT,
			title              TEXT,
			short_title        TEXT,
			bill_type          TEXT,
			chamber            TEXT,
			sponsor_id         TEXT REFERENCES members(id),
			current_stage      TEXT,
			current_status     TEXT,
			category           TEXT,
			summary_ai         TEXT,
			summary_lop        TEXT,
			full_text_url      TEXT,
			legisinfo_url      TEXT,
			introduced_date    TEXT,
			last_activity_date TEXT,
			last_scraped       TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS divisions (
			id           TEXT PRIMARY KEY,
			parliament   INTEGER,
			session      INTEGER,
			number       INTEGER,
			date         TEXT,
			bill_id      TEXT REFERENCES bills(id),
			description  TEXT,
			yeas         INTEGER,
			nays         INTEGER,
			paired       INTEGER DEFAULT 0,
			result       TEXT,
			chamber      TEXT DEFAULT 'commons',
			sitting_url  TEXT,
			last_scraped TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS member_votes (
			division_id TEXT REFERENCES divisions(id),
			member_id   TEXT REFERENCES members(id),
			vote        TEXT,
			PRIMARY KEY (division_id, member_id)
		)`,
		`CREATE TABLE IF NOT EXISTS bill_stages (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			bill_id TEXT REFERENCES bills(id),
			stage   TEXT,
			chamber TEXT,
			date    TEXT,
			notes   TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sitting_calendar (
			parliament INTEGER,
			session    INTEGER,
			date       TEXT,
			PRIMARY KEY (parliament, session, date)
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id                   TEXT PRIMARY KEY,
			email                TEXT UNIQUE,
			postal_code          TEXT,
			federal_riding_id    TEXT,
			provincial_riding_id TEXT,
			created_at           TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			email_digest         TEXT DEFAULT 'weekly'
		)`,
		`CREATE TABLE IF NOT EXISTS user_follows (
			user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
			member_id  TEXT REFERENCES members(id) ON DELETE CASCADE,
			created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			PRIMARY KEY (user_id, member_id)
		)`,
		`CREATE TABLE IF NOT EXISTS bill_reactions (
			user_id    TEXT REFERENCES users(id) ON DELETE CASCADE,
			bill_id    TEXT REFERENCES bills(id) ON DELETE CASCADE,
			reaction   TEXT CHECK (reaction IN ('support', 'oppose', 'neutral')),
			note       TEXT,
			created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			PRIMARY KEY (user_id, bill_id)
		)`,
		`CREATE TABLE IF NOT EXISTS policy_submissions (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id      TEXT REFERENCES users(id),
			member_id    TEXT REFERENCES members(id),
			subject      TEXT,
			body         TEXT,
			category     TEXT,
			submitted_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		)`,
		`CREATE TABLE IF NOT EXISTS bill_reaction_counts (
			bill_id          TEXT PRIMARY KEY REFERENCES bills(id),
			support_count    INTEGER DEFAULT 0,
			oppose_count     INTEGER DEFAULT 0,
			neutral_count    INTEGER DEFAULT 0,
			total_reactions  INTEGER DEFAULT 0,
			refreshed_at     TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_divisions_bill      ON divisions(bill_id)`,
		`CREATE INDEX IF NOT EXISTS idx_member_votes_member ON member_votes(member_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bills_stage         ON bills(current_stage)`,
		`CREATE INDEX IF NOT EXISTS idx_bills_category      ON bills(category)`,
		`CREATE INDEX IF NOT EXISTS idx_bill_stages_bill    ON bill_stages(bill_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_follows_member ON user_follows(member_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bill_reactions_bill ON bill_reactions(bill_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// nullStr converts an empty string to nil (SQL NULL) so that FK columns that
// have no value don't trigger foreign-key constraint violations.
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ── upsert helpers ────────────────────────────────────────────────────────────

// Member represents a row in the members table.
type Member struct {
	ID          string
	Name        string
	Party       string
	Riding      string
	Province    string
	Role        string
	PhotoURL    string
	Email       string
	Website     string
	Chamber     string
	Active      bool
	LastScraped string
}

// UpsertMember inserts or updates a member record.
func UpsertMember(db *sql.DB, m Member) error {
	active := 0
	if m.Active {
		active = 1
	}
	chamber := m.Chamber
	if chamber == "" {
		chamber = "commons"
	}
	_, err := db.Exec(`
		INSERT INTO members
			(id, name, party, riding, province, role, photo_url, email, website,
			 chamber, active, last_scraped)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
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
			last_scraped = excluded.last_scraped`,
		m.ID, m.Name, m.Party, m.Riding, m.Province, m.Role, m.PhotoURL,
		m.Email, m.Website, chamber, active, m.LastScraped,
	)
	return err
}

// Bill represents a row in the bills table.
type Bill struct {
	ID               string
	Parliament       int
	Session          int
	Number           string
	Title            string
	ShortTitle       string
	BillType         string
	Chamber          string
	SponsorID        string
	CurrentStage     string
	CurrentStatus    string
	Category         string
	SummaryAI        string
	SummaryLoP       string
	FullTextURL      string
	LegisInfoURL     string
	IntroducedDate   string
	LastActivityDate string
	LastScraped      string
}

// UpsertBill inserts or updates a bill record.
// Existing AI/LoP summaries are preserved when the incoming value is empty.
func UpsertBill(db *sql.DB, b Bill) error {
	_, err := db.Exec(`
		INSERT INTO bills
			(id, parliament, session, number, title, short_title, bill_type,
			 chamber, sponsor_id, current_stage, current_status, category,
			 summary_ai, summary_lop, full_text_url, legisinfo_url,
			 introduced_date, last_activity_date, last_scraped)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			summary_ai         = COALESCE(NULLIF(excluded.summary_ai,''), bills.summary_ai),
			summary_lop        = COALESCE(NULLIF(excluded.summary_lop,''), bills.summary_lop),
			full_text_url      = excluded.full_text_url,
			legisinfo_url      = excluded.legisinfo_url,
			introduced_date    = excluded.introduced_date,
			last_activity_date = excluded.last_activity_date,
			last_scraped       = excluded.last_scraped`,
		b.ID, b.Parliament, b.Session, b.Number, b.Title, b.ShortTitle,
		b.BillType, b.Chamber, nullStr(b.SponsorID), b.CurrentStage, b.CurrentStatus,
		b.Category, b.SummaryAI, b.SummaryLoP, b.FullTextURL, b.LegisInfoURL,
		b.IntroducedDate, b.LastActivityDate, b.LastScraped,
	)
	return err
}

// Division represents a row in the divisions table.
type Division struct {
	ID          string
	Parliament  int
	Session     int
	Number      int
	Date        string
	BillID      string
	Description string
	Yeas        int
	Nays        int
	Paired      int
	Result      string
	Chamber     string
	SittingURL  string
	LastScraped string
}

// UpsertDivision inserts or updates a division record.
func UpsertDivision(db *sql.DB, d Division) error {
	chamber := d.Chamber
	if chamber == "" {
		chamber = "commons"
	}
	_, err := db.Exec(`
		INSERT INTO divisions
			(id, parliament, session, number, date, bill_id, description,
			 yeas, nays, paired, result, chamber, sitting_url, last_scraped)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			last_scraped = excluded.last_scraped`,
		d.ID, d.Parliament, d.Session, d.Number, d.Date, nullStr(d.BillID), d.Description,
		d.Yeas, d.Nays, d.Paired, d.Result, chamber, d.SittingURL, d.LastScraped,
	)
	return err
}

// UpsertMemberVote inserts or updates a single MP vote on a division.
func UpsertMemberVote(db *sql.DB, divisionID, memberID, vote string) error {
	_, err := db.Exec(`
		INSERT INTO member_votes (division_id, member_id, vote)
		VALUES (?,?,?)
		ON CONFLICT(division_id, member_id) DO UPDATE SET vote = excluded.vote`,
		divisionID, memberID, vote,
	)
	return err
}

// BillStage represents a row in the bill_stages table.
type BillStage struct {
	BillID  string
	Stage   string
	Chamber string
	Date    string
	Notes   string
}

// UpsertBillStage inserts a bill-stage record (idempotent by bill_id + stage + date).
func UpsertBillStage(db *sql.DB, s BillStage) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO bill_stages (bill_id, stage, chamber, date, notes)
		VALUES (?,?,?,?,?)`,
		s.BillID, s.Stage, s.Chamber, s.Date, s.Notes,
	)
	return err
}

// UpsertSittingDate inserts a sitting calendar date (idempotent).
func UpsertSittingDate(db *sql.DB, parliament, session int, date string) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO sitting_calendar (parliament, session, date)
		VALUES (?,?,?)`,
		parliament, session, date,
	)
	return err
}

// DivisionExists returns true if a division with the given ID already exists.
func DivisionExists(db *sql.DB, divisionID string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id = ?`, divisionID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SittingDates returns all sitting dates for the given parliament/session.
func SittingDates(db *sql.DB, parliament, session int) ([]string, error) {
	rows, err := db.Query(
		`SELECT date FROM sitting_calendar WHERE parliament = ? AND session = ? ORDER BY date`,
		parliament, session,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		dates = append(dates, d)
	}
	return dates, rows.Err()
}
