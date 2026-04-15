// Members scraper: MP profile pages and the members search list.
package scraper

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/philspins/open-democracy/internal/utils"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	MembersListURL    = "https://www.ourcommons.ca/Members/en/search"
	MemberProfileBase = "https://www.ourcommons.ca/Members/en/%s"
	MemberVotesBase   = "https://www.ourcommons.ca/Members/en/%s?tab=votes"
	OurCommonsBase    = "https://www.ourcommons.ca"

	// RepresentAPIURL is the Represent OpenNorth API endpoint for current House of
	// Commons MPs. A single request with limit=1000 returns all 343 members.
	RepresentAPIURL = "https://represent.opennorth.ca/representatives/house-of-commons/?format=json&limit=1000"

	// RepresentBaseURL is the root URL of the Represent OpenNorth API.
	RepresentBaseURL = "https://represent.opennorth.ca"
)

// ProvincialLegislatureAPIs lists the Represent OpenNorth API endpoints for
// each provincial and territorial legislature, keyed by the representative-set
// slug. The slug is used to generate deterministic member IDs.
var ProvincialLegislatureAPIs = map[string]string{
	"alberta-legislature":                "https://represent.opennorth.ca/representatives/alberta-legislature/?format=json&limit=1000",
	"bc-legislature":                     "https://represent.opennorth.ca/representatives/bc-legislature/?format=json&limit=1000",
	"manitoba-legislature":               "https://represent.opennorth.ca/representatives/manitoba-legislature/?format=json&limit=1000",
	"newfoundland-labrador-legislature":  "https://represent.opennorth.ca/representatives/newfoundland-labrador-legislature/?format=json&limit=1000",
	"nova-scotia-legislature":            "https://represent.opennorth.ca/representatives/nova-scotia-legislature/?format=json&limit=1000",
	"northwest-territories-legislature":  "https://represent.opennorth.ca/representatives/northwest-territories-legislature/?format=json&limit=1000",
	"ontario-legislature":                "https://represent.opennorth.ca/representatives/ontario-legislature/?format=json&limit=1000",
	"pei-legislature":                    "https://represent.opennorth.ca/representatives/pei-legislature/?format=json&limit=1000",
	"quebec-assemblee-nationale":         "https://represent.opennorth.ca/representatives/quebec-assemblee-nationale/?format=json&limit=1000",
	"saskatchewan-legislature":           "https://represent.opennorth.ca/representatives/saskatchewan-legislature/?format=json&limit=1000",
	"yukon-legislature":                  "https://represent.opennorth.ca/representatives/yukon-legislature/?format=json&limit=1000",
}

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
	ID              string
	Name            string
	Party           string
	Riding          string
	Province        string
	Role            string
	PhotoURL        string
	Email           string
	Website         string
	Chamber         string
	Active          bool
	LastScraped     string
	GovernmentLevel string // "federal" | "provincial"
}

// MemberVoteRecord is a vote-history entry from an MP's 'Work' tab.
type MemberVoteRecord struct {
	DivisionID  string
	MemberID    string
	Vote        string
	Description string
	Date        string
}

// ── Represent API types ───────────────────────────────────────────────────────

// representAPIResponse is the top-level JSON response from the Represent API.
type representAPIResponse struct {
	Objects []representAPIItem `json:"objects"`
	Meta    struct {
		Next string `json:"next"`
	} `json:"meta"`
}

// representAPIItem is one representative record from the Represent API.
type representAPIItem struct {
	Name          string               `json:"name"`
	PartyName     string               `json:"party_name"`
	DistrictName  string               `json:"district_name"`
	Email         string               `json:"email"`
	URL           string               `json:"url"`
	PersonalURL   string               `json:"personal_url"`
	PhotoURL      string               `json:"photo_url"`
	Offices       []representAPIOffice `json:"offices"`
	Extra         representAPIExtra    `json:"extra"`
	ElectedOffice string               `json:"elected_office"`
}

// representAPIOffice is a single office record inside a representative item.
type representAPIOffice struct {
	Postal string `json:"postal"`
	Type   string `json:"type"`
}

// representAPIExtra holds optional extra fields returned by the API.
type representAPIExtra struct {
	Roles []string `json:"roles"`
}

// provinceAbbrevRe matches a two-letter Canadian province/territory code that
// appears on a word boundary, e.g. "Ottawa ON  K1A 0A6".
var provinceAbbrevRe = regexp.MustCompile(`\b(AB|BC|MB|NB|NL|NS|NT|NU|ON|PE|QC|SK|YT)\b`)

// provinceNames maps two-letter codes to full province/territory names.
var provinceNames = map[string]string{
	"AB": "Alberta",
	"BC": "British Columbia",
	"MB": "Manitoba",
	"NB": "New Brunswick",
	"NL": "Newfoundland and Labrador",
	"NS": "Nova Scotia",
	"NT": "Northwest Territories",
	"NU": "Nunavut",
	"ON": "Ontario",
	"PE": "Prince Edward Island",
	"QC": "Quebec",
	"SK": "Saskatchewan",
	"YT": "Yukon",
}

// extractProvinceFromOffices infers the MP's home province from the postal
// address of their constituency office.
func extractProvinceFromOffices(offices []representAPIOffice) string {
	// Prefer constituency office; fall back to any office.
	for _, pass := range []string{"constituency", ""} {
		for _, o := range offices {
			if pass != "" && o.Type != pass {
				continue
			}
			if m := provinceAbbrevRe.FindString(o.Postal); m != "" {
				if full, ok := provinceNames[m]; ok {
					return full
				}
			}
		}
	}
	return ""
}

// ── Represent API ─────────────────────────────────────────────────────────────

// CrawlMembersFromAPI fetches all current House of Commons members from the
// Represent OpenNorth API and returns them as MemberProfile records. All
// profile fields (name, party, riding, province, email, photo URL, etc.) are
// populated directly from the API — no per-MP HTML requests are needed.
//
// If apiURL is empty, RepresentAPIURL is used. The function follows the API's
// pagination links so it works correctly even when limit < total_count.
func CrawlMembersFromAPI(apiURL string, client *http.Client) ([]MemberProfile, error) {
	if apiURL == "" {
		apiURL = RepresentAPIURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	return fetchRepresentPages(apiURL, "federal", "", client)
}

// CrawlProvincialMembersFromAPI fetches all members for one provincial or
// territorial legislature from the Represent OpenNorth API.
//
// setSlug is the representative-set slug (e.g. "ontario-legislature") and is
// used to form deterministic member IDs of the form "{setSlug}-{name-slug}".
// If apiURL is empty the URL is derived from ProvincialLegislatureAPIs.
func CrawlProvincialMembersFromAPI(setSlug, apiURL string, client *http.Client) ([]MemberProfile, error) {
	if apiURL == "" {
		var ok bool
		apiURL, ok = ProvincialLegislatureAPIs[setSlug]
		if !ok {
			return nil, fmt.Errorf("no known API URL for provincial set %q", setSlug)
		}
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	return fetchRepresentPages(apiURL, "provincial", setSlug, client)
}

// fetchRepresentPages is the shared pagination engine used by both federal and
// provincial crawl functions.
//
//   - governmentLevel is stored verbatim on each MemberProfile.
//   - setSlug is used to build provincial member IDs; pass "" for federal (IDs
//     are extracted from the ourcommons.ca URL instead).
func fetchRepresentPages(apiURL, governmentLevel, setSlug string, client *http.Client) ([]MemberProfile, error) {
	base, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("members API: bad URL %q: %w", apiURL, err)
	}

	var profiles []MemberProfile
	pageURL := apiURL
	for pageURL != "" {
		log.Printf("[members] fetching API page: %s", pageURL)

		resp, err := client.Get(pageURL)
		if err != nil {
			return nil, fmt.Errorf("members API GET %s: %w", pageURL, err)
		}

		var page representAPIResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("members API decode: %w", decodeErr)
		}

		for _, item := range page.Objects {
			memberID := ""
			if governmentLevel == "federal" {
				memberID = utils.ExtractMemberID(item.URL)
			} else {
				memberID = extractProvincialMemberID(setSlug, item)
			}
			if memberID == "" {
				log.Printf("[members] skipping item with no extractable ID: url=%q", item.URL)
				continue
			}

			role := item.ElectedOffice
			if role == "" {
				for _, r := range item.Extra.Roles {
					if r != "" {
						role = r
						break
					}
				}
			}
			if role == "" {
				if governmentLevel == "federal" {
					role = "Member of Parliament"
				} else {
					role = "Member of Provincial Parliament"
				}
			}

			chamber := "legislature"
			if governmentLevel == "federal" {
				chamber = "commons"
			}

			profiles = append(profiles, MemberProfile{
				ID:              memberID,
				Name:            item.Name,
				Party:           item.PartyName,
				Riding:          item.DistrictName,
				Province:        extractProvinceFromOffices(item.Offices),
				Role:            role,
				PhotoURL:        item.PhotoURL,
				Email:           item.Email,
				Website:         item.PersonalURL,
				Chamber:         chamber,
				Active:          true,
				LastScraped:     utils.NowISO(),
				GovernmentLevel: governmentLevel,
			})
		}

		// Follow pagination — meta.next is a relative path on the same host.
		pageURL = ""
		if page.Meta.Next != "" {
			nextRef, err := url.Parse(page.Meta.Next)
			if err == nil {
				pageURL = base.ResolveReference(nextRef).String()
			}
		}
	}

	log.Printf("[members] fetched %d %s members from Represent API", len(profiles), governmentLevel)
	return profiles, nil
}

// extractProvincialMemberID builds a deterministic ID for a provincial member
// as "{setSlug}-{name-slug}" where the name slug is derived from the last
// path segment of item.URL (a slugified name on most provincial sites), or
// falls back to a slugified version of item.Name when the URL is empty.
func extractProvincialMemberID(setSlug string, item representAPIItem) string {
	nameSlug := urlLastSegment(item.URL)
	if nameSlug == "" {
		nameSlug = nameToSlug(item.Name)
	}
	if nameSlug == "" {
		return ""
	}
	return setSlug + "-" + nameSlug
}

// urlLastSegment returns the last non-empty path segment of rawURL.
func urlLastSegment(rawURL string) string {
	rawURL = strings.TrimSuffix(rawURL, "/")
	if i := strings.LastIndex(rawURL, "/"); i >= 0 {
		return rawURL[i+1:]
	}
	return rawURL
}

// nameToSlug converts a display name to a URL-safe slug, e.g.
// "Laura Smith" → "laura-smith".
func nameToSlug(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Replace spaces with hyphens; strip characters that aren't alphanumeric or hyphens.
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
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
		ID:              memberID,
		Name:            name,
		Party:           party,
		Riding:          riding,
		Province:        province,
		Role:            role,
		PhotoURL:        photoURL,
		Email:           email,
		Website:         website,
		Chamber:         "commons",
		Active:          true,
		LastScraped:     utils.NowISO(),
		GovernmentLevel: "federal",
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
