package main

// Tests for the domain crawler helper functions in cmd/crawler/main.go.
//
// Each crawler helper accepts a *sql.DB and a *http.Client, making them
// straightforwardly testable with a temporary SQLite database and an
// httptest.Server.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/summarizer"
)

// ── shared test helpers ───────────────────────────────────────────────────────

// newDB returns a fresh in-memory SQLite database for use within a single test.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// serve returns an httptest.Server that always responds with body.
func serve(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(body))
	}))
}

// noDelay is a zero-length delay for tests so they finish quickly.
const noDelay = 0 * time.Millisecond

// ── crawlCalendar ─────────────────────────────────────────────────────────────

const calendarHTML = `<html><body>
  <table>
    <tr>
      <td class="sitting" data-date="2024-04-03">3</td>
      <td class="sitting" data-date="2024-04-04">4</td>
    </tr>
  </table>
</body></html>`

func TestCrawlCalendar_PersistsDates(t *testing.T) {
	srv := serve(calendarHTML)
	defer srv.Close()

	conn := newDB(t)
	if err := crawlCalendar(conn, srv.Client(), noDelay, srv.URL); err != nil {
		t.Fatalf("crawlCalendar: %v", err)
	}

	dates, err := db.SittingDates(conn, scraper.CurrentParliament, scraper.CurrentSession)
	if err != nil {
		t.Fatalf("SittingDates: %v", err)
	}
	if len(dates) < 2 {
		t.Errorf("expected ≥2 sitting dates, got %d", len(dates))
	}
}

func TestCrawlCalendar_ReturnsErrorOnBadServer(t *testing.T) {
	conn := newDB(t)
	err := crawlCalendar(conn, http.DefaultClient, noDelay, "http://localhost:0/no-server")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ── crawlBills ────────────────────────────────────────────────────────────────

// billsRSSBody is a minimal RSS feed with one valid bill entry.
const billsRSSBody = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>LEGISinfo</title>
    <item>
      <title>Budget Implementation Act</title>
      <link>https://www.parl.ca/legisinfo/en/bill/45-1/c-47</link>
      <pubDate>Wed, 03 Apr 2024 00:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

// billDetailBody is a minimal bill detail page.
const billDetailBody = `<html><body>
  <div class="bill-latest-activity">2nd Reading</div>
  <div class="bill-type">Government Bill</div>
</body></html>`

func TestCrawlBills_PersistsBill(t *testing.T) {
	// We need two different responses: RSS feed and detail page.
	// Use a mux that serves RSS to /rss and the detail page to everything else.
	mux := http.NewServeMux()
	mux.HandleFunc("/rss", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(billsRSSBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(billDetailBody))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn := newDB(t)
	if err := crawlBills(conn, srv.Client(), noDelay, srv.URL+"/rss", nil); err != nil {
		t.Fatalf("crawlBills: %v", err)
	}

	// The bill should now be in the DB
	var count int
	conn.QueryRow(`SELECT COUNT(1) FROM bills WHERE id='45-1-c-47'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected bill 45-1-c-47 in DB, count=%d", count)
	}
}

func TestCrawlBills_ReturnsErrorOnBadRSS(t *testing.T) {
	conn := newDB(t)
	err := crawlBills(conn, http.DefaultClient, noDelay, "http://localhost:0/no-server", nil)
	if err == nil {
		t.Error("expected error for unreachable RSS feed")
	}
}

func TestCrawlBills_EmitsSummaryRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rss", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(billsRSSBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(billDetailBody))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn := newDB(t)
	ch := make(chan summarizer.BillSummaryRequest, 4)
	if err := crawlBills(conn, srv.Client(), noDelay, srv.URL+"/rss", ch); err != nil {
		t.Fatalf("crawlBills: %v", err)
	}

	select {
	case req := <-ch:
		if req.BillID != "45-1-c-47" {
			t.Fatalf("unexpected bill id: %s", req.BillID)
		}
		if req.FullTextURL == "" {
			t.Fatal("expected non-empty FullTextURL in summary request")
		}
	default:
		t.Fatal("expected a summary request to be emitted")
	}
}

// ── crawlMembers ──────────────────────────────────────────────────────────────

const membersListBody = `<html><body>
  <div class="ce-mip-mp-tile">
    <a href="/Members/en/111">
      <span class="ce-mip-mp-name">Jane Doe</span>
    </a>
    <span class="ce-mip-mp-party">Liberal</span>
    <span class="ce-mip-mp-constituency">Ottawa Centre</span>
    <span class="ce-mip-mp-province">Ontario</span>
  </div>
</body></html>`

const memberProfileBody = `<html><body>
  <h1 class="ce-mip-mp-name">Jane Doe</h1>
  <span class="ce-mip-mp-party">Liberal</span>
  <span class="ce-mip-mp-constituency">Ottawa Centre</span>
  <span class="ce-mip-mp-province">Ontario</span>
  <a href="mailto:jane.doe@parl.gc.ca">jane.doe@parl.gc.ca</a>
</body></html>`

func TestCrawlMembers_PersistsMember(t *testing.T) {
	srv := serve(membersListBody + memberProfileBody)
	defer srv.Close()

	conn := newDB(t)
	if err := crawlMembers(conn, srv.Client(), noDelay, srv.URL, srv.URL); err != nil {
		t.Fatalf("crawlMembers: %v", err)
	}

	var count int
	conn.QueryRow(`SELECT COUNT(1) FROM members WHERE id='111'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected member 111 in DB, count=%d", count)
	}
}

func TestCrawlMembers_ReturnsErrorOnBadServer(t *testing.T) {
	conn := newDB(t)
	err := crawlMembers(conn, http.DefaultClient, noDelay, "http://localhost:0/no-server", "")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ── crawlVotes ────────────────────────────────────────────────────────────────

const votesIndexBody = `<html><body>
  <table class="table">
    <thead><tr>
      <th>#</th><th>Date</th><th>Description</th>
      <th>Yeas</th><th>Nays</th><th>Result</th>
    </tr></thead>
    <tbody>
      <tr>
        <td>892</td>
        <td>April 3, 2024</td>
        <td>Motion on C-47</td>
        <td>172</td>
        <td>148</td>
        <td>Agreed to</td>
      </tr>
    </tbody>
  </table>
</body></html>`

func TestCrawlVotes_PersistsDivision(t *testing.T) {
	srv := serve(votesIndexBody)
	defer srv.Close()

	conn := newDB(t)
	if err := crawlVotes(conn, srv.Client(), noDelay, srv.URL); err != nil {
		t.Fatalf("crawlVotes: %v", err)
	}

	var count int
	conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id='45-1-892'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected division 45-1-892 in DB, count=%d", count)
	}
}

func TestCrawlVotes_ReturnsErrorOnBadServer(t *testing.T) {
	conn := newDB(t)
	err := crawlVotes(conn, http.DefaultClient, noDelay, "http://localhost:0/no-server")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ── crawlSenate ───────────────────────────────────────────────────────────────

const senateVotesBody = `<html><body>
  <table>
    <thead><tr>
      <th>#</th><th>Date</th><th>Description</th>
      <th>Yeas</th><th>Nays</th><th>Result</th>
    </tr></thead>
    <tbody>
      <tr>
        <td>42</td>
        <td>April 4, 2024</td>
        <td>Motion on S-209</td>
        <td>58</td>
        <td>22</td>
        <td>Agreed to</td>
      </tr>
    </tbody>
  </table>
</body></html>`

func TestCrawlSenate_PersistsDivision(t *testing.T) {
	srv := serve(senateVotesBody)
	defer srv.Close()

	conn := newDB(t)
	if err := crawlSenate(conn, srv.Client(), noDelay, srv.URL); err != nil {
		t.Fatalf("crawlSenate: %v", err)
	}

	var count int
	conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id='senate-45-1-42'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected senate division senate-45-1-42 in DB, count=%d", count)
	}
}

func TestCrawlSenate_ReturnsErrorOnBadServer(t *testing.T) {
	conn := newDB(t)
	err := crawlSenate(conn, http.DefaultClient, noDelay, "http://localhost:0/no-server")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

// ── runParallel ───────────────────────────────────────────────────────────────

func TestRunParallel_RunsAllFunctions(t *testing.T) {
	const n = 5
	var mu sync.Mutex
	called := make(map[int]bool)

	fns := make([]func(), n)
	for i := range fns {
		fns[i] = func() {
			mu.Lock()
			called[i] = true
			mu.Unlock()
		}
	}

	runParallel(3, fns)

	for i := range n {
		if !called[i] {
			t.Errorf("function %d was not called", i)
		}
	}
}

func TestRunParallel_RespectsParallelismLimit(t *testing.T) {
	const parallelism = 2
	const total = 6

	var mu sync.Mutex
	maxConcurrent := 0
	current := 0

	fns := make([]func(), total)
	for i := range fns {
		fns[i] = func() {
			mu.Lock()
			current++
			if current > maxConcurrent {
				maxConcurrent = current
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond) // simulate work

			mu.Lock()
			current--
			mu.Unlock()
		}
	}

	runParallel(parallelism, fns)

	if maxConcurrent > parallelism {
		t.Errorf("max concurrent goroutines=%d, want ≤%d", maxConcurrent, parallelism)
	}
}

func TestRunParallel_SerialWhenParallelismOne(t *testing.T) {
	order := make([]int, 0, 3)
	var mu sync.Mutex

	fns := []func(){
		func() { mu.Lock(); order = append(order, 1); mu.Unlock() },
		func() { mu.Lock(); order = append(order, 2); mu.Unlock() },
		func() { mu.Lock(); order = append(order, 3); mu.Unlock() },
	}

	runParallel(1, fns)

	if len(order) != 3 {
		t.Errorf("expected 3 calls, got %d", len(order))
	}
}

func TestRunParallel_NilParallelismDefaultsToSerial(t *testing.T) {
	var called int32
	fns := []func(){
		func() { atomic.AddInt32(&called, 1) },
		func() { atomic.AddInt32(&called, 1) },
	}
	runParallel(0, fns) // 0 treated as 1
	if atomic.LoadInt32(&called) != 2 {
		t.Errorf("expected 2 calls, got %d", called)
	}
}

func TestRunParallel_EmptyFnsNoOp(t *testing.T) {
	// Should not panic and should return immediately.
	runParallel(5, nil)
}

// ── defaultParallelism ────────────────────────────────────────────────────────

func TestDefaultParallelism_DefaultFive(t *testing.T) {
	t.Setenv("CRAWLER_PARALLELISM", "")
	got := defaultParallelism()
	if got != 5 {
		t.Errorf("defaultParallelism()=%d, want 5", got)
	}
}

func TestDefaultParallelism_ReadsEnvVar(t *testing.T) {
	t.Setenv("CRAWLER_PARALLELISM", "3")
	got := defaultParallelism()
	if got != 3 {
		t.Errorf("defaultParallelism()=%d, want 3", got)
	}
}

func TestDefaultParallelism_IgnoresInvalidEnvVar(t *testing.T) {
	t.Setenv("CRAWLER_PARALLELISM", "not-a-number")
	got := defaultParallelism()
	if got != 5 {
		t.Errorf("defaultParallelism()=%d, want 5 for invalid env", got)
	}
}

func TestDefaultParallelism_IgnoresZeroEnvVar(t *testing.T) {
	t.Setenv("CRAWLER_PARALLELISM", "0")
	got := defaultParallelism()
	if got != 5 {
		t.Errorf("defaultParallelism()=%d, want 5 for zero env", got)
	}
}

func TestDefaultParallelism_IgnoresNegativeEnvVar(t *testing.T) {
	t.Setenv("CRAWLER_PARALLELISM", "-2")
	got := defaultParallelism()
	if got != 5 {
		t.Errorf("defaultParallelism()=%d, want 5 for negative env", got)
	}
}

func TestRunFrequentVoteCheck_SkipsWhenNotSitting(t *testing.T) {
	conn := newDB(t)
	// No sitting dates in the DB → should skip votes crawl and return nil.
	if err := runFrequentVoteCheck(conn, http.DefaultClient, noDelay, ""); err != nil {
		t.Errorf("expected nil (skip), got %v", err)
	}
}

func TestRunFrequentVoteCheck_CrawlsVotesWhenSitting(t *testing.T) {
	srv := serve(votesIndexBody)
	defer srv.Close()

	conn := newDB(t)
	// Insert today's date as a sitting date so the check proceeds.
	today := time.Now().UTC().Format("2006-01-02")
	db.UpsertSittingDate(conn, scraper.CurrentParliament, scraper.CurrentSession, today)

	if err := runFrequentVoteCheck(conn, srv.Client(), noDelay, srv.URL); err != nil {
		t.Fatalf("runFrequentVoteCheck: %v", err)
	}

	// If votes were crawled, the division should now exist.
	var count int
	conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id='45-1-892'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected division to be stored when parliament is sitting, count=%d", count)
	}
}
