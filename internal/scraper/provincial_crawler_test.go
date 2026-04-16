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
	// VP HTML fixture with one recorded division (8 yeas, 3 nays).
	vpHTML := `<!DOCTYPE html><html><body>
<p>Bill 12 second reading carried on the following division:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 8</td></tr>
<tr><td>Smith <br>Jones <br></td><td>Brown <br>Davis <br></td><td>Wilson <br>Taylor <br></td><td>Allen <br>Foster <br></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 3</td></tr>
<tr><td>Lee <br></td><td>Chen <br></td><td>Park <br></td><td></td></tr>
</table></body></html>`

	// LIMS API JSON for legislature=31, session=1 → "31st1st".
	limsJSON := `{"allParliamentaryFileAttributes":{"nodes":[{"fileName":"v260407.htm","filePath":"/ldp/31st1st/votes/","published":true,"date":"2026-04-07T00:00:00","votesAttributesByFileId":{"nodes":[{"voteNumbers":"1"}]}}]}}`

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
	// BC now uses the LIMS API. VotesURL is used as the LIMS base URL for testing.
	// legislature=31, session=1 → parliament ordinal "31st", session ordinal "1st".
	mux.HandleFunc("/pdms/votes-and-proceedings/31st1st", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(limsJSON))
	})
	mux.HandleFunc("/pdms/ldp/31st1st/votes/v260407.htm", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(vpHTML))
	})

	conn := newScraperDB(t)
	src := scraper.ProvincialSource{
		Code:     "bc",
		Province: "British Columbia",
		Chamber:  "british_columbia",
		BillsURL: srv.URL + "/bills",
		VotesURL: srv.URL,
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

