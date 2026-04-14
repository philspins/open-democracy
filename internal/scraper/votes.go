// Votes scraper: ourcommons.ca votes index, division detail, sitting calendar.
package scraper

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/philspins/open-democracy/internal/utils"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	VotesIndexURL      = "https://www.ourcommons.ca/Members/en/votes"
	SittingCalendarURL = "https://www.ourcommons.ca/en/sitting-calendar"

	// CurrentParliament and CurrentSession: update when a new parliament opens.
	CurrentParliament = 45
	CurrentSession    = 1
)

// ── types ─────────────────────────────────────────────────────────────────────

// DivisionStub holds a row from the votes index page.
type DivisionStub struct {
	ID          string
	Parliament  int
	Session     int
	Number      int
	Date        string
	Description string
	Yeas        int
	Nays        int
	Paired      int
	Result      string
	Chamber     string
	DetailURL   string
	LastScraped string
}

// MemberVote records how a single MP voted in a division.
type MemberVote struct {
	DivisionID string
	MemberID   string
	Vote       string // "Yea" | "Nay" | "Paired" | "Abstain"
}

// ── Votes index ───────────────────────────────────────────────────────────────

// CrawlVotesIndex scrapes the ourcommons.ca recorded-votes index table.
func CrawlVotesIndex(
	url string,
	parliament, session int,
	client *http.Client,
) ([]DivisionStub, error) {
	if url == "" {
		url = VotesIndexURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[votes] fetching index: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("votes index: %w", err)
	}

	table := doc.Find("table.table, table#votes-table, table").First()
	if table.Length() == 0 {
		return nil, fmt.Errorf("votes index: no table found on %s", url)
	}

	nonDigitRe := regexp.MustCompile(`\D`)

	var divs []DivisionStub
	table.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
		cols := row.Find("td")
		// Actual ourcommons.ca column order (6 columns):
		// 0: vote number  1: bill type (optional)  2: description
		// 3: "Yeas / Nays / Paired"  4: result (with icon)  5: date
		if cols.Length() < 5 {
			return
		}

		numText := strings.TrimSpace(nonDigitRe.ReplaceAllString(cols.Eq(0).Text(), ""))
		if numText == "" {
			return
		}
		num, _ := strconv.Atoi(numText)

		description := strings.TrimSpace(cols.Eq(2).Text())

		// Col 3 contains "Yeas / Nays / Paired" — split on "/"
		yeas, nays, paired := 0, 0, 0
		voteParts := strings.Split(cols.Eq(3).Text(), "/")
		if len(voteParts) >= 1 {
			yeas, _ = strconv.Atoi(strings.TrimSpace(nonDigitRe.ReplaceAllString(voteParts[0], "")))
		}
		if len(voteParts) >= 2 {
			nays, _ = strconv.Atoi(strings.TrimSpace(nonDigitRe.ReplaceAllString(voteParts[1], "")))
		}
		if len(voteParts) >= 3 {
			paired, _ = strconv.Atoi(strings.TrimSpace(nonDigitRe.ReplaceAllString(voteParts[2], "")))
		}

		result := strings.TrimSpace(cols.Eq(4).Text())

		// Col 5: date formatted as "Wednesday, March 25, 2026"
		date := ""
		if cols.Length() > 5 {
			date = utils.FindDateInText(strings.TrimSpace(cols.Eq(5).Text()))
		}

		// Detail link
		var detailURL string
		row.Find("a[href*='votes']").Each(func(_ int, a *goquery.Selection) {
			if detailURL == "" {
				if href, ok := a.Attr("href"); ok {
					if strings.HasPrefix(href, "http") {
						detailURL = href
					} else {
						detailURL = "https://www.ourcommons.ca" + href
					}
				}
			}
		})

		divs = append(divs, DivisionStub{
			ID:          utils.DivisionID(parliament, session, num),
			Parliament:  parliament,
			Session:     session,
			Number:      num,
			Date:        date,
			Description: description,
			Yeas:        yeas,
			Nays:        nays,
			Paired:      paired,
			Result:      result,
			Chamber:     "commons",
			DetailURL:   detailURL,
			LastScraped: utils.NowISO(),
		})
	})

	log.Printf("[votes] found %d divisions", len(divs))
	return divs, nil
}

// ── Division detail ───────────────────────────────────────────────────────────

// voteSelectors maps canonical vote types to the CSS selectors used on the
// ourcommons.ca division detail page.
var voteSelectors = map[string][]string{
	"Yea": {
		".vote-yea .member-name a",
		"[class*='Yea'] .member-name a",
		"section.agreed-to li a",
		"ul.yea li a",
	},
	"Nay": {
		".vote-nay .member-name a",
		"[class*='Nay'] .member-name a",
		"section.negatived li a",
		"ul.nay li a",
	},
	"Paired": {
		".vote-paired .member-name a",
		"[class*='Paired'] .member-name a",
		"ul.paired li a",
	},
}

// CrawlDivisionDetail scrapes how each MP voted on a single division.
func CrawlDivisionDetail(divisionID, url string, client *http.Client) ([]MemberVote, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[votes] scraping division detail: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("division detail %q: %w", url, err)
	}

	var votes []MemberVote
	for voteType, selectors := range voteSelectors {
		for _, sel := range selectors {
			members := doc.Find(sel)
			if members.Length() == 0 {
				continue
			}
			members.Each(func(_ int, a *goquery.Selection) {
				href, _ := a.Attr("href")
				memberID := utils.ExtractMemberID(href)
				if memberID != "" {
					votes = append(votes, MemberVote{
						DivisionID: divisionID,
						MemberID:   memberID,
						Vote:       voteType,
					})
				}
			})
			break // found a working selector; skip the rest
		}
	}

	log.Printf("[votes] division %s: %d member votes", divisionID, len(votes))
	return votes, nil
}

// ── Sitting calendar ──────────────────────────────────────────────────────────

// CrawlSittingCalendar scrapes the ourcommons.ca sitting calendar.
// Returns a sorted, deduplicated list of ISO-8601 sitting dates.
func CrawlSittingCalendar(url string, client *http.Client) ([]string, error) {
	if url == "" {
		url = SittingCalendarURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[votes] fetching sitting calendar: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("sitting calendar: %w", err)
	}

	seen := make(map[string]bool)
	doc.Find("[data-date], td.sitting, td[class*='sitting'], [class*='sitting-day']").Each(
		func(_ int, s *goquery.Selection) {
			raw, _ := s.Attr("data-date")
			if raw == "" {
				raw, _ = s.Attr("datetime")
			}
			if raw == "" {
				raw = strings.TrimSpace(s.Text())
			}
			if d := utils.ParseDate(raw); d != "" {
				seen[d] = true
			}
		})

	dates := make([]string, 0, len(seen))
	for d := range seen {
		dates = append(dates, d)
	}

	// Sort (dates are ISO-8601 so lexicographic = chronological)
	for i := 1; i < len(dates); i++ {
		for j := i; j > 0 && dates[j] < dates[j-1]; j-- {
			dates[j], dates[j-1] = dates[j-1], dates[j]
		}
	}

	log.Printf("[votes] found %d sitting dates", len(dates))
	return dates, nil
}

// ParliamentIsSitting returns true if today (or the provided date) falls in
// the list of known sitting dates.
func ParliamentIsSitting(sittingDates []string, today string) bool {
	if today == "" {
		today = utils.TodayISO()
	}
	for _, d := range sittingDates {
		if d == today {
			return true
		}
	}
	return false
}
