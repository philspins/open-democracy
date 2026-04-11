package scraper_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
}

// ── BillChamber helper (via BillNumberFromID) ─────────────────────────────────

// ── CrawlBillsRSS ─────────────────────────────────────────────────────────────

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>LEGISinfo</title>
    <item>
      <title>Budget Implementation Act</title>
      <link>https://www.parl.ca/legisinfo/en/bill/45-1/c-47</link>
      <pubDate>Wed, 03 Apr 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>Senate Bill</title>
      <link>https://www.parl.ca/legisinfo/en/bill/45-1/s-209</link>
      <pubDate>Tue, 02 Apr 2024 00:00:00 GMT</pubDate>
    </item>
    <item>
      <title>No bill ID</title>
      <link>https://www.parl.ca/legisinfo/en/bills/rss</link>
    </item>
  </channel>
</rss>`

func TestCrawlBillsRSS_ParsesEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	stubs, err := scraper.CrawlBillsRSS(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlBillsRSS: %v", err)
	}
	if len(stubs) != 2 {
		t.Errorf("len=%d, want 2", len(stubs))
	}
	if stubs[0].ID != "45-1-c-47" {
		t.Errorf("stubs[0].ID=%q want 45-1-c-47", stubs[0].ID)
	}
	if stubs[1].ID != "45-1-s-209" {
		t.Errorf("stubs[1].ID=%q want 45-1-s-209", stubs[1].ID)
	}
}

func TestCrawlBillsRSS_SkipsEntriesWithoutBillID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	stubs, _ := scraper.CrawlBillsRSS(srv.URL, srv.Client())
	for _, s := range stubs {
		if s.ID == "" {
			t.Error("expected no empty bill IDs")
		}
	}
}

// ── CrawlBillDetail ───────────────────────────────────────────────────────────

const sampleBillHTML = `<html><body>
  <div class="bill-latest-activity">2nd Reading — April 3, 2024</div>
  <div class="bill-type">Government Bill</div>
  <h2 id="SecondReading">Second Reading</h2>
  <p>April 3, 2024</p>
  <div class="bill-profile-sponsor">
    Sponsored by <a href="/Members/en/123006">Jane Doe</a>
  </div>
</body></html>`

func TestCrawlBillDetail_ParsesStatus(t *testing.T) {
	srv := newTestServer(sampleBillHTML)
	defer srv.Close()

	detail, err := scraper.CrawlBillDetail("45-1-c-47", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlBillDetail: %v", err)
	}
	want := "2nd Reading — April 3, 2024"
	if detail.CurrentStatus != want {
		t.Errorf("CurrentStatus=%q, want %q", detail.CurrentStatus, want)
	}
}

func TestCrawlBillDetail_ParsesSponsorID(t *testing.T) {
	srv := newTestServer(sampleBillHTML)
	defer srv.Close()

	detail, _ := scraper.CrawlBillDetail("45-1-c-47", srv.URL, srv.Client())
	if detail.SponsorID != "123006" {
		t.Errorf("SponsorID=%q, want 123006", detail.SponsorID)
	}
}

func TestCrawlBillDetail_ParsesBillType(t *testing.T) {
	srv := newTestServer(sampleBillHTML)
	defer srv.Close()

	detail, _ := scraper.CrawlBillDetail("45-1-c-47", srv.URL, srv.Client())
	if detail.BillType != "Government Bill" {
		t.Errorf("BillType=%q, want Government Bill", detail.BillType)
	}
}

func TestCrawlBillDetail_BuildsFullTextURL(t *testing.T) {
	srv := newTestServer(sampleBillHTML)
	defer srv.Close()

	detail, _ := scraper.CrawlBillDetail("45-1-c-47", srv.URL, srv.Client())
	want := "https://www.parl.ca/DocumentViewer/en/45-1/bill/C-47/first-reading"
	if detail.FullTextURL != want {
		t.Errorf("FullTextURL=%q, want %q", detail.FullTextURL, want)
	}
}

func TestCrawlBillDetail_ErrorOnBadURL(t *testing.T) {
	_, err := scraper.CrawlBillDetail("45-1-c-47", "http://localhost:0/no-server", http.DefaultClient)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
