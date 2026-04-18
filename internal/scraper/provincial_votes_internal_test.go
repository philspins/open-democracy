package scraper

import (
	"path/filepath"
	"testing"

	"github.com/philspins/open-democracy/internal/db"
)

func TestHasPDFTextShowOperator(t *testing.T) {
	if !hasPDFTextShowOperator("BT /F9 7.999 Tf 0 0 0 rg 380.167 TL 242.496 325.155 Td (Kaeding, ) Tj T* ET") {
		t.Fatal("expected line with inline Tj operator to be detected")
	}
	if !hasPDFTextShowOperator("(Members\376\377\000'\000 )Tj") {
		t.Fatal("expected line ending in Tj without preceding space to be detected")
	}
	if !hasPDFTextShowOperator("[(Legislati)-6.2(v)1(e Assem)-6(b)2.2(ly )]TJ") {
		t.Fatal("expected line ending in TJ without preceding space to be detected")
	}
	if hasPDFTextShowOperator("q 16.622 368.075 754.532 14.0 re W n") {
		t.Fatal("expected non-text operator line to be ignored")
	}
}

func TestIsPEICaptchaBody_CaseInsensitive(t *testing.T) {
	if !isPEICaptchaBody([]byte(`<html><head><link href="HTTPS://CAPTCHA.PERFDRIVE.COM/challenge.css"></head></html>`)) {
		t.Fatal("expected captcha signature to be detected case-insensitively")
	}
	if !isPEICaptchaBody([]byte(`<script src="https://cdn.perfdrive.com/aperture/aperture.js"></script>`)) {
		t.Fatal("expected generic perfdrive bot-manager signature to be detected")
	}
}

func TestExtractPlainVoteNames_CollapsesSplitUppercaseSurnames(t *testing.T) {
	block := `AYE B ALCAEN B EREZA D ELA C RUZ W OWCHUK ................................ ..... 46 NAY ................................ 0`
	names := extractPlainVoteNames(block)
	want := []string{"BALCAEN", "BEREZA", "DELA CRUZ", "WOWCHUK"}
	if len(names) != len(want) {
		t.Fatalf("len(names)=%d, want %d (%v)", len(names), len(want), names)
	}
	for i, got := range names {
		if got != want[i] {
			t.Fatalf("names[%d]=%q, want %q", i, got, want[i])
		}
	}
}

func TestResolveProvincialMemberID_StripsTitlesAndMatchesInitialPlusSurname(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer conn.Close()

	_, err = conn.Exec(`INSERT INTO members (id, name, province, chamber, active, government_level) VALUES
		('nb-legislature-wilson-sherry', 'Sherry Wilson', 'New Brunswick', 'new_brunswick', 1, 'provincial'),
		('nb-legislature-wilson-mary', 'Mary Wilson', 'New Brunswick', 'new_brunswick', 1, 'provincial'),
		('nb-legislature-savoie-glen', 'Glen Savoie', 'New Brunswick', 'new_brunswick', 1, 'provincial'),
		('nb-legislature-chiasson-chuck', 'Chuck Chiasson', 'New Brunswick', 'new_brunswick', 1, 'provincial'),
		('manitoba-legislature-dela-cruz-nellie', 'Nellie Kennedy Dela Cruz', 'Manitoba', 'manitoba', 1, 'provincial')`)
	if err != nil {
		t.Fatalf("insert members: %v", err)
	}

	tests := []struct {
		province   string
		sourceName string
		wantID     string
	}{
		{"New Brunswick", "Hon. Ms. S. Wilson", "nb-legislature-wilson-sherry"},
		{"New Brunswick", "Hon. Ms. M. Wilson", "nb-legislature-wilson-mary"},
		{"New Brunswick", "Hon. Mr. G. Savoie", "nb-legislature-savoie-glen"},
		{"New Brunswick", "Mr. C. Chiasson", "nb-legislature-chiasson-chuck"},
		{"Manitoba", "DELA CRUZ", "manitoba-legislature-dela-cruz-nellie"},
	}

	for _, tc := range tests {
		got, err := resolveProvincialMemberID(conn, tc.province, tc.sourceName)
		if err != nil {
			t.Fatalf("resolveProvincialMemberID(%q): %v", tc.sourceName, err)
		}
		if got != tc.wantID {
			t.Fatalf("resolveProvincialMemberID(%q)=%q, want %q", tc.sourceName, got, tc.wantID)
		}
	}
}

func TestParseNewBrunswickVoteNames_KeepsInitialAndSurname(t *testing.T) {
	block := `YEAS - 25 Hon. Mr. Hogan Hon. Ms. S. Wilson Ms. Scott - Wallace Mr. J. LeBlanc Hon. Mr. G. Savoie`
	names := parseNewBrunswickVoteNames(block)
	want := []string{"Hogan", "S. Wilson", "Scott-Wallace", "J. LeBlanc", "G. Savoie"}
	if len(names) != len(want) {
		t.Fatalf("len(names)=%d, want %d (%v)", len(names), len(want), names)
	}
	for i, got := range names {
		if got != want[i] {
			t.Fatalf("names[%d]=%q, want %q (all=%v)", i, got, want[i], names)
		}
	}
}
