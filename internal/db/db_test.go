package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/philspins/open-democracy/internal/db"
)

func tempDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestMigrate_CreatesAllTables(t *testing.T) {
	d := tempDB(t)

	tables := []string{
		"members", "bills", "divisions",
		"member_votes", "bill_stages", "sitting_calendar",
	}
	for _, tbl := range tables {
		var name string
		err := d.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name)
		if err != nil || name != tbl {
			t.Errorf("expected table %q to exist, got err=%v name=%q", tbl, err, name)
		}
	}
}

func TestMigrate_CreatesIndices(t *testing.T) {
	d := tempDB(t)

	indices := []string{
		"idx_divisions_bill",
		"idx_member_votes_member",
		"idx_bills_stage",
		"idx_bills_category",
		"idx_bill_stages_bill",
	}
	for _, idx := range indices {
		var name string
		err := d.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&name)
		if err != nil || name != idx {
			t.Errorf("expected index %q to exist, got err=%v name=%q", idx, err, name)
		}
	}
}

func TestUpsertMember(t *testing.T) {
	d := tempDB(t)

	m := db.Member{
		ID:          "123006",
		Name:        "Jane Doe",
		Party:       "Liberal",
		Riding:      "Ottawa Centre",
		Province:    "Ontario",
		Role:        "Member of Parliament",
		Chamber:     "commons",
		Active:      true,
		LastScraped: "2024-04-03T00:00:00",
	}
	if err := db.UpsertMember(d, m); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	var name, party string
	err := d.QueryRow(`SELECT name, party FROM members WHERE id='123006'`).Scan(&name, &party)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "Jane Doe" {
		t.Errorf("name=%q want %q", name, "Jane Doe")
	}
	if party != "Liberal" {
		t.Errorf("party=%q want %q", party, "Liberal")
	}
}

func TestUpsertMember_Updates(t *testing.T) {
	d := tempDB(t)

	base := db.Member{ID: "123006", Name: "Jane Doe", Party: "Liberal", Active: true, LastScraped: "2024-04-03"}
	db.UpsertMember(d, base)

	updated := base
	updated.Party = "NDP"
	db.UpsertMember(d, updated)

	var party string
	d.QueryRow(`SELECT party FROM members WHERE id='123006'`).Scan(&party)
	if party != "NDP" {
		t.Errorf("expected party=NDP, got %q", party)
	}
}

func TestUpsertBill(t *testing.T) {
	d := tempDB(t)

	b := db.Bill{
		ID:           "45-1-c-47",
		Parliament:   45,
		Session:      1,
		Number:       "C-47",
		Title:        "Budget Implementation Act",
		CurrentStage: "2nd_reading",
		LastScraped:  "2024-04-03T00:00:00",
	}
	if err := db.UpsertBill(d, b); err != nil {
		t.Fatalf("UpsertBill: %v", err)
	}

	var number, stage string
	err := d.QueryRow(`SELECT number, current_stage FROM bills WHERE id='45-1-c-47'`).Scan(&number, &stage)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if number != "C-47" {
		t.Errorf("number=%q want C-47", number)
	}
	if stage != "2nd_reading" {
		t.Errorf("current_stage=%q want 2nd_reading", stage)
	}
}

func TestUpsertBill_PreservesSummaries(t *testing.T) {
	d := tempDB(t)

	// Insert with LoP summary
	b := db.Bill{ID: "45-1-c-47", Title: "Budget", SummaryLoP: "A bill.", LastScraped: "2024-04-03"}
	db.UpsertBill(d, b)

	// Update without summary — existing summary should be preserved
	b2 := db.Bill{ID: "45-1-c-47", Title: "Budget (amended)", SummaryLoP: "", LastScraped: "2024-04-04"}
	db.UpsertBill(d, b2)

	var summary string
	d.QueryRow(`SELECT summary_lop FROM bills WHERE id='45-1-c-47'`).Scan(&summary)
	if summary != "A bill." {
		t.Errorf("expected summary_lop preserved, got %q", summary)
	}
}

func TestUpsertDivision(t *testing.T) {
	d := tempDB(t)

	div := db.Division{
		ID:          "45-1-892",
		Parliament:  45,
		Session:     1,
		Number:      892,
		Date:        "2024-04-03",
		Yeas:        172,
		Nays:        148,
		Result:      "Agreed to",
		Chamber:     "commons",
		LastScraped: "2024-04-03T00:00:00",
	}
	if err := db.UpsertDivision(d, div); err != nil {
		t.Fatalf("UpsertDivision: %v", err)
	}

	var yeas, nays int
	err := d.QueryRow(`SELECT yeas, nays FROM divisions WHERE id='45-1-892'`).Scan(&yeas, &nays)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if yeas != 172 || nays != 148 {
		t.Errorf("yeas=%d nays=%d, want 172/148", yeas, nays)
	}
}

func TestUpsertMemberVote(t *testing.T) {
	d := tempDB(t)

	// Insert prerequisites
	db.UpsertMember(d, db.Member{ID: "123006", Name: "Jane", Active: true, LastScraped: "2024"})
	db.UpsertDivision(d, db.Division{ID: "45-1-892", Parliament: 45, Session: 1, Number: 892, LastScraped: "2024"})

	if err := db.UpsertMemberVote(d, "45-1-892", "123006", "Yea"); err != nil {
		t.Fatalf("UpsertMemberVote: %v", err)
	}

	var vote string
	d.QueryRow(`SELECT vote FROM member_votes WHERE division_id='45-1-892' AND member_id='123006'`).Scan(&vote)
	if vote != "Yea" {
		t.Errorf("vote=%q want Yea", vote)
	}
}

func TestUpsertMemberVote_Updates(t *testing.T) {
	d := tempDB(t)

	db.UpsertMember(d, db.Member{ID: "123006", Name: "Jane", Active: true, LastScraped: "2024"})
	db.UpsertDivision(d, db.Division{ID: "45-1-892", Parliament: 45, Session: 1, Number: 892, LastScraped: "2024"})

	db.UpsertMemberVote(d, "45-1-892", "123006", "Nay")
	db.UpsertMemberVote(d, "45-1-892", "123006", "Yea")

	var vote string
	d.QueryRow(`SELECT vote FROM member_votes WHERE division_id='45-1-892' AND member_id='123006'`).Scan(&vote)
	if vote != "Yea" {
		t.Errorf("vote=%q want Yea after update", vote)
	}
}

func TestUpsertSittingDate_Idempotent(t *testing.T) {
	d := tempDB(t)

	db.UpsertSittingDate(d, 45, 1, "2024-04-03")
	db.UpsertSittingDate(d, 45, 1, "2024-04-03") // duplicate — should be ignored

	var count int
	d.QueryRow(`SELECT COUNT(1) FROM sitting_calendar WHERE date='2024-04-03'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

func TestDivisionExists(t *testing.T) {
	d := tempDB(t)

	exists, err := db.DivisionExists(d, "45-1-999")
	if err != nil || exists {
		t.Errorf("expected false for missing division, got %v err=%v", exists, err)
	}

	db.UpsertDivision(d, db.Division{ID: "45-1-999", Parliament: 45, Session: 1, Number: 999, LastScraped: "2024"})

	exists, err = db.DivisionExists(d, "45-1-999")
	if err != nil || !exists {
		t.Errorf("expected true after insert, got %v err=%v", exists, err)
	}
}

func TestSittingDates(t *testing.T) {
	d := tempDB(t)

	for _, date := range []string{"2024-04-03", "2024-04-04", "2024-04-10"} {
		db.UpsertSittingDate(d, 45, 1, date)
	}

	dates, err := db.SittingDates(d, 45, 1)
	if err != nil {
		t.Fatalf("SittingDates: %v", err)
	}
	if len(dates) != 3 {
		t.Errorf("len=%d want 3", len(dates))
	}
	if dates[0] != "2024-04-03" {
		t.Errorf("dates[0]=%q want 2024-04-03", dates[0])
	}
}
