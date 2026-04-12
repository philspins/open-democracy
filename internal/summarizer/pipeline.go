// Package summarizer handles AI-powered bill summarization using Claude API.
package summarizer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

const (
	claudeModel = "claude-3-5-sonnet-20241022"
	claudeURL   = "https://api.anthropic.com/v1/messages"
)

var systemPrompt = `You are a non-partisan Canadian civic education assistant.
Your job is to summarize bills from the Parliament of Canada in plain English.
You must be accurate, neutral, and clear. Never editorialize or express opinions.
Always write for a Canadian high school student — no legal jargon.

Provide your response as valid JSON only (no markdown or extra text):
{
  "one_sentence": "One sentence (max 25 words) describing what this bill does.",
  "plain_summary": "2–3 paragraph plain-English explanation. What does it do? Who does it affect? Why was it introduced?",
  "key_changes": ["List of 3–6 specific things this bill would change or create"],
  "who_is_affected": ["List of groups, industries, or people most affected"],
  "estimated_cost": "Fiscal impact if mentioned in the bill, or 'Not specified'",
  "category": "One of: Housing, Health, Environment, Defence, Indigenous, Finance, Justice, Agriculture, Transport, Labour, Education, Foreign Affairs, Digital/Tech, Other"
}`

// SummaryResult holds the structured fields returned by Claude.
type SummaryResult struct {
	OneSentence   string   `json:"one_sentence"`
	PlainSummary  string   `json:"plain_summary"`
	KeyChanges    []string `json:"key_changes"`
	WhoIsAffected []string `json:"who_is_affected"`
	EstimatedCost string   `json:"estimated_cost"`
	Category      string   `json:"category"`
	BillID        string   `json:"bill_id"`
	GeneratedAt   string   `json:"generated_at"`
	Model         string   `json:"model"`
}

// claudeRequest is the request body structure for Claude API.
type claudeRequest struct {
	Model      string        `json:"model"`
	MaxTokens  int           `json:"max_tokens"`
	System     string        `json:"system"`
	Messages   []claudeMsg   `json:"messages"`
	Temperature float64      `json:"temperature,omitempty"`
}

type claudeMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the response from Claude API.
type claudeResponse struct {
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// SummarizeBill calls Claude API and returns a structured summary.
// It truncates very long bills (keeping first ~120k chars + last 30k chars).
func SummarizeBill(ctx context.Context, billID, billTitle, billText string) (*SummaryResult, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	// Truncate very long bills — keep first ~120k + last 30k characters (rune-safe).
	const maxRunes = 150_000
	if utf8.RuneCountInString(billText) > maxRunes {
		runes := []rune(billText)
		billText = string(runes[:120_000]) + "\n\n[...truncated...]\n\n" + string(runes[len(runes)-30_000:])
	}

	prompt := fmt.Sprintf(`Please summarize the following Canadian bill:

Bill ID: %s
Title: %s

Full text:
%s

Respond with only valid JSON, no markdown or extra text.`, billID, billTitle, billText)

	req := claudeRequest{
		Model:      claudeModel,
		MaxTokens:  2048,
		System:     systemPrompt,
		Temperature: 0.3,
		Messages: []claudeMsg{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp claudeResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("api error: %s", apiResp.Error.Message)
	}

	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty response from api")
	}

	// Parse the JSON response from Claude.
	var result SummaryResult
	if err := json.Unmarshal([]byte(apiResp.Content[0].Text), &result); err != nil {
		return nil, fmt.Errorf("parse summary JSON: %w", err)
	}

	result.BillID = billID
	result.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	result.Model = claudeModel

	return &result, nil
}

// SummarizeNewBills processes all bills that still lack summaries.
// Priority: LoP > AI fallback.
// This is meant to be called by a robfig/cron scheduler job.
func SummarizeNewBills(ctx context.Context, db *sql.DB, onlyMissing bool) (int, error) {
	// Find bills without summaries (neither LoP nor AI).
	query := `
		SELECT id, number, title, full_text_url
		FROM bills
		WHERE summary_lop IS NULL AND summary_ai IS NULL
		ORDER BY introduced_date DESC
		LIMIT 50  -- Batch size to avoid API overload
	`
	if !onlyMissing {
		query = `
			SELECT id, number, title, full_text_url
			FROM bills
			ORDER BY introduced_date DESC
			LIMIT 50
		`
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("query bills: %w", err)
	}
	defer rows.Close()

	processed := 0
	for rows.Next() {
		var billID, number, title, fullTextURL string
		if err := rows.Scan(&billID, &number, &title, &fullTextURL); err != nil {
			log.Printf("[summarizer] scan error: %v", err)
			continue
		}

		// Skip if no full text URL.
		if fullTextURL == "" {
			continue
		}

		// Fetch bill text.
		billText, err := fetchBillText(ctx, fullTextURL)
		if err != nil {
			log.Printf("[summarizer] fetch bill text %q: %v", billID, err)
			continue
		}

		// Call Claude API.
		log.Printf("[summarizer] summarizing bill %q (%s)...", number, title)
		summary, err := SummarizeBill(ctx, billID, title, billText)
		if err != nil {
			log.Printf("[summarizer] summarize error %q: %v", billID, err)
			// Don't bail — retry later.
			continue
		}

		// Store in database.
		summaryJSON, _ := json.Marshal(summary)
		_, err = db.ExecContext(ctx,
			`UPDATE bills SET summary_ai = ?, category = ? WHERE id = ?`,
			string(summaryJSON), summary.Category, billID)
		if err != nil {
			log.Printf("[summarizer] store summary %q: %v", billID, err)
			continue
		}

		processed++
		log.Printf("[summarizer] ✓ stored summary for %q", number)

		// Rate limit: 1 second between API calls to be polite.
		time.Sleep(1 * time.Second)
	}

	return processed, rows.Err()
}

// fetchBillText fetches and extracts plain text from a bill's HTML document using goquery.
func fetchBillText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	// Standard user agent for politeness.
	req.Header.Set("User-Agent", "Open Democracy/1.0 (open-democracy.ca; contact@open-democracy.ca)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}

	// Use goquery to parse HTML and extract text.
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Remove script and style tags to clean text.
	doc.Find("script, style").Remove()

	// Extract all text content.
	text := doc.Text()

	// Collapse whitespace.
	text = collapseWhitespace(text)

	return strings.TrimSpace(text), nil
}

func removeHTMLTags(s string, tags ...string) string {
	for _, tag := range tags {
		pattern := fmt.Sprintf(`(?i)<%s[^>]*>.*?</%s>`, tag, tag)
		re := regexp.MustCompile(pattern)
		s = re.ReplaceAllString(s, "")
	}
	return s
}

func removeAllHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return re.ReplaceAllString(s, "")
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
