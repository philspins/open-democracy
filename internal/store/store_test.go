package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/store"
)

func tempDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("tempDB: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestListBills_Empty(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	bills, total, err := st.ListBills(store.BillFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills: %v", err)
	}
	if total != 0 {
		t.Errorf("want total=0, got %d", total)
	}
	if len(bills) != 0 {
		t.Errorf("want 0 bills, got %d", len(bills))
	}
}

func TestListBills_Filter(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO bills (id, parliament, session, number, title, category, current_stage, chamber)
		VALUES ('b1', 45, 1, 'C-1', 'Housing Act', 'Housing', '1st_reading', 'commons'),
		       ('b2', 45, 1, 'C-2', 'Health Act', 'Health', '1st_reading', 'commons')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	bills, total, err := st.ListBills(store.BillFilter{Category: "Housing", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills: %v", err)
	}
	if total != 1 {
		t.Errorf("want total=1, got %d", total)
	}
	if len(bills) != 1 || bills[0].ID != "b1" {
		t.Errorf("wrong bill returned: %+v", bills)
	}
}

func TestGetBill_NotFound(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	_, err := st.GetBill("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent bill")
	}
}

func TestGetMember_NotFound(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	_, err := st.GetMember("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent member")
	}
}

func TestGetParliamentStatus_InSession(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	today := time.Now().Format("2006-01-02")
	_, err := conn.Exec(`INSERT INTO sitting_calendar (parliament, session, date) VALUES (45, 1, ?)`, today)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	ps, err := st.GetParliamentStatus(45, 1)
	if err != nil {
		t.Fatalf("GetParliamentStatus: %v", err)
	}
	if ps.Status != "in_session" {
		t.Errorf("want in_session, got %q", ps.Status)
	}
}

func TestGetParliamentStatus_OnBreak(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	// No sitting dates — should be on_break
	ps, err := st.GetParliamentStatus(45, 1)
	if err != nil {
		t.Fatalf("GetParliamentStatus: %v", err)
	}
	if ps.Status != "on_break" {
		t.Errorf("want on_break, got %q", ps.Status)
	}
}

func TestGetParliamentStatus_NextSitting(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)
	// Insert a future date
	futureDate := "2099-01-01"
	_, err := conn.Exec(`INSERT INTO sitting_calendar (parliament, session, date) VALUES (45, 1, ?)`, futureDate)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	ps, err := st.GetParliamentStatus(45, 1)
	if err != nil {
		t.Fatalf("GetParliamentStatus: %v", err)
	}
	if ps.Status != "on_break" {
		t.Errorf("want on_break for future date, got %q", ps.Status)
	}
	if ps.Detail != "Next sitting: "+futureDate {
		t.Errorf("unexpected detail: %q", ps.Detail)
	}
}

func TestGetMemberStats_Basic(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO members (id, name, party, riding, province, chamber, active)
		VALUES ('m1', 'Alice Smith', 'Liberal', 'Ottawa Centre', 'ON', 'commons', 1),
		       ('m2', 'Bob Jones', 'Liberal', 'Ottawa West', 'ON', 'commons', 1)`)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	for i := 1; i <= 3; i++ {
		_, err := conn.Exec(fmt.Sprintf(`INSERT INTO divisions (id, parliament, session, number, date, yeas, nays, result, chamber)
			VALUES (?, 45, 1, ?, '2025-01-0%d', 100, 50, 'Carried', 'commons')`, i),
			fmt.Sprintf("d%d", i), i)
		if err != nil {
			t.Fatalf("insert division: %v", err)
		}
	}

	// m1 votes: d1=Yea, d2=Yea, d3=Nay; m2 votes: d1=Yea, d2=Yea, d3=Yea
	for _, v := range []struct{ div, member, vote string }{
		{"d1", "m1", "Yea"}, {"d1", "m2", "Yea"},
		{"d2", "m1", "Yea"}, {"d2", "m2", "Yea"},
		{"d3", "m1", "Nay"}, {"d3", "m2", "Yea"},
	} {
		_, err := conn.Exec(`INSERT INTO member_votes (division_id, member_id, vote) VALUES (?,?,?)`,
			v.div, v.member, v.vote)
		if err != nil {
			t.Fatalf("insert vote: %v", err)
		}
	}

	stats, err := st.GetMemberStats("m1")
	if err != nil {
		t.Fatalf("GetMemberStats: %v", err)
	}
	if stats.TotalVotes != 3 {
		t.Errorf("want TotalVotes=3, got %d", stats.TotalVotes)
	}
	// m1 voted Yea in d1,d2 (party majority Yea) and Nay in d3 (party majority Yea from m2)
	// party line: 2/3 = 66%
	if stats.PartyLinePct < 60 || stats.PartyLinePct > 70 {
		t.Errorf("want PartyLinePct ~66, got %d", stats.PartyLinePct)
	}
}

func TestCompareMemberVotes(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO members (id, name, party, chamber, active)
		VALUES ('m1', 'Alice', 'Liberal', 'commons', 1),
		       ('m2', 'Bob', 'Conservative', 'commons', 1)`)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	for i := 1; i <= 3; i++ {
		_, err := conn.Exec(fmt.Sprintf(`INSERT INTO divisions (id, parliament, session, number, date, yeas, nays, result, chamber)
			VALUES (?, 45, 1, ?, '2025-01-0%d', 100, 50, 'Carried', 'commons')`, i),
			fmt.Sprintf("d%d", i), i)
		if err != nil {
			t.Fatalf("insert division: %v", err)
		}
	}

	// d1: both Yea (agree), d2: both Nay (agree), d3: m1 Yea m2 Nay (disagree)
	for _, v := range []struct{ div, member, vote string }{
		{"d1", "m1", "Yea"}, {"d1", "m2", "Yea"},
		{"d2", "m1", "Nay"}, {"d2", "m2", "Nay"},
		{"d3", "m1", "Yea"}, {"d3", "m2", "Nay"},
	} {
		_, err := conn.Exec(`INSERT INTO member_votes (division_id, member_id, vote) VALUES (?,?,?)`,
			v.div, v.member, v.vote)
		if err != nil {
			t.Fatalf("insert vote: %v", err)
		}
	}

	overlap, total, err := st.CompareMemberVotes("m1", "m2")
	if err != nil {
		t.Fatalf("CompareMemberVotes: %v", err)
	}
	if total != 3 {
		t.Errorf("want total=3, got %d", total)
	}
	if overlap != 2 {
		t.Errorf("want overlap=2, got %d", overlap)
	}
}

func TestUpsertUserAndFollowMember(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO members (id, name, chamber, active) VALUES ('m1', 'Jane MP', 'commons', 1)`)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	u, err := st.UpsertUser("person@example.com", "K1A0B1")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if u.ID == "" || u.Email != "person@example.com" {
		t.Fatalf("unexpected user: %+v", u)
	}

	if err := st.FollowMember("person@example.com", "K1A0B1", "m1"); err != nil {
		t.Fatalf("FollowMember: %v", err)
	}

	var count int
	err = conn.QueryRow(`SELECT COUNT(*) FROM user_follows WHERE user_id=? AND member_id='m1'`, u.ID).Scan(&count)
	if err != nil {
		t.Fatalf("query follow: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 follow row, got %d", count)
	}
}

func TestReactToBillAndCounts(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO bills (id, parliament, session, number, title) VALUES ('b1', 45, 1, 'C-1', 'Test Bill')`)
	if err != nil {
		t.Fatalf("insert bill: %v", err)
	}

	if err := st.ReactToBill("a@example.com", "", "b1", "support", "Looks good"); err != nil {
		t.Fatalf("ReactToBill support: %v", err)
	}
	if err := st.ReactToBill("b@example.com", "", "b1", "oppose", "Concerned"); err != nil {
		t.Fatalf("ReactToBill oppose: %v", err)
	}
	if err := st.ReactToBill("a@example.com", "", "b1", "neutral", "Updating vote"); err != nil {
		t.Fatalf("ReactToBill update: %v", err)
	}

	c, err := st.GetBillReactionCounts("b1")
	if err != nil {
		t.Fatalf("GetBillReactionCounts: %v", err)
	}
	if c.TotalReactions != 2 || c.SupportCount != 0 || c.OpposeCount != 1 || c.NeutralCount != 1 {
		t.Fatalf("unexpected counts: %+v", c)
	}
}

func TestLogPolicySubmission(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO members (id, name, chamber, active) VALUES ('m1', 'Jane MP', 'commons', 1)`)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	err = st.LogPolicySubmission("person@example.com", "K1A0B1", "m1", "Housing support", "Please support this bill", "Housing")
	if err != nil {
		t.Fatalf("LogPolicySubmission: %v", err)
	}

	var count int
	err = conn.QueryRow(`SELECT COUNT(*) FROM policy_submissions`).Scan(&count)
	if err != nil {
		t.Fatalf("query submissions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 submission row, got %d", count)
	}
}
