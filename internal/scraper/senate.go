// Senate scraper: sencanada.ca votes index and division detail.
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
	SenateVotesURL  = "https://sencanada.ca/en/in-the-chamber/votes"
	SenateSiteBase  = "https://sencanada.ca"
)

// ── Senate votes index ────────────────────────────────────────────────────────

// CrawlSenateVotesIndex scrapes the sencanada.ca votes index.
// Returns division stubs with chamber="senate".
func CrawlSenateVotesIndex(
	url string,
	parliament, session int,
	client *http.Client,
) ([]DivisionStub, error) {
	if url == "" {
		url = SenateVotesURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[senate] fetching votes index: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("senate votes index: %w", err)
	}

	table := doc.Find("table").First()
	if table.Length() == 0 {
		return nil, fmt.Errorf("senate votes index: no table found on %s", url)
	}

	nonDigitRe := regexp.MustCompile(`\D`)

	var divs []DivisionStub
	table.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
		cols := row.Find("td")
		if cols.Length() < 3 {
			return
		}

		numText := strings.TrimSpace(nonDigitRe.ReplaceAllString(cols.Eq(0).Text(), ""))
		if numText == "" {
			return
		}
		num, _ := strconv.Atoi(numText)

		date := utils.ParseDate(strings.TrimSpace(cols.Eq(1).Text()))
		description := strings.TrimSpace(cols.Eq(2).Text())

		yeas := 0
		if cols.Length() > 3 {
			yeas, _ = strconv.Atoi(strings.TrimSpace(nonDigitRe.ReplaceAllString(cols.Eq(3).Text(), "")))
		}
		nays := 0
		if cols.Length() > 4 {
			nays, _ = strconv.Atoi(strings.TrimSpace(nonDigitRe.ReplaceAllString(cols.Eq(4).Text(), "")))
		}
		result := ""
		if cols.Length() > 5 {
			result = strings.TrimSpace(cols.Eq(5).Text())
		}

		var detailURL string
		row.Find("a").Each(func(_ int, a *goquery.Selection) {
			if detailURL == "" {
				href, _ := a.Attr("href")
				if strings.HasPrefix(href, "http") {
					detailURL = href
				} else if href != "" {
					detailURL = SenateSiteBase + href
				}
			}
		})

		divID := fmt.Sprintf("senate-%s", utils.DivisionID(parliament, session, num))

		divs = append(divs, DivisionStub{
			ID:          divID,
			Parliament:  parliament,
			Session:     session,
			Number:      num,
			Date:        date,
			Description: description,
			Yeas:        yeas,
			Nays:        nays,
			Paired:      0,
			Result:      result,
			Chamber:     "senate",
			DetailURL:   detailURL,
			LastScraped: utils.NowISO(),
		})
	})

	log.Printf("[senate] found %d divisions", len(divs))
	return divs, nil
}

// ── Senate division detail ────────────────────────────────────────────────────

// senateVoteSelectors maps vote types to CSS selectors for the sencanada.ca
// division detail page (structure mirrors ourcommons.ca).
var senateVoteSelectors = map[string][]string{
	"Yea": {
		".vote-yea li a",
		"ul.yea li a",
		"[class*='Yea'] li a",
	},
	"Nay": {
		".vote-nay li a",
		"ul.nay li a",
		"[class*='Nay'] li a",
	},
	"Abstain": {
		".vote-abstain li a",
		"ul.abstain li a",
		"[class*='Abstain'] li a",
	},
}

// CrawlSenateDivisionDetail scrapes how each senator voted on a single division.
func CrawlSenateDivisionDetail(divisionID, url string, client *http.Client) ([]MemberVote, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[senate] scraping division detail: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("senate division detail %q: %w", url, err)
	}

	var votes []MemberVote
	for voteType, selectors := range senateVoteSelectors {
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
			break
		}
	}

	log.Printf("[senate] division %s: %d votes", divisionID, len(votes))
	return votes, nil
}
