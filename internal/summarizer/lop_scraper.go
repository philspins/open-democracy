package summarizer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const lopBase = "https://lop.parl.ca/sites/PublicWebsite/default/en_CA/ResearchPublications/LegislativeSummaries"

var (
	lopBaseURL      = lopBase
	lopRequestDelay = 500 * time.Millisecond
)

// FetchLoPSummary fetches a single summary from the Library of Parliament for a bill.
// Returns empty string if not found.
func FetchLoPSummary(ctx context.Context, billNumber string) (string, error) {
	// Format: search page expects bill number like "C-47"
	searchURL := fmt.Sprintf("%s?keyword=%s&parliament=45&sort=DESC", lopBaseURL, billNumber)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Open Democracy/1.0 (open-democracy.ca; contact@open-democracy.ca)")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Look for the search results and extract the first match.
	// The LoP page structure varies, so this is a best-effort extraction.
	var summary string
	doc.Find("article, .legislative-summary, [id*='summary']").Each(func(i int, s *goquery.Selection) {
		if summary == "" {
			text := s.Text()
			if text != "" {
				summary = strings.TrimSpace(text)
			}
		}
	})

	return summary, nil
}

// DownloadLoPSummaries fetches LoP summaries for bills lacking them.
// This runs as a scheduled job before AI summarization.
func DownloadLoPSummaries(ctx context.Context, db *sql.DB) (int, error) {
	// Find bills without LoP summaries.
	rows, err := db.QueryContext(ctx, `
		SELECT id, number
		FROM bills
		WHERE summary_lop IS NULL
		ORDER BY introduced_date DESC
		LIMIT 100
	`)
	if err != nil {
		return 0, fmt.Errorf("query bills: %w", err)
	}

	type billRef struct {
		id     string
		number string
	}
	var bills []billRef
	for rows.Next() {
		var b billRef
		if err := rows.Scan(&b.id, &b.number); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan bills: %w", err)
		}
		bills = append(bills, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	downloaded := 0
	for _, b := range bills {
		billID, billNumber := b.id, b.number

		log.Printf("[lop-scraper] fetching summary for %q...", billNumber)
		summary, err := FetchLoPSummary(ctx, billNumber)
		if err != nil {
			log.Printf("[lop-scraper] fetch error for %q: %v", billNumber, err)
			continue
		}

		if summary == "" {
			// No LoP summary available; will fall back to AI.
			continue
		}

		// Store in database.
		_, dbErr := db.ExecContext(ctx,
			`UPDATE bills SET summary_lop = ? WHERE id = ?`,
			summary, billID)
		if dbErr != nil {
			log.Printf("[lop-scraper] store error for %q: %v", billID, dbErr)
			continue
		}

		downloaded++
		log.Printf("[lop-scraper] ✓ stored LoP summary for %q", billNumber)

		// Rate limit between requests to be polite to LoP servers.
		time.Sleep(lopRequestDelay)
	}

	return downloaded, nil
}
