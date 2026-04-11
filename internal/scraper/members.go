// Members scraper: MP profile pages and the members search list.
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
	MembersListURL     = "https://www.ourcommons.ca/Members/en/search"
	MemberProfileBase  = "https://www.ourcommons.ca/Members/en/%s"
	MemberVotesBase    = "https://www.ourcommons.ca/Members/en/%s?tab=votes"
	OurCommonsBase     = "https://www.ourcommons.ca"
)

// ── types ─────────────────────────────────────────────────────────────────────

// MemberStub is a lightweight record built from the members-list page.
type MemberStub struct {
	ID       string
	Name     string
	Party    string
	Riding   string
	Province string
	Chamber  string
	Active   bool
}

// MemberProfile is a fully enriched MP record scraped from the profile page.
type MemberProfile struct {
	ID          string
	Name        string
	Party       string
	Riding      string
	Province    string
	Role        string
	PhotoURL    string
	Email       string
	Website     string
	Chamber     string
	Active      bool
	LastScraped string
}

// MemberVoteRecord is a vote-history entry from an MP's 'Work' tab.
type MemberVoteRecord struct {
	DivisionID  string
	MemberID    string
	Vote        string
	Description string
	Date        string
}

// ── Members list ──────────────────────────────────────────────────────────────

// CrawlMembersList scrapes the ourcommons.ca member search page for stubs.
func CrawlMembersList(url string, client *http.Client) ([]MemberStub, error) {
	if url == "" {
		url = MembersListURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[members] fetching list: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("members list: %w", err)
	}

	var stubs []MemberStub
	doc.Find(".ce-mip-mp-tile, [class*='mp-tile'], [class*='MemberTile'], article.member").
		Each(func(_ int, card *goquery.Selection) {
			href := ""
			card.Find("a[href*='/Members/en/']").Each(func(_ int, a *goquery.Selection) {
				if href == "" {
					href, _ = a.Attr("href")
				}
			})
			memberID := utils.ExtractMemberID(href)
			if memberID == "" {
				return
			}

			name := strings.TrimSpace(
				card.Find(".ce-mip-mp-name, .member-name, [class*='Name'], h2, h3").First().Text(),
			)
			party := strings.TrimSpace(card.Find(".ce-mip-mp-party, [class*='party'], [class*='Party']").First().Text())
			riding := strings.TrimSpace(card.Find(".ce-mip-mp-constituency, [class*='constituency'], [class*='riding']").First().Text())
			province := strings.TrimSpace(card.Find(".ce-mip-mp-province, [class*='province']").First().Text())

			stubs = append(stubs, MemberStub{
				ID:       memberID,
				Name:     name,
				Party:    party,
				Riding:   riding,
				Province: province,
				Chamber:  "commons",
				Active:   true,
			})
		})

	log.Printf("[members] found %d member stubs", len(stubs))
	return stubs, nil
}

// ── Member profile ────────────────────────────────────────────────────────────

// CrawlMemberProfile scrapes the full profile page for a single MP.
// If profileURL is empty, it is constructed from memberID using the default base URL.
func CrawlMemberProfile(memberID string, profileURL string, client *http.Client) (MemberProfile, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	if profileURL == "" {
		profileURL = fmt.Sprintf(MemberProfileBase, memberID)
	}
	log.Printf("[members] scraping profile: %s", profileURL)

	doc, err := fetchDoc(profileURL, client)
	if err != nil {
		// Return a minimal stub rather than a hard failure so we can continue
		// crawling the rest of the MP list.
		log.Printf("[members] failed to fetch profile %s: %v", memberID, err)
		return MemberProfile{ID: memberID, LastScraped: utils.NowISO()}, nil
	}

	name := strings.TrimSpace(doc.Find("h1.ce-mip-mp-name, h1[class*='Name'], .mp-name, h1").First().Text())
	party := strings.TrimSpace(doc.Find(".ce-mip-mp-party, [class*='party-name']").First().Text())
	riding := strings.TrimSpace(doc.Find(".ce-mip-mp-constituency, [class*='constituency'], [class*='riding']").First().Text())
	province := strings.TrimSpace(doc.Find(".ce-mip-mp-province, [class*='province']").First().Text())
	role := strings.TrimSpace(doc.Find(".ce-mip-mp-role, [class*='role'], .member-role").First().Text())
	if role == "" {
		role = "Member of Parliament"
	}

	// Photo URL
	var photoURL string
	doc.Find(".ce-mip-mp-picture img, .member-photo img, img[alt*='photo']").Each(func(_ int, img *goquery.Selection) {
		if photoURL == "" {
			src, _ := img.Attr("src")
			if strings.HasPrefix(src, "http") {
				photoURL = src
			} else if src != "" {
				photoURL = OurCommonsBase + src
			}
		}
	})

	// Email
	var email string
	doc.Find("a[href^='mailto:']").Each(func(_ int, a *goquery.Selection) {
		if email == "" {
			href, _ := a.Attr("href")
			email = strings.TrimPrefix(href, "mailto:")
		}
	})

	// Website (external link)
	var website string
	doc.Find("a[href^='http'][class*='web'], a[href^='http'][title*='website']").Each(func(_ int, a *goquery.Selection) {
		if website == "" {
			website, _ = a.Attr("href")
		}
	})

	return MemberProfile{
		ID:          memberID,
		Name:        name,
		Party:       party,
		Riding:      riding,
		Province:    province,
		Role:        role,
		PhotoURL:    photoURL,
		Email:       email,
		Website:     website,
		Chamber:     "commons",
		Active:      true,
		LastScraped: utils.NowISO(),
	}, nil
}

// ── Vote history ──────────────────────────────────────────────────────────────

// nonDigit matches any non-digit character.
var nonDigit = regexp.MustCompile(`\D`)

// CrawlMemberVoteHistory scrapes the 'Work → Votes' tab on an MP's profile.
// Returns a slice of vote-history entries.
//
// Note: this page is sometimes JS-rendered. When the table is absent, an
// empty slice is returned with a warning; callers should fall back to
// Playwright if needed.
func CrawlMemberVoteHistory(memberID string, parliament, session int, client *http.Client) ([]MemberVoteRecord, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}

	url := fmt.Sprintf(MemberVotesBase, memberID)
	log.Printf("[members] scraping vote history: %s", url)

	doc, err := fetchDoc(url, client)
	if err != nil {
		return nil, fmt.Errorf("member vote history %s: %w", memberID, err)
	}

	table := doc.Find("table.table, table#vote-history").First()
	if table.Length() == 0 {
		log.Printf("[members] vote history table not found for %s — page may require JS", memberID)
		return nil, nil
	}

	var records []MemberVoteRecord
	table.Find("tbody tr").Each(func(_ int, row *goquery.Selection) {
		cols := row.Find("td")
		if cols.Length() < 3 {
			return
		}

		numText := nonDigit.ReplaceAllString(cols.Eq(0).Text(), "")
		if numText == "" {
			return
		}
		num, _ := strconv.Atoi(numText)
		divisionID := utils.DivisionID(parliament, session, num)

		date := utils.ParseDate(strings.TrimSpace(cols.Eq(1).Text()))
		description := strings.TrimSpace(cols.Eq(2).Text())
		rawVote := ""
		if cols.Length() > 3 {
			rawVote = strings.TrimSpace(cols.Eq(3).Text())
		}

		records = append(records, MemberVoteRecord{
			DivisionID:  divisionID,
			MemberID:    memberID,
			Vote:        NormaliseVote(rawVote),
			Description: description,
			Date:        date,
		})
	})

	log.Printf("[members] member %s: %d historical votes", memberID, len(records))
	return records, nil
}

// NormaliseVote maps raw vote text (EN/FR) to one of the canonical values:
// "Yea" | "Nay" | "Paired" | "Abstain"
func NormaliseVote(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yea", "yes", "pour", "oui":
		return "Yea"
	case "nay", "no", "contre", "non":
		return "Nay"
	case "paired", "apparié":
		return "Paired"
	default:
		return "Abstain"
	}
}
