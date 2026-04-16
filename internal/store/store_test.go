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

func TestListBills_ChamberFilter(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO bills (id, parliament, session, number, title, category, current_stage, chamber)
		VALUES ('b1', 45, 1, 'C-1', 'Commons Bill', 'Housing', '1st_reading', 'commons'),
		       ('b2', 45, 1, 'S-1', 'Senate Bill', 'Health', '1st_reading', 'senate')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	bills, total, err := st.ListBills(store.BillFilter{Chamber: "commons", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills: %v", err)
	}
	if total != 1 {
		t.Errorf("want total=1 for commons filter, got %d", total)
	}
	if len(bills) != 1 || bills[0].ID != "b1" {
		t.Errorf("wrong bill returned for commons filter: %+v", bills)
	}

	bills, total, err = st.ListBills(store.BillFilter{Chamber: "senate", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills senate: %v", err)
	}
	if total != 1 {
		t.Errorf("want total=1 for senate filter, got %d", total)
	}
	if len(bills) != 1 || bills[0].ID != "b2" {
		t.Errorf("wrong bill returned for senate filter: %+v", bills)
	}
}

func TestListBills_LevelFilter(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO bills (id, parliament, session, number, title, category, current_stage, chamber)
		VALUES ('45-1-c-1', 45, 1, 'C-1', 'Federal Commons Bill', 'Housing', '1st_reading', 'commons'),
		       ('45-1-s-1', 45, 1, 'S-1', 'Federal Senate Bill', 'Health', '1st_reading', 'senate'),
		       ('on-43-1-12', 43, 1, '12', 'Ontario Bill', 'Housing', '1st_reading', 'ontario')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	bills, total, err := st.ListBills(store.BillFilter{Level: "federal", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills federal: %v", err)
	}
	if total != 2 || len(bills) != 2 {
		t.Fatalf("expected 2 federal bills, total=%d len=%d", total, len(bills))
	}
	for _, b := range bills {
		if b.ID == "on-43-1-12" {
			t.Fatalf("provincial bill included in federal filter: %+v", b)
		}
	}

	bills, total, err = st.ListBills(store.BillFilter{Level: "provincial", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListBills provincial: %v", err)
	}
	if total != 1 || len(bills) != 1 {
		t.Fatalf("expected 1 provincial bill, total=%d len=%d", total, len(bills))
	}
	if bills[0].ID != "on-43-1-12" {
		t.Fatalf("unexpected provincial result: %+v", bills[0])
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

	u, err := st.UpsertUser("person@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if u.ID == "" || u.Email != "person@example.com" {
		t.Fatalf("unexpected user: %+v", u)
	}

	if err := st.FollowMember("person@example.com", "m1"); err != nil {
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

	if err := st.ReactToBill("a@example.com", "b1", "support", "Looks good"); err != nil {
		t.Fatalf("ReactToBill support: %v", err)
	}
	if err := st.ReactToBill("b@example.com", "b1", "oppose", "Concerned"); err != nil {
		t.Fatalf("ReactToBill oppose: %v", err)
	}
	if err := st.ReactToBill("a@example.com", "b1", "neutral", "Updating vote"); err != nil {
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

	err = st.LogPolicySubmission("person@example.com", "m1", "Housing support", "Please support this bill", "Housing")
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

func TestEmailVerificationFlow(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	token, _, err := st.CreateEmailVerification("verify@example.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	var storedToken string
	if err := conn.QueryRow(`SELECT token FROM email_verification_tokens WHERE email='verify@example.com' ORDER BY id DESC LIMIT 1`).Scan(&storedToken); err != nil {
		t.Fatalf("query stored token: %v", err)
	}
	if storedToken == token {
		t.Fatalf("expected token to be hashed at rest")
	}
	if len(storedToken) != 64 {
		t.Fatalf("expected sha256 hex token length 64, got %d", len(storedToken))
	}

	var storedCode string
	if err := conn.QueryRow(`SELECT code FROM email_verification_tokens WHERE email='verify@example.com' ORDER BY id DESC LIMIT 1`).Scan(&storedCode); err != nil {
		t.Fatalf("query stored code: %v", err)
	}
	if len(storedCode) != 64 {
		t.Fatalf("expected hashed code length 64, got %d", len(storedCode))
	}

	u, err := st.GetUserByEmail("verify@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u.EmailVerified {
		t.Fatalf("expected user to be unverified before token verification")
	}

	u, err = st.VerifyEmailToken(token)
	if err != nil {
		t.Fatalf("VerifyEmailToken: %v", err)
	}
	if !u.EmailVerified {
		t.Fatalf("expected user to be verified after token verification")
	}

	if _, err := st.VerifyEmailToken(token); err == nil {
		t.Fatalf("expected second token use to fail")
	}
}

func TestEmailVerificationByCode(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, code, err := st.CreateEmailVerification("code@example.com", time.Hour)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	u, err := st.VerifyEmailCode("code@example.com", code)
	if err != nil {
		t.Fatalf("VerifyEmailCode: %v", err)
	}
	if !u.EmailVerified {
		t.Fatalf("expected code-verified user to be verified")
	}
}

func TestEmailVerificationCooldown(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	if _, _, err := st.CreateEmailVerification("cooldown@example.com", time.Hour); err != nil {
		t.Fatalf("first CreateEmailVerification: %v", err)
	}
	if _, _, err := st.CreateEmailVerification("cooldown@example.com", time.Hour); err == nil {
		t.Fatalf("expected cooldown error on rapid second verification request")
	}
}

func TestSessionLifecycle(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	u, err := st.UpsertUser("session@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	sid, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var storedSessionID string
	if err := conn.QueryRow(`SELECT id FROM user_sessions WHERE user_id = ?`, u.ID).Scan(&storedSessionID); err != nil {
		t.Fatalf("query stored session id: %v", err)
	}
	if storedSessionID == sid {
		t.Fatalf("expected session id to be hashed at rest")
	}
	if len(storedSessionID) != 64 {
		t.Fatalf("expected sha256 hex session id length 64, got %d", len(storedSessionID))
	}

	got, err := st.GetUserBySession(sid)
	if err != nil {
		t.Fatalf("GetUserBySession: %v", err)
	}
	if got.Email != "session@example.com" {
		t.Fatalf("unexpected session user: %+v", got)
	}

	if err := st.DeleteSession(sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.GetUserBySession(sid); err == nil {
		t.Fatalf("expected deleted session lookup to fail")
	}
}

func TestAuthenticateOAuthMarksVerified(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	u, err := st.AuthenticateOAuth("google", "provider-123", "oauth@example.com", true)
	if err != nil {
		t.Fatalf("AuthenticateOAuth: %v", err)
	}
	if !u.EmailVerified {
		t.Fatalf("expected oauth-authenticated user to be verified")
	}

	// Re-auth on same provider identity should remain idempotent.
	u2, err := st.AuthenticateOAuth("google", "provider-123", "oauth@example.com", true)
	if err != nil {
		t.Fatalf("AuthenticateOAuth repeat: %v", err)
	}
	if u.ID != u2.ID {
		t.Fatalf("expected same user id on repeated oauth auth, got %q and %q", u.ID, u2.ID)
	}
}

func TestUpdateUserLocationPersistsAddressAndRidings(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	u, err := st.UpsertUser("profile@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	updated, err := st.UpdateUserLocation(u.ID, "123 Main St, Ottawa, ON", "Ottawa Centre", "Ottawa South")
	if err != nil {
		t.Fatalf("UpdateUserLocation: %v", err)
	}
	if updated.Address != "123 Main St, Ottawa, ON" {
		t.Fatalf("Address=%q want %q", updated.Address, "123 Main St, Ottawa, ON")
	}
	if updated.FederalRidingID != "Ottawa Centre" || updated.ProvincialRidingID != "Ottawa South" {
		t.Fatalf("unexpected riding ids: %+v", updated)
	}

	reloaded, err := st.GetUserByEmail("profile@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if reloaded.Address != updated.Address || reloaded.FederalRidingID != updated.FederalRidingID || reloaded.ProvincialRidingID != updated.ProvincialRidingID {
		t.Fatalf("reloaded user mismatch: %+v vs %+v", reloaded, updated)
	}
}

func TestListMembers_Filters(t *testing.T) {
	conn := tempDB(t)
	st := store.New(conn)

	_, err := conn.Exec(`INSERT INTO members (id, name, party, riding, province, chamber, active, government_level)
		VALUES ('m1', 'Alice Smith', 'Liberal', 'Ottawa Centre', 'Ontario', 'commons', 1, 'federal'),
		       ('m2', 'Bob Jones', 'Conservative', 'Calgary East', 'Alberta', 'commons', 1, 'federal'),
		       ('m3', 'Carol White', 'NDP', 'Vancouver East', 'British Columbia', 'legislature', 1, 'provincial')`)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	tests := []struct {
		name           string
		search         string
		party          string
		province       string
		governmentLevel string
		wantIDs        []string
	}{
		{"no filter returns all", "", "", "", "", []string{"m1", "m2", "m3"}},
		{"name search exact", "Alice Smith", "", "", "", []string{"m1"}},
		{"name search partial", "alice", "", "", "", []string{"m1"}},
		{"name search case insensitive", "ALICE", "", "", "", []string{"m1"}},
		{"riding search partial", "ottawa", "", "", "", []string{"m1"}},
		{"party exact match", "", "Liberal", "", "", []string{"m1"}},
		{"party lowercase", "", "liberal", "", "", []string{"m1"}},
		{"party partial match", "", "Lib", "", "", []string{"m1"}},
		{"province exact match", "", "", "Ontario", "", []string{"m1"}},
		{"province lowercase", "", "", "ontario", "", []string{"m1"}},
		{"province partial match", "", "", "Ont", "", []string{"m1"}},
		{"name and party combined", "alice", "Liberal", "", "", []string{"m1"}},
		{"no match returns empty", "zzz", "", "", "", []string{}},
		{"federal filter returns two", "", "", "", "federal", []string{"m1", "m2"}},
		{"provincial filter returns one", "", "", "", "provincial", []string{"m3"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			members, err := st.ListMembers(tc.search, tc.party, tc.province, tc.governmentLevel)
			if err != nil {
				t.Fatalf("ListMembers: %v", err)
			}
			gotIDs := make([]string, len(members))
			for i, m := range members {
				gotIDs[i] = m.ID
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Errorf("got %d members (%v), want %d (%v)", len(gotIDs), gotIDs, len(tc.wantIDs), tc.wantIDs)
				return
			}
			wantSet := make(map[string]bool)
			for _, id := range tc.wantIDs {
				wantSet[id] = true
			}
			for _, id := range gotIDs {
				if !wantSet[id] {
					t.Errorf("unexpected member ID %q in results %v", id, gotIDs)
				}
			}
		})
	}
}
