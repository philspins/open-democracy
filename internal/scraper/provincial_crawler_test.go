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

// TestCrawlProvinceSource_FallsBackToPreviousSessionWhenDBEmpty verifies that when
// the current session returns 0 divisions and the DB has no divisions for the
// province, CrawlProvinceSource retries with (legislature, session-1) and stores
// the previous session's data.
func TestCrawlProvinceSource_FallsBackToPreviousSessionWhenDBEmpty(t *testing.T) {
	vpHTML := `<!DOCTYPE html><html><body>
<p>Bill 5 carried on the following division:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 4</td></tr>
<tr><td>Alpha <br>Beta <br></td><td>Gamma <br>Delta <br></td><td></td><td></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 2</td></tr>
<tr><td>Zeta <br></td><td>Eta <br></td><td></td><td></td></tr>
</table></body></html>`

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Bills page detects session 2 but returns no bill links.
	mux.HandleFunc("/bills", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>
		  <h2>5th Legislature 2nd Session</h2>
		</body></html>`))
	})

	// Votes: first request (session 2) returns no divisions; second (session 1 fallback) has data.
	voteCalls := 0
	mux.HandleFunc("/votes", func(w http.ResponseWriter, _ *http.Request) {
		voteCalls++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if voteCalls <= 1 {
			_, _ = w.Write([]byte(`<html><body><p>No recorded divisions.</p></body></html>`))
		} else {
			_, _ = w.Write([]byte(vpHTML))
		}
	})

	conn := newScraperDB(t)
	src := scraper.ProvincialSource{
		Code:     "nb",
		Province: "New Brunswick",
		Chamber:  "new_brunswick",
		BillsURL: srv.URL + "/bills",
		VotesURL: srv.URL + "/votes",
	}

	if err := scraper.CrawlProvinceSource(conn, srv.Client(), noDelay, src, nil); err != nil {
		t.Fatalf("CrawlProvinceSource: %v", err)
	}

	var divCount int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id LIKE 'nb-%'`).Scan(&divCount); err != nil {
		t.Fatalf("division count query: %v", err)
	}
	if divCount == 0 {
		t.Fatal("expected at least one NB division from the previous-session fallback, got 0")
	}
}

