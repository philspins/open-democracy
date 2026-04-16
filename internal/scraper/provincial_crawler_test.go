package scraper_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/scraper"
)

const noDelay = 0 * time.Millisecond

func newScraperDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dbConn, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbConn.Close() })
	return dbConn
}

func TestCrawlProvinceSource_PersistsBillsAndDivisions(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/bills", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
      <h2>31st Legislature 1st Session</h2>
      <a href="/archives/99th-parliament/1st-session">Archive 99th Parliament, 1st Session</a>
      <a href="/bill/12">Bill 12 - Test Act</a>
    </body></html>`))
	})
	mux.HandleFunc("/votes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
      <h2>31st Legislature 1st Session</h2>
      <a href="/votes/2026-04-07">Votes and Proceedings</a>
    </body></html>`))
	})
	mux.HandleFunc("/votes/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
      <h3>Bill 12 second reading</h3>
      <table><tr><td>Yeas: 8</td><td>Nays: 3</td></tr></table>
    </body></html>`))
	})

	conn := newScraperDB(t)
	// Use "bc" instead of "ab": BC still falls back to the generic HTML scraper,
	// while AB now requires docs.assembly.ab.ca PDF links.
	src := scraper.ProvincialSource{
		Code:     "bc",
		Province: "British Columbia",
		Chamber:  "british_columbia",
		BillsURL: srv.URL + "/bills",
		VotesURL: srv.URL + "/votes",
	}

	if err := scraper.CrawlProvinceSource(conn, srv.Client(), noDelay, src, nil); err != nil {
		t.Fatalf("CrawlProvinceSource: %v", err)
	}

	var billCount int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM bills WHERE id='bc-31-1-12'`).Scan(&billCount); err != nil {
		t.Fatalf("bill count query: %v", err)
	}
	if billCount != 1 {
		t.Fatalf("expected bill bc-31-1-12, count=%d", billCount)
	}

	var divCount int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE chamber='british_columbia'`).Scan(&divCount); err != nil {
		t.Fatalf("division count query: %v", err)
	}
	if divCount == 0 {
		t.Fatal("expected at least one british_columbia division")
	}
}

