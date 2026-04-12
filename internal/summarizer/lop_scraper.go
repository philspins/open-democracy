package summarizer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/utils"
)

var lopRequestDelay = 500 * time.Millisecond

// DownloadLoPSummaries fetches LoP summaries for bills lacking them using the
// existing scraper.CrawlLibraryOfParliamentSummary function.
// This runs as a scheduled job before AI summarization.
// If client is nil, utils.NewHTTPClient() is used.
func DownloadLoPSummaries(ctx context.Context, db *sql.DB, client *http.Client) (int, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}

	// Find bills without LoP summaries (treat empty string as missing).
	rows, err := db.QueryContext(ctx, `
		SELECT id, number
		FROM bills
		WHERE summary_lop IS NULL OR summary_lop = ''
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

		parliament, session, ok := utils.ParliamentSessionFromBillID(billID)
		if !ok {
			log.Printf("[lop-scraper] could not parse parliament/session from %q, skipping", billID)
			continue
		}

		log.Printf("[lop-scraper] fetching summary for %q...", billNumber)
		summary := scraper.CrawlLibraryOfParliamentSummary(billNumber, parliament, session, client)

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
		select {
		case <-time.After(lopRequestDelay):
		case <-ctx.Done():
			return downloaded, ctx.Err()
		}
	}

	return downloaded, nil
}
