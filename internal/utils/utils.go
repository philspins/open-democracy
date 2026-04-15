// Package utils provides shared HTTP, URL/ID extraction and date helpers.
package utils

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// AppUserAgent is the User-Agent header sent with every request.
// Identifies the project and provides a contact email — good scraping etiquette.
const AppUserAgent = "Open Democracy/1.0 (open-democracy.ca; contact@open-democracy.ca)"

// CacheTTL is the maximum age of cached HTTP responses.
const CacheTTL = 6 * time.Hour

// DefaultRequestDelay is the pause inserted between outbound requests.
const DefaultRequestDelay = 500 * time.Millisecond

// NewHTTPClient returns an *http.Client with a 15-second timeout and our User-Agent
// injected via a transport wrapper.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &uaTransport{base: http.DefaultTransport},
	}
}

// uaTransport injects the User-Agent header on every request.
type uaTransport struct {
	base http.RoundTripper
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", AppUserAgent)
	return t.base.RoundTrip(clone)
}

// ── URL / ID extraction helpers ───────────────────────────────────────────────

var (
	legisinfoRe  = regexp.MustCompile(`(?i)/legisinfo/en/bill/(\d+)-(\d+)/([A-Za-z]+-?\d+)`)
	memberRe     = regexp.MustCompile(`(?i)/Members/en/(\d+)`)
	billNumberRe = regexp.MustCompile(`(?i)\b([CS]-\d+)\b`)
)

// ExtractBillID parses a canonical bill ID from a LEGISinfo URL.
//
//	https://www.parl.ca/legisinfo/en/bill/45-1/c-47  →  "45-1-c-47"
func ExtractBillID(rawURL string) string {
	m := legisinfoRe.FindStringSubmatch(rawURL)
	if len(m) == 4 {
		return fmt.Sprintf("%s-%s-%s", m[1], m[2], strings.ToLower(m[3]))
	}
	return ""
}

// ExtractMemberID parses a member ID from an ourcommons.ca URL.
//
//	https://www.ourcommons.ca/Members/en/123006  →  "123006"
func ExtractMemberID(rawURL string) string {
	m := memberRe.FindStringSubmatch(rawURL)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// DivisionID builds the canonical division ID from its components.
func DivisionID(parliament, session, number int) string {
	return fmt.Sprintf("%d-%d-%d", parliament, session, number)
}

// BillIDFromParts constructs a canonical bill ID from its components.
// billNumber should be like "C-47" or "S-209"; returns empty string if blank.
func BillIDFromParts(parliament, session int, billNumber string) string {
	billNumber = strings.TrimSpace(billNumber)
	if billNumber == "" {
		return ""
	}
	return fmt.Sprintf("%d-%d-%s", parliament, session, strings.ToLower(billNumber))
}

// ExtractBillNumber extracts a bill number like "C-47" or "S-209" from text.
// Returns empty string if none found.
func ExtractBillNumber(text string) string {
	m := billNumberRe.FindStringSubmatch(text)
	if len(m) == 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// BillNumberFromID extracts the bill-number portion of a bill ID.
//
//	"45-1-c-47"  →  "C-47"
func BillNumberFromID(billID string) string {
	parts := strings.SplitN(billID, "-", 3)
	if len(parts) == 3 {
		return strings.ToUpper(parts[2])
	}
	return ""
}

// ParliamentSessionFromBillID returns (parliament, session) from a bill ID like "45-1-c-47".
func ParliamentSessionFromBillID(billID string) (parliament int, session int, ok bool) {
	parts := strings.SplitN(billID, "-", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	var p, s int
	if _, err := fmt.Sscan(parts[0], &p); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscan(parts[1], &s); err != nil {
		return 0, 0, false
	}
	return p, s, true
}

// BillChamber returns "senate" for S-nnn bills, "commons" otherwise.
func BillChamber(billNumber string) string {
	if strings.HasPrefix(strings.ToUpper(billNumber), "S-") {
		return "senate"
	}
	return "commons"
}

// ── Date helpers ──────────────────────────────────────────────────────────────

var dateFormats = []string{
	"2006-01-02",      // ISO
	"January 2, 2006", // e.g. "April 3, 2024"
	"2 January 2006",  // e.g. "3 April 2024"
	"Jan 2, 2006",     // e.g. "Apr 3, 2024"
	"2006/01/02",
}

// ParseDate attempts to parse a date string and return it in ISO-8601 format (YYYY-MM-DD).
// Returns an empty string if no format matches.
func ParseDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var dateInTextRe = regexp.MustCompile(
	`(\d{4}-\d{2}-\d{2})` +
		`|([A-Z][a-z]+ \d{1,2},? \d{4})` +
		`|(\d{1,2} [A-Z][a-z]+ \d{4})`,
)

// FindDateInText searches text for a recognisable date and returns it in
// ISO-8601 format, or an empty string if none is found.
func FindDateInText(text string) string {
	m := dateInTextRe.FindString(text)
	if m == "" {
		return ""
	}
	return ParseDate(m)
}

// TodayISO returns today's date as an ISO-8601 string.
func TodayISO() string {
	return time.Now().UTC().Format("2006-01-02")
}

// NowISO returns the current UTC time as an ISO-8601 string (without timezone suffix).
func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}
