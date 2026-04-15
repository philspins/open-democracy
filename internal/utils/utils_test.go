package utils_test

import (
	"testing"

	"github.com/philspins/open-democracy/internal/utils"
)

// ── ExtractBillID ─────────────────────────────────────────────────────────────

func TestExtractBillID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.parl.ca/legisinfo/en/bill/45-1/c-47", "45-1-c-47"},
		{"https://www.parl.ca/legisinfo/en/bill/45-1/s-209", "45-1-s-209"},
		{"https://www.parl.ca/legisinfo/en/bill/45-1/C-47", "45-1-c-47"}, // normalise to lower
		{"https://www.parl.ca/legisinfo/en/bills/rss", ""},               // no bill path
		{"", ""},
	}
	for _, c := range cases {
		got := utils.ExtractBillID(c.url)
		if got != c.want {
			t.Errorf("ExtractBillID(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// ── ExtractMemberID ───────────────────────────────────────────────────────────

func TestExtractMemberID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.ourcommons.ca/Members/en/123006", "123006"},
		{"https://www.ourcommons.ca/Members/en/123006?tab=votes", "123006"},
		{"/Members/en/99999", "99999"},
		{"https://www.ourcommons.ca/en/", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := utils.ExtractMemberID(c.url)
		if got != c.want {
			t.Errorf("ExtractMemberID(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// ── DivisionID ────────────────────────────────────────────────────────────────

func TestDivisionID(t *testing.T) {
	if got := utils.DivisionID(45, 1, 892); got != "45-1-892" {
		t.Errorf("got %q, want 45-1-892", got)
	}
}

// ── BillIDFromParts ───────────────────────────────────────────────────────────

func TestBillIDFromParts(t *testing.T) {
	cases := []struct {
		parliament, session int
		billNumber          string
		want                string
	}{
		{45, 1, "C-47", "45-1-c-47"},
		{45, 1, "S-209", "45-1-s-209"},
		{45, 1, "c-47", "45-1-c-47"}, // already lowercase
		{45, 1, "  C-47  ", "45-1-c-47"}, // trims whitespace
		{45, 1, "", ""},              // empty input → empty output
	}
	for _, c := range cases {
		got := utils.BillIDFromParts(c.parliament, c.session, c.billNumber)
		if got != c.want {
			t.Errorf("BillIDFromParts(%d, %d, %q) = %q, want %q", c.parliament, c.session, c.billNumber, got, c.want)
		}
	}
}

// ── ExtractBillNumber ─────────────────────────────────────────────────────────

func TestExtractBillNumber(t *testing.T) {
	cases := []struct{ text, want string }{
		{"Motion on C-47", "C-47"},
		{"Third reading of S-209", "S-209"},
		{"Second reading of Bill C-230, An Act respecting X", "C-230"},
		{"S-5 third reading", "S-5"},
		{"Procedural motion", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := utils.ExtractBillNumber(c.text)
		if got != c.want {
			t.Errorf("ExtractBillNumber(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

// ── BillNumberFromID ──────────────────────────────────────────────────────────

func TestBillNumberFromID(t *testing.T) {
	cases := []struct{ id, want string }{
		{"45-1-c-47", "C-47"},
		{"45-1-s-209", "S-209"},
		{"45-1", ""},
	}
	for _, c := range cases {
		got := utils.BillNumberFromID(c.id)
		if got != c.want {
			t.Errorf("BillNumberFromID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// ── ParliamentSessionFromBillID ───────────────────────────────────────────────

func TestParliamentSessionFromBillID(t *testing.T) {
	p, s, ok := utils.ParliamentSessionFromBillID("45-1-c-47")
	if !ok || p != 45 || s != 1 {
		t.Errorf("got p=%d s=%d ok=%v, want 45 1 true", p, s, ok)
	}
	_, _, ok2 := utils.ParliamentSessionFromBillID("invalid")
	if ok2 {
		t.Error("expected ok=false for invalid input")
	}
}

// ── BillChamber ───────────────────────────────────────────────────────────────

func TestBillChamber(t *testing.T) {
	cases := []struct{ number, want string }{
		{"C-47", "commons"},
		{"c-47", "commons"},
		{"S-209", "senate"},
		{"s-5", "senate"},
		{"", "commons"},
	}
	for _, c := range cases {
		got := utils.BillChamber(c.number)
		if got != c.want {
			t.Errorf("BillChamber(%q) = %q, want %q", c.number, got, c.want)
		}
	}
}

// ── ParseDate ─────────────────────────────────────────────────────────────────

func TestParseDate(t *testing.T) {
	cases := []struct{ input, want string }{
		{"2024-04-03", "2024-04-03"},
		{"April 3, 2024", "2024-04-03"},
		{"3 April 2024", "2024-04-03"},
		{"Apr 3, 2024", "2024-04-03"},
		{"2024/04/03", "2024-04-03"},
		{"  2024-04-03  ", "2024-04-03"},
		{"", ""},
		{"not-a-date", ""},
	}
	for _, c := range cases {
		got := utils.ParseDate(c.input)
		if got != c.want {
			t.Errorf("ParseDate(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ── FindDateInText ────────────────────────────────────────────────────────────

func TestFindDateInText(t *testing.T) {
	cases := []struct{ text, want string }{
		{"Passed on 2024-04-03 in committee", "2024-04-03"},
		{"Reading on April 3, 2024 was agreed to", "2024-04-03"},
		{"No date here at all", ""},
	}
	for _, c := range cases {
		got := utils.FindDateInText(c.text)
		if got != c.want {
			t.Errorf("FindDateInText(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
