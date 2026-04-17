package summarizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestParseSummaryJSON_FencedJSON(t *testing.T) {
	raw := "```json\n{\"one_sentence\":\"One line\",\"plain_summary\":\"Two lines\",\"key_changes\":[\"a\"],\"who_is_affected\":[\"b\"],\"notable_considerations\":[],\"estimated_cost\":\"Not specified\",\"category\":\"Other\"}\n```"

	got, err := parseSummaryJSON(raw)
	if err != nil {
		t.Fatalf("parseSummaryJSON returned error: %v", err)
	}
	if got.OneSentence != "One line" {
		t.Fatalf("unexpected one_sentence: %q", got.OneSentence)
	}
	if got.Category != "Other" {
		t.Fatalf("unexpected category: %q", got.Category)
	}
}

func TestParseSummaryJSON_MixedTextWithJSONObject(t *testing.T) {
	raw := "Here is your result:\n{\"one_sentence\":\"One line\",\"plain_summary\":\"Two lines\",\"key_changes\":[\"a\"],\"who_is_affected\":[\"b\"],\"notable_considerations\":[\"c\"],\"estimated_cost\":\"Not specified\",\"category\":\"Housing\"}\nThanks!"

	got, err := parseSummaryJSON(raw)
	if err != nil {
		t.Fatalf("parseSummaryJSON returned error: %v", err)
	}
	if got.Category != "Housing" {
		t.Fatalf("unexpected category: %q", got.Category)
	}
	if len(got.NotableConsiderations) != 1 {
		t.Fatalf("unexpected notable_considerations length: %d", len(got.NotableConsiderations))
	}
}

func TestParseSummaryJSON_InvalidPayload(t *testing.T) {
	if _, err := parseSummaryJSON("```\nnot json\n```"); err == nil {
		t.Fatal("expected error for invalid summary payload")
	}
}

func TestParseSummaryResult(t *testing.T) {
	// Create a fake JSON summary like Claude would return
	fakeResult := SummaryResult{
		OneSentence:           "This bill establishes new housing regulations.",
		PlainSummary:          "This bill creates a framework for affordable housing in Canada...",
		KeyChanges:            []string{"Increases housing tax credit", "Requires landlord transparency"},
		WhoIsAffected:         []string{"Renters", "Landlords", "Government"},
		NotableConsiderations: []string{"Citizens must give up privacy rights", "Excludes rural municipalities from some requirements"},
		EstimatedCost:         "$2 billion over 10 years",
		Category:              "Housing",
		BillID:                "45-1-C-123",
		GeneratedAt:           "2026-04-11T00:00:00Z",
		Model:                 claudeModel,
	}

	// Marshal to JSON to test round-trip
	jsonData, err := json.Marshal(fakeResult)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Verify we can unmarshal it back
	var parsed SummaryResult
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed.OneSentence != fakeResult.OneSentence {
		t.Errorf("OneSentence mismatch: got %q, want %q", parsed.OneSentence, fakeResult.OneSentence)
	}

	if len(parsed.KeyChanges) != 2 {
		t.Errorf("KeyChanges length mismatch: got %d, want 2", len(parsed.KeyChanges))
	}

	if parsed.Category != "Housing" {
		t.Errorf("Category mismatch: got %q, want %q", parsed.Category, "Housing")
	}

	if len(parsed.NotableConsiderations) != 2 {
		t.Errorf("NotableConsiderations length mismatch: got %d, want 2", len(parsed.NotableConsiderations))
	}
}

func TestParseAISummaryEmpty(t *testing.T) {
	// ParseAISummary should handle empty strings gracefully
	tests := []string{"", "   ", "not json"}

	for _, test := range tests {
		result := &SummaryResult{
			OneSentence:  "",
			PlainSummary: "",
		}
		json.Unmarshal([]byte(test), result)
		// Should not panic or error, just return zero values
	}
}

func TestSummaryResultStructure(t *testing.T) {
	// Verify the SummaryResult struct has all expected fields
	sr := SummaryResult{
		OneSentence:           "test",
		PlainSummary:          "test",
		KeyChanges:            []string{"test"},
		WhoIsAffected:         []string{"test"},
		NotableConsiderations: []string{"test"},
		EstimatedCost:         "test",
		Category:              "Housing",
		BillID:                "45-1-C-1",
		GeneratedAt:           "2026-04-11T00:00:00Z",
		Model:                 claudeModel,
	}

	if sr.BillID == "" {
		t.Error("BillID should not be empty")
	}

	if sr.Category == "" {
		t.Error("Category should not be empty")
	}

	if len(sr.KeyChanges) != 1 {
		t.Error("KeyChanges should have one item")
	}

	if len(sr.NotableConsiderations) != 1 {
		t.Error("NotableConsiderations should have one item")
	}
}

func TestSummarizeBillsFromChannel_Clears404FullTextURL(t *testing.T) {
	// Serve HTTP 404 for any request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Build a minimal in-memory SQLite DB with the bills table.
	db, err := sql.Open("sqlite3", "file:clear404?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE bills (
		id TEXT PRIMARY KEY,
		number TEXT,
		title TEXT,
		full_text_url TEXT,
		summary_ai TEXT,
		summary_lop TEXT,
		last_activity_date TEXT
	)`); err != nil {
		t.Fatalf("create bills: %v", err)
	}

	billURL := srv.URL + "/bill/S-1/first-reading"
	if _, err := db.Exec(
		`INSERT INTO bills (id, number, title, full_text_url) VALUES ('45-1-s-1','S-1','Pro-forma Senate bill',?)`,
		billURL,
	); err != nil {
		t.Fatalf("insert bill: %v", err)
	}

	// Feed one request through the channel pipeline.
	ch := make(chan BillSummaryRequest, 1)
	ch <- BillSummaryRequest{
		BillID:      "45-1-s-1",
		BillTitle:   "Pro-forma Senate bill",
		FullTextURL: billURL,
	}
	close(ch)

	SummarizeBillsFromChannel(t.Context(), db, ch) //nolint:errcheck

	// full_text_url must now be empty.
	var storedURL string
	db.QueryRow(`SELECT COALESCE(full_text_url,'') FROM bills WHERE id='45-1-s-1'`).Scan(&storedURL)
	if storedURL != "" {
		t.Errorf("expected full_text_url to be cleared after 404, got %q", storedURL)
	}
}

func TestFetchBillText_FallsBackForDeeplyNestedHTML(t *testing.T) {
	const deepNesting = 600 // exceeds x/net/html parser stack limit (512)
	deep := strings.Repeat("<div>", deepNesting) + "Bill body text" + strings.Repeat("</div>", deepNesting)
	html := `<html><head><style>.x{display:none}</style></head><body><script>ignored()</script>` + deep + `</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	text, err := fetchBillText(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchBillText returned error: %v", err)
	}
	if !strings.Contains(text, "Bill body text") {
		t.Fatalf("expected extracted text to contain bill text, got %q", text)
	}
	if strings.Contains(text, "ignored()") {
		t.Fatalf("expected script content to be removed, got %q", text)
	}
}
