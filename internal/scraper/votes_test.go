package scraper_test

import (
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

// ── CrawlVotesIndex ───────────────────────────────────────────────────────────

// sampleVotesHTML mirrors the actual ourcommons.ca recorded-votes table structure:
// Col 0: vote number | Col 1: bill type (optional) | Col 2: description
// Col 3: "Yeas / Nays / Paired" | Col 4: result (with icon) | Col 5: date
const sampleVotesHTML = `<html><body>
  <table class="table">
    <thead><tr>
      <th>#</th><th>Vote type</th><th>Description</th>
      <th>Votes</th><th>Result</th><th>Date</th>
    </tr></thead>
    <tbody>
      <tr>
        <td><a href="/Members/en/votes/45/1/892">No. 892</a></td>
        <td>House Government Bill</td>
        <td>Motion on C-47</td>
        <td>172 / 148 / 5</td>
        <td><i class="icon"></i> Agreed to</td>
        <td>Wednesday, April 3, 2024</td>
      </tr>
      <tr>
        <td><a href="/Members/en/votes/45/1/891">No. 891</a></td>
        <td></td>
        <td>Motion on S-209</td>
        <td>100 / 90 / 0</td>
        <td><i class="icon"></i> Negatived</td>
        <td>Tuesday, April 2, 2024</td>
      </tr>
    </tbody>
  </table>
</body></html>`

func TestCrawlVotesIndex_ParsesRows(t *testing.T) {
	srv := newTestServer(sampleVotesHTML)
	defer srv.Close()

	divs, err := scraper.CrawlVotesIndex(srv.URL, 45, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlVotesIndex: %v", err)
	}
	if len(divs) != 2 {
		t.Errorf("len=%d, want 2", len(divs))
	}
}

func TestCrawlVotesIndex_ParsesFirstDivision(t *testing.T) {
	srv := newTestServer(sampleVotesHTML)
	defer srv.Close()

	divs, _ := scraper.CrawlVotesIndex(srv.URL, 45, 1, srv.Client())
	d := divs[0]
	if d.ID != "45-1-892" {
		t.Errorf("ID=%q want 45-1-892", d.ID)
	}
	if d.Number != 892 {
		t.Errorf("Number=%d want 892", d.Number)
	}
	if d.Yeas != 172 || d.Nays != 148 {
		t.Errorf("Yeas=%d Nays=%d want 172/148", d.Yeas, d.Nays)
	}
	if d.Paired != 5 {
		t.Errorf("Paired=%d want 5", d.Paired)
	}
	if d.Result != "Agreed to" {
		t.Errorf("Result=%q want Agreed to", d.Result)
	}
	if d.Date != "2024-04-03" {
		t.Errorf("Date=%q want 2024-04-03", d.Date)
	}
	if d.Chamber != "commons" {
		t.Errorf("Chamber=%q want commons", d.Chamber)
	}
}

func TestCrawlVotesIndex_ExtractsBillNumberFromDescription(t *testing.T) {
	srv := newTestServer(sampleVotesHTML)
	defer srv.Close()

	divs, _ := scraper.CrawlVotesIndex(srv.URL, 45, 1, srv.Client())
	// First row: description "Motion on C-47" → BillNumber should be "C-47"
	if divs[0].BillNumber != "C-47" {
		t.Errorf("divs[0].BillNumber=%q want C-47", divs[0].BillNumber)
	}
	// Second row: description "Motion on S-209" → BillNumber should be "S-209"
	if divs[1].BillNumber != "S-209" {
		t.Errorf("divs[1].BillNumber=%q want S-209", divs[1].BillNumber)
	}
}

func TestCrawlVotesIndex_ErrorOnBadServer(t *testing.T) {
	_, err := scraper.CrawlVotesIndex("http://localhost:0/no-server", 45, 1, nil)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestCrawlVotesIndex_ErrorWhenNoTable(t *testing.T) {
	srv := newTestServer("<html><body><p>No table</p></body></html>")
	defer srv.Close()

	_, err := scraper.CrawlVotesIndex(srv.URL, 45, 1, srv.Client())
	if err == nil {
		t.Error("expected error when no table found")
	}
}

// ── CrawlDivisionDetail ───────────────────────────────────────────────────────

const sampleDivisionHTML = `<html><body>
  <div class="vote-yea">
    <ul>
      <li class="member-name"><a href="/Members/en/111">Alice Smith</a></li>
      <li class="member-name"><a href="/Members/en/222">Bob Jones</a></li>
    </ul>
  </div>
  <div class="vote-nay">
    <ul>
      <li class="member-name"><a href="/Members/en/333">Carol Brown</a></li>
    </ul>
  </div>
</body></html>`

func TestCrawlDivisionDetail_ParsesYeaVotes(t *testing.T) {
	srv := newTestServer(sampleDivisionHTML)
	defer srv.Close()

	votes, err := scraper.CrawlDivisionDetail("45-1-892", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlDivisionDetail: %v", err)
	}
	var yeas []scraper.MemberVote
	for _, v := range votes {
		if v.Vote == "Yea" {
			yeas = append(yeas, v)
		}
	}
	if len(yeas) != 2 {
		t.Errorf("len(yeas)=%d want 2", len(yeas))
	}
}

func TestCrawlDivisionDetail_ParsesNayVotes(t *testing.T) {
	srv := newTestServer(sampleDivisionHTML)
	defer srv.Close()

	votes, _ := scraper.CrawlDivisionDetail("45-1-892", srv.URL, srv.Client())
	var nays []scraper.MemberVote
	for _, v := range votes {
		if v.Vote == "Nay" {
			nays = append(nays, v)
		}
	}
	if len(nays) != 1 || nays[0].MemberID != "333" {
		t.Errorf("nay member_id mismatch: %+v", nays)
	}
}

func TestCrawlDivisionDetail_AllHaveDivisionID(t *testing.T) {
	srv := newTestServer(sampleDivisionHTML)
	defer srv.Close()

	votes, _ := scraper.CrawlDivisionDetail("45-1-892", srv.URL, srv.Client())
	for _, v := range votes {
		if v.DivisionID != "45-1-892" {
			t.Errorf("DivisionID=%q want 45-1-892", v.DivisionID)
		}
	}
}

// ── CrawlSittingCalendar ──────────────────────────────────────────────────────

const sampleCalendarHTML = `<html><body>
  <table>
    <tbody>
      <tr>
        <td class="sitting" data-date="2024-04-03">3</td>
        <td class="sitting" data-date="2024-04-04">4</td>
        <td data-date="2024-04-06">6</td>
      </tr>
    </tbody>
  </table>
</body></html>`

func TestCrawlSittingCalendar_ParsesDates(t *testing.T) {
	srv := newTestServer(sampleCalendarHTML)
	defer srv.Close()

	dates, err := scraper.CrawlSittingCalendar(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlSittingCalendar: %v", err)
	}
	found := make(map[string]bool)
	for _, d := range dates {
		found[d] = true
	}
	if !found["2024-04-03"] || !found["2024-04-04"] {
		t.Errorf("expected 2024-04-03 and 2024-04-04, got %v", dates)
	}
}

func TestCrawlSittingCalendar_Sorted(t *testing.T) {
	srv := newTestServer(sampleCalendarHTML)
	defer srv.Close()

	dates, _ := scraper.CrawlSittingCalendar(srv.URL, srv.Client())
	for i := 1; i < len(dates); i++ {
		if dates[i] < dates[i-1] {
			t.Errorf("dates not sorted: %v", dates)
		}
	}
}

// ── ParliamentIsSitting ───────────────────────────────────────────────────────

func TestParliamentIsSitting_TrueWhenInList(t *testing.T) {
	dates := []string{"2024-04-03", "2024-04-04"}
	if !scraper.ParliamentIsSitting(dates, "2024-04-03") {
		t.Error("expected true")
	}
}

func TestParliamentIsSitting_FalseWhenNotInList(t *testing.T) {
	dates := []string{"2024-04-03", "2024-04-04"}
	if scraper.ParliamentIsSitting(dates, "2024-04-05") {
		t.Error("expected false")
	}
}

func TestParliamentIsSitting_FalseForEmpty(t *testing.T) {
	if scraper.ParliamentIsSitting(nil, "2024-04-03") {
		t.Error("expected false for empty list")
	}
}
