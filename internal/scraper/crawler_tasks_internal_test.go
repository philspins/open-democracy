package scraper

import (
	"path/filepath"
	"testing"

	"github.com/philspins/open-democracy/internal/db"
)

func TestExtractLegislatureSessionCandidates_AlbertaFormats(t *testing.T) {
	tests := []struct {
		text string
		want legislatureSession
	}{
		{
			text: "Legislature, Session 31-2 (2025-2026)",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
		{
			text: "Legislature 31, Session 2 (2025-2026)",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
		{
			text: "https://www.assembly.ab.ca/assembly-business/assembly-dashboard?legl=31&session=2&sectionb=d&btn=i#page-menu",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
	}

	for _, tc := range tests {
		candidates := extractLegislatureSessionCandidates("ab", tc.text, 50)
		best, ok := maxLegislatureSession(candidates)
		if !ok {
			t.Fatalf("no candidates for %q", tc.text)
		}
		if best.Legislature != tc.want.Legislature || best.Session != tc.want.Session {
			t.Fatalf("best=%+v, want legislature=%d session=%d", best, tc.want.Legislature, tc.want.Session)
		}
	}
}

func TestExtractLegislatureSessionCandidates_ManitobaFormats(t *testing.T) {
	tests := []struct {
		text string
		want legislatureSession
	}{
		{
			text: "Current Session: 43 - 3 (2025- )",
			want: legislatureSession{Legislature: 43, Session: 3},
		},
		{
			text: "https://web2.gov.mb.ca/bills/43-3/index.php",
			want: legislatureSession{Legislature: 43, Session: 3},
		},
	}

	for _, tc := range tests {
		candidates := extractLegislatureSessionCandidates("mb", tc.text, 50)
		best, ok := maxLegislatureSession(candidates)
		if !ok {
			t.Fatalf("no candidates for %q", tc.text)
		}
		if best.Legislature != tc.want.Legislature || best.Session != tc.want.Session {
			t.Fatalf("best=%+v, want legislature=%d session=%d", best, tc.want.Legislature, tc.want.Session)
		}
	}
}

func TestExtractLegislatureSessionCandidates_NovaScotiaHansardURL(t *testing.T) {
	candidates := extractLegislatureSessionCandidates("ns", "https://nslegislature.ca/legislative-business/hansard-debates/assembly-65-session-1", 50)
	best, ok := maxLegislatureSession(candidates)
	if !ok {
		t.Fatal("no candidates for Nova Scotia Hansard session URL")
	}
	if best.Legislature != 65 || best.Session != 1 {
		t.Fatalf("best=%+v, want legislature=65 session=1", best)
	}
}

func TestNormalizeSaskatchewanBillsURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{
			in:   "https://www.legassembly.sk.ca/legislative-business",
			want: "https://www.legassembly.sk.ca/legislative-business/bills/",
		},
		{
			in:   "https://www.legassembly.sk.ca/legislative-business/",
			want: "https://www.legassembly.sk.ca/legislative-business/bills/",
		},
		{
			in:   "https://www.legassembly.sk.ca/legislative-business/bills/",
			want: "https://www.legassembly.sk.ca/legislative-business/bills/",
		},
	}

	for _, tc := range tests {
		if got := normalizeSaskatchewanBillsURL(tc.in); got != tc.want {
			t.Fatalf("normalizeSaskatchewanBillsURL(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanupManitobaStaleSessionDivisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(path)
	if err != nil {
		 t.Fatalf("db.Open: %v", err)
	}
	defer conn.Close()

	if err := db.UpsertDivision(conn, db.Division{ID: "mb-43-3-2025-04-07-2", Parliament: 43, Session: 3, Number: 2, Date: "2025-04-07", SittingURL: "https://www.gov.mb.ca/legislature/business/43rd/2nd/votes_037.pdf", LastScraped: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("insert stale division: %v", err)
	}
	if err := db.UpsertDivision(conn, db.Division{ID: "mb-43-3-2025-10-07-44", Parliament: 43, Session: 3, Number: 44, Date: "2025-10-07", SittingURL: "https://www.gov.mb.ca/legislature/business/43rd/3rd/votes_044.pdf", LastScraped: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("insert current division: %v", err)
	}
	_, err = conn.Exec(`INSERT INTO members (id, name, province, chamber, active, government_level) VALUES
		('m1', 'Member One', 'Manitoba', 'manitoba', 1, 'provincial'),
		('m2', 'Member Two', 'Manitoba', 'manitoba', 1, 'provincial')`)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}
	_, err = conn.Exec(`INSERT INTO member_votes (division_id, member_id, vote) VALUES ('mb-43-3-2025-04-07-2', 'm1', 'Yea'), ('mb-43-3-2025-10-07-44', 'm2', 'Nay')`)
	if err != nil {
		t.Fatalf("insert member votes: %v", err)
	}

	deleted, err := cleanupManitobaStaleSessionDivisions(conn, 43, 3)
	if err != nil {
		t.Fatalf("cleanupManitobaStaleSessionDivisions: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d, want 1", deleted)
	}

	var staleCount, currentCount, staleVoteCount int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id='mb-43-3-2025-04-07-2'`).Scan(&staleCount); err != nil {
		t.Fatalf("query stale division: %v", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id='mb-43-3-2025-10-07-44'`).Scan(&currentCount); err != nil {
		t.Fatalf("query current division: %v", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(1) FROM member_votes WHERE division_id='mb-43-3-2025-04-07-2'`).Scan(&staleVoteCount); err != nil {
		t.Fatalf("query stale votes: %v", err)
	}
	if staleCount != 0 || currentCount != 1 || staleVoteCount != 0 {
		t.Fatalf("staleCount=%d currentCount=%d staleVoteCount=%d", staleCount, currentCount, staleVoteCount)
	}
}