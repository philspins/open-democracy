package summarizer

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestDownloadLoPSummaries_StoresOnlyFoundSummaries(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:lop_test_store?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE bills (
			id TEXT PRIMARY KEY,
			number TEXT,
			summary_lop TEXT,
			introduced_date TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create bills table: %v", err)
	}

	// Bill IDs use the "parliament-session-bill" format so ParliamentSessionFromBillID
	// can extract parliament=45, session=1.
	_, err = db.Exec(`
		INSERT INTO bills (id, number, summary_lop, introduced_date) VALUES
		('45-1-c-1', 'C-1', NULL, '2026-01-01'),
		('45-1-c-2', 'C-2', NULL, '2026-01-02')
	`)
	if err != nil {
		t.Fatalf("insert bills: %v", err)
	}

	// CrawlLibraryOfParliamentSummary fetches ?ls=C1&Parl=45&Ses=1 and looks for
	// ".views-field-body .field-content p, .field-item p, ..." selectors.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		ls := q.Get("ls")
		w.WriteHeader(http.StatusOK)
		if ls == "C1" {
			_, _ = w.Write([]byte(`<html><body><div class="field-item"><p>Summary for C-1</p></div></body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><body><div>No summary available</div></body></html>`))
	}))
	defer ts.Close()

	// Point CrawlLibraryOfParliamentSummary at the test server via a custom transport.
	client := &http.Client{
		Transport: &rewriteHostTransport{target: ts.URL, base: http.DefaultTransport},
	}

	oldDelay := lopRequestDelay
	lopRequestDelay = 0
	defer func() { lopRequestDelay = oldDelay }()

	count, err := DownloadLoPSummaries(context.Background(), db, client)
	if err != nil {
		t.Fatalf("DownloadLoPSummaries returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("unexpected downloaded count: got %d, want 1", count)
	}

	var bill1Summary, bill2Summary sql.NullString
	if err := db.QueryRow(`SELECT summary_lop FROM bills WHERE id = '45-1-c-1'`).Scan(&bill1Summary); err != nil {
		t.Fatalf("query bill C-1 summary: %v", err)
	}
	if err := db.QueryRow(`SELECT summary_lop FROM bills WHERE id = '45-1-c-2'`).Scan(&bill2Summary); err != nil {
		t.Fatalf("query bill C-2 summary: %v", err)
	}

	if !bill1Summary.Valid || bill1Summary.String != "Summary for C-1" {
		t.Fatalf("bill C-1 summary mismatch: got %#v", bill1Summary)
	}
	if bill2Summary.Valid {
		t.Fatalf("expected bill C-2 summary to remain NULL, got %#v", bill2Summary)
	}
}

func TestDownloadLoPSummaries_RespectsContextCancellation(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:lop_test_cancel?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE bills (
			id TEXT PRIMARY KEY,
			number TEXT,
			summary_lop TEXT,
			introduced_date TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create bills table: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return quickly with context cancellation on query.
	_, err = DownloadLoPSummaries(ctx, db, nil)
	if err == nil {
		t.Fatalf("expected error from canceled context")
	}
}

// rewriteHostTransport redirects all requests to a fixed base URL (for tests).
type rewriteHostTransport struct {
	target string
	base   http.RoundTripper
}

func (t *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	base, _ := url.Parse(t.target)
	cloned.URL.Scheme = base.Scheme
	cloned.URL.Host = base.Host
	return t.base.RoundTrip(cloned)
}
