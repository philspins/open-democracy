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

func TestFetchLoPSummary_ExtractsSummary(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body><article>  This is a LoP summary. </article></body></html>`))
	}))
	defer ts.Close()

	oldBase := lopBaseURL
	lopBaseURL = ts.URL
	defer func() { lopBaseURL = oldBase }()

	summary, err := FetchLoPSummary(context.Background(), "C-47")
	if err != nil {
		t.Fatalf("FetchLoPSummary returned error: %v", err)
	}
	if summary != "This is a LoP summary." {
		t.Fatalf("unexpected summary: got %q", summary)
	}
}

func TestFetchLoPSummary_NoMatchReturnsEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body><div>No matching summary nodes</div></body></html>`))
	}))
	defer ts.Close()

	oldBase := lopBaseURL
	lopBaseURL = ts.URL
	defer func() { lopBaseURL = oldBase }()

	summary, err := FetchLoPSummary(context.Background(), "C-1")
	if err != nil {
		t.Fatalf("FetchLoPSummary returned error: %v", err)
	}
	if summary != "" {
		t.Fatalf("expected empty summary, got %q", summary)
	}
}

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

	_, err = db.Exec(`
		INSERT INTO bills (id, number, summary_lop, introduced_date) VALUES
		('bill-1', 'C-1', NULL, '2026-01-01'),
		('bill-2', 'C-2', NULL, '2026-01-02')
	`)
	if err != nil {
		t.Fatalf("insert bills: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		keyword := q.Get("keyword")
		w.WriteHeader(http.StatusOK)
		if keyword == "C-1" {
			_, _ = w.Write([]byte(`<html><body><article>Summary for C-1</article></body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><body><div>No summary available</div></body></html>`))
	}))
	defer ts.Close()

	oldBase := lopBaseURL
	oldDelay := lopRequestDelay
	lopBaseURL = ts.URL
	lopRequestDelay = 0
	defer func() {
		lopBaseURL = oldBase
		lopRequestDelay = oldDelay
	}()

	count, err := DownloadLoPSummaries(context.Background(), db)
	if err != nil {
		t.Fatalf("DownloadLoPSummaries returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("unexpected downloaded count: got %d, want 1", count)
	}

	var bill1Summary, bill2Summary sql.NullString
	if err := db.QueryRow(`SELECT summary_lop FROM bills WHERE id = 'bill-1'`).Scan(&bill1Summary); err != nil {
		t.Fatalf("query bill-1 summary: %v", err)
	}
	if err := db.QueryRow(`SELECT summary_lop FROM bills WHERE id = 'bill-2'`).Scan(&bill2Summary); err != nil {
		t.Fatalf("query bill-2 summary: %v", err)
	}

	if !bill1Summary.Valid || bill1Summary.String != "Summary for C-1" {
		t.Fatalf("bill-1 summary mismatch: got %#v", bill1Summary)
	}
	if bill2Summary.Valid {
		t.Fatalf("expected bill-2 summary to remain NULL, got %#v", bill2Summary)
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
	_, err = DownloadLoPSummaries(ctx, db)
	if err == nil {
		t.Fatalf("expected error from canceled context")
	}

}
