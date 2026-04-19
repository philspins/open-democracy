package scraper

import "testing"

func TestParseSaskatchewanBillsFromProgressText_CurrentLayout(t *testing.T) {
	text := `30th Legislature 2nd Session Progress of Bills Government Bills No. EN * Title Member 1 st Reading Royal Rec. Comm. 2nd Reading Comm. Amend Date 3 rd Reading Royal Assent Comes Into Force On 24 * The Saskatchewan Internal Trade Promotion Act Kaeding, Warren Oct 28, 2025 Nov 03, 2025 Mar 30, 2026 ECO 25 EN * Miscellaneous\ Amendment Act, 2025 Reiter, Jim Oct 28, 2025 Nov 03, 2025 Nov 17, 2025 CCA Nov 24, 2025 Nov 25, 2025 Dec 04, 2025 A-SD 38 * The Building Schools Faster Act Hindley, Everett Nov 13, 2025 Nov 17, 2025 Apr 15, 2026 IAJ 39 * The Building Schools Faster Consequential Amendment Act, 2025 / Act Hindley, Everett Nov 13, 2025 Apr 15, 2026 IAJ 50 EN * The Financial Administration Amendment Act, 2026 Reiter, Jim Mar 23, 2026 Mar 30, 2026`

	bills := parseSaskatchewanBillsFromProgressText(text, "https://example.com/progress-of-bills.pdf", 30, 2)
	if len(bills) != 5 {
		t.Fatalf("len(bills)=%d, want 5", len(bills))
	}
	if bills[0].ID != "sk-30-2-24" {
		t.Fatalf("first bill ID=%q, want sk-30-2-24", bills[0].ID)
	}
	if bills[0].Title != "The Saskatchewan Internal Trade Promotion Act" {
		t.Fatalf("first bill title=%q", bills[0].Title)
	}
	if bills[1].Title != "Miscellaneous Amendment Act, 2025" {
		t.Fatalf("second bill title=%q", bills[1].Title)
	}
	if bills[2].Number != "38" || bills[2].Title != "The Building Schools Faster Act" {
		t.Fatalf("third bill=%+v", bills[2])
	}
	if bills[4].Number != "50" || bills[4].Title != "The Financial Administration Amendment Act, 2026" {
		t.Fatalf("last bill=%+v", bills[4])
	}
}
