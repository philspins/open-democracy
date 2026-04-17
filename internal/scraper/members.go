// Members scraper: MP profile pages and the members search list.
package scraper

import (
	"encoding/json"
	"fmt"
	"io"
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

	// NLMembersJSURL is the URL for the Newfoundland and Labrador House of
	// Assembly member-index JavaScript data file.  The page at
	// https://www.assembly.nl.ca/Members/members.aspx loads its member table
	// from this file via JavaScript, so we read it directly to obtain the
	// member profile URLs (and from those, the BioPhoto URLs).
	NLMembersJSURL = "https://www.assembly.nl.ca/js/members-index.js"

	// NLMembersBase is the base URL used to resolve relative photo and profile
	// paths found in the NL member JS file.
	NLMembersBase = "https://www.assembly.nl.ca"
)

// ProvincialLegislatureAPIs lists the Represent OpenNorth API endpoints for
// each provincial and territorial legislature, keyed by the representative-set
// slug. The slug is used to generate deterministic member IDs.
var ProvincialLegislatureAPIs = map[string]string{
	"alberta-legislature":               "https://represent.opennorth.ca/representatives/alberta-legislature/?format=json&limit=1000",
	"bc-legislature":                    "https://represent.opennorth.ca/representatives/bc-legislature/?format=json&limit=1000",
	"manitoba-legislature":              "https://represent.opennorth.ca/representatives/manitoba-legislature/?format=json&limit=1000",
	"nb-legislature":                    "https://represent.opennorth.ca/representatives/nb-legislature/?format=json&limit=1000",
	"newfoundland-labrador-legislature": "https://represent.opennorth.ca/representatives/newfoundland-labrador-legislature/?format=json&limit=1000",
	"nova-scotia-legislature":           "https://represent.opennorth.ca/representatives/nova-scotia-legislature/?format=json&limit=1000",
	"northwest-territories-legislature": "https://represent.opennorth.ca/representatives/northwest-territories-legislature/?format=json&limit=1000",
	"ontario-legislature":               "https://represent.opennorth.ca/representatives/ontario-legislature/?format=json&limit=1000",
	"pei-legislature":                   "https://represent.opennorth.ca/representatives/pei-legislature/?format=json&limit=1000",
	"quebec-assemblee-nationale":        "https://represent.opennorth.ca/representatives/quebec-assemblee-nationale/?format=json&limit=1000",
	"saskatchewan-legislature":          "https://represent.opennorth.ca/representatives/saskatchewan-legislature/?format=json&limit=1000",
	"yukon-legislature":                 "https://represent.opennorth.ca/representatives/yukon-legislature/?format=json&limit=1000",
}

// setSlugToProvince maps each representative-set slug to its full province or
// territory name. This is used as a fallback when a member's office records do
// not contain a postal address with a recognisable province abbreviation.
var setSlugToProvince = map[string]string{
	"alberta-legislature":               "Alberta",
	"bc-legislature":                    "British Columbia",
	"manitoba-legislature":              "Manitoba",
	"nb-legislature":                    "New Brunswick",
	"newfoundland-labrador-legislature": "Newfoundland and Labrador",
	"nova-scotia-legislature":           "Nova Scotia",
	"northwest-territories-legislature": "Northwest Territories",
	"ontario-legislature":               "Ontario",
	"pei-legislature":                   "Prince Edward Island",
	"quebec-assemblee-nationale":        "Quebec",
	"saskatchewan-legislature":          "Saskatchewan",
	"yukon-legislature":                 "Yukon",
}

// NLMembersJSURLOverride may be set in tests to redirect enrichNLMemberPhotos
// to a local test server instead of the live NL assembly website.
var NLMembersJSURLOverride string

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
	profiles, err := fetchRepresentPages(apiURL, "provincial", setSlug, client)
	if err != nil {
		return nil, err
	}
	// The Newfoundland and Labrador Represent API does not include photo URLs.
	// Enrich member photos from the legislature's own JavaScript member-index.
	if setSlug == "newfoundland-labrador-legislature" {
		profiles = enrichNLMemberPhotos(profiles, "", client)
	}
	return profiles, nil
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
					role = "Member of the Legislature"
				}
			}

			chamber := "legislature"
			if governmentLevel == "federal" {
				chamber = "commons"
			}

			province := extractProvinceFromOffices(item.Offices)
			if province == "" && setSlug != "" {
				if prov, ok := setSlugToProvince[setSlug]; ok {
					province = prov
				}
			}

			profiles = append(profiles, MemberProfile{
				ID:              memberID,
				Name:            item.Name,
				Party:           item.PartyName,
				Riding:          item.DistrictName,
				Province:        province,
				Role:            role,
				PhotoURL:        normalizeRepresentPhotoURL(item.PhotoURL, item.URL),
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

// normalizeRepresentPhotoURL converts Represent API photo_url values to an
// absolute URL. Relative photo paths are resolved against the member profile
// URL when available, and otherwise against the Represent API base URL.
func normalizeRepresentPhotoURL(photoURL, memberURL string) string {
	photoURL = strings.TrimSpace(photoURL)
	if photoURL == "" {
		return ""
	}
	photoRef, err := url.Parse(photoURL)
	if err != nil {
		return photoURL
	}
	if photoRef.IsAbs() {
		return photoURL
	}
	if memberURL != "" {
		base, err := url.Parse(memberURL)
		if err == nil {
			return base.ResolveReference(photoRef).String()
		}
	}
	return ""
}

// genericPageSegments is the set of path-segment values that are shared across
// all members of a legislature (i.e. all members have the same path, differing
// only by query parameters).  When urlLastSegment returns one of these values
// the segment cannot be used as a unique member identifier, so
// extractProvincialMemberID falls back to deriving the slug from item.Name.
//
// Known cases:
//   - Alberta: .../member-information?mid=0924…  (all share "member-information")
//   - Saskatchewan: .../member-details?first=…&last=…  (all share "member-details")
var genericPageSegments = map[string]bool{
	"member-information": true,
	"member-details":     true,
}

// extractProvincialMemberID builds a deterministic ID for a provincial member
// as "{setSlug}-{name-slug}" where the name slug is derived from the last
// path segment of item.URL (a slugified name on most provincial sites), or
// falls back to a slugified version of item.Name when the URL is empty or its
// last path segment is a known generic page name shared by all members.
func extractProvincialMemberID(setSlug string, item representAPIItem) string {
	nameSlug := urlLastSegment(item.URL)
	if nameSlug == "" || genericPageSegments[nameSlug] {
		nameSlug = nameToSlug(item.Name)
	}
	if nameSlug == "" {
		return ""
	}
	return setSlug + "-" + nameSlug
}

// urlLastSegment returns the last non-empty path segment of rawURL.
// The URL is parsed properly so that query strings and fragments are
// excluded from the result, and percent-encoded characters are decoded.
func urlLastSegment(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Parse the URL so that query strings (e.g. ?id=42) and fragments (#foo)
	// are stripped before we look at the path.
	parsed, err := url.Parse(rawURL)
	parsedPath := rawURL
	if err == nil && parsed.Path != "" {
		parsedPath = parsed.Path
		rawURL = parsed.Path
	}
	rawURL = strings.TrimSuffix(rawURL, "/")
	if i := strings.LastIndex(rawURL, "/"); i >= 0 {
		rawURL = rawURL[i+1:]
	}
	// Many provincial profile URLs end with "/index.html"; in those cases we
	// need the parent path segment for stable unique IDs.
	if strings.EqualFold(rawURL, "index.html") || strings.EqualFold(rawURL, "index.htm") || strings.EqualFold(rawURL, "default.aspx") {
		parent := strings.TrimSuffix(parsedPath, "/")
		if i := strings.LastIndex(parent, "/"); i >= 0 {
			parent = parent[:i]
		}
		parent = strings.TrimSuffix(parent, "/")
		if i := strings.LastIndex(parent, "/"); i >= 0 {
			rawURL = parent[i+1:]
		}
	}
	// URL-decode the segment so that percent-encoded characters (e.g. %20)
	// don't end up in member IDs where they would cause URL routing mismatches.
	if decoded, derr := url.PathUnescape(rawURL); derr == nil {
		rawURL = decoded
	}
	return rawURL
}

// nameToSlug converts a display name to a URL-safe slug, e.g.
// "Laura Smith" → "laura-smith", "Émilise Lessard-Therrien" → "emilise-lessard-therrien".
// Common French/accented characters are transliterated to their ASCII equivalents.
func nameToSlug(name string) string {
	// Transliterate common accented characters before lowercasing.
	replacer := strings.NewReplacer(
		"À", "a", "Â", "a", "Ä", "a", "à", "a", "â", "a", "ä", "a",
		"Ç", "c", "ç", "c",
		"È", "e", "É", "e", "Ê", "e", "Ë", "e",
		"è", "e", "é", "e", "ê", "e", "ë", "e",
		"Î", "i", "Ï", "i", "î", "i", "ï", "i",
		"Ô", "o", "Ö", "o", "ô", "o", "ö", "o",
		"Ù", "u", "Û", "u", "Ü", "u", "ù", "u", "û", "u", "ü", "u",
		"Ÿ", "y", "ÿ", "y",
	)
	name = strings.ToLower(strings.TrimSpace(replacer.Replace(name)))
	// Replace spaces with hyphens; strip characters that aren't alphanumeric or hyphens.
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '\'':
			b.WriteByte('-')
		}
	}
	// Collapse multiple consecutive hyphens and trim.
	slug := b.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}

// ── New Brunswick member scraper ──────────────────────────────────────────────

// NBMembersURL is the canonical URL for current NB MLAs.
const NBMembersURL = "https://www.legnb.ca/en/members/current"

// CrawlNewBrunswickMembersFromWebsite scrapes current MLA profiles from the
// New Brunswick Legislative Assembly website. It is used as a fallback when
// the Represent OpenNorth API returns an empty result set for "nb-legislature".
//
// The page lists members in "LastName, FirstName" format; names are converted
// to "FirstName LastName" for storage so that the surname-based vote-matching
// logic in resolveProvincialMemberID works correctly.
func CrawlNewBrunswickMembersFromWebsite(indexURL string, client *http.Client) ([]MemberProfile, error) {
	if indexURL == "" {
		indexURL = NBMembersURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("nb members website: %w", err)
	}

	var profiles []MemberProfile
	doc.Find("div.member-card").Each(func(_ int, card *goquery.Selection) {
		nameEl := card.Find("li.member-card-description-name a[href]")
		href, _ := nameEl.Attr("href")
		rawName := strings.TrimSpace(nameEl.Find("h3").Text())
		if rawName == "" || href == "" {
			return
		}

		// Convert "LastName, FirstName" → "FirstName LastName".
		name := nbConvertMemberName(rawName)

		// Build a deterministic ID from the URL's last path segment,
		// matching the pattern used by extractProvincialMemberID.
		nameSlug := urlLastSegment(href)
		if nameSlug == "" {
			nameSlug = nameToSlug(name)
		}
		memberID := "nb-legislature-" + nameSlug

		// Party: text node inside the li, after the colour-dot div.
		partyLi := card.Find("li.member-card-description-party")
		partyLi.Find("div").Remove()
		party := strings.TrimSpace(partyLi.Text())

		// Riding: text inside the span.
		riding := strings.TrimSpace(card.Find("li.member-card-description-riding span").Text())

		// Photo URL: src attribute of the avatar image, resolved to absolute.
		photoSrc := strings.TrimSpace(card.Find("div.member-card-avatar img").AttrOr("src", ""))
		photoSrc = strings.ReplaceAll(photoSrc, "\\", "/")
		var photoURL string
		if photoSrc != "" {
			photoURL = resolveRelativeURL(indexURL, photoSrc)
		}

		profiles = append(profiles, MemberProfile{
			ID:              memberID,
			Name:            name,
			Party:           party,
			Riding:          riding,
			Province:        "New Brunswick",
			Role:            "MLA",
			PhotoURL:        photoURL,
			Chamber:         "legislature",
			Active:          true,
			LastScraped:     utils.NowISO(),
			GovernmentLevel: "provincial",
		})
	})

	log.Printf("[members] fetched %d NB members from website", len(profiles))
	return profiles, nil
}

// nbConvertMemberName converts a name in "LastName, FirstName" format (as used
// by the NB legislature website) to "FirstName LastName" for consistent storage.
// Names that don't contain a comma are returned unchanged.
func nbConvertMemberName(raw string) string {
	if idx := strings.Index(raw, ", "); idx >= 0 {
		last := strings.TrimSpace(raw[:idx])
		first := strings.TrimSpace(raw[idx+2:])
		if first != "" {
			return first + " " + last
		}
	}
	return raw
}

// ── Newfoundland and Labrador photo enrichment ────────────────────────────────

// nlMemberHrefRe matches a member profile href in the NL members-index.js file.
// Each entry looks like:  href="/Members/YourMember/DwyerJeff.aspx"
var nlMemberHrefRe = regexp.MustCompile(`href="(/Members/YourMember/([^"]+)\.aspx)"`)

// nlMemberNameRe matches the display name in the NL members-index.js file.
// Each entry looks like:  >Dwyer, Jeff</a>
var nlMemberNameRe = regexp.MustCompile(`>([^<]+)</a>`)

// enrichNLMemberPhotos fetches the Newfoundland and Labrador House of Assembly
// member-index JavaScript file and fills in the PhotoURL field for any member
// whose photo is currently empty.
//
// The NL Represent API returns member records without photo URLs.  The
// legislature website exposes a JavaScript data file that lists all members
// with their individual profile paths; photos live at a predictable sibling
// path:  /Members/YourMember/BioPhotos/{slug}.jpg
//
// Members are matched by normalised full name (case-insensitive, whitespace
// collapsed). Unmatched members are left unchanged.
func enrichNLMemberPhotos(profiles []MemberProfile, jsURL string, client *http.Client) []MemberProfile {
	if jsURL == "" {
		// Allow tests to redirect to a local server via NLMembersJSURLOverride.
		if NLMembersJSURLOverride != "" {
			jsURL = NLMembersJSURLOverride
		} else {
			jsURL = NLMembersJSURL
		}
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}

	resp, err := client.Get(jsURL)
	if err != nil {
		log.Printf("[members] NL photo enrichment: GET %s: %v", jsURL, err)
		return profiles
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[members] NL photo enrichment: read body: %v", err)
		return profiles
	}
	content := string(body)

	// Split on the entry separator (`},`) to process each member block.
	// Build a map: normalised "firstname lastname" → absolute photo URL.
	photoMap := make(map[string]string)
	// Each entry spans from `{` to `}`.  We scan for href/name pairs within
	// blocks delimited by `{` and `}`.
	blocks := strings.Split(content, "},")
	for _, block := range blocks {
		hrefMatch := nlMemberHrefRe.FindStringSubmatch(block)
		nameMatch := nlMemberNameRe.FindStringSubmatch(block)
		if hrefMatch == nil || nameMatch == nil {
			continue
		}
		slug := hrefMatch[2] // e.g. "DwyerJeff"
		// Unescape JS string escapes in the display name (e.g. "O\'Driscoll" → "O'Driscoll").
		rawName := strings.TrimSpace(strings.ReplaceAll(nameMatch[1], `\'`, "'"))
		// Convert "LastName, FirstName" to normalised "firstname lastname".
		displayName := nbConvertMemberName(rawName)
		normName := normalisePersonName(displayName)
		if normName == "" || slug == "" {
			continue
		}
		photoPath := "/Members/YourMember/BioPhotos/" + slug + ".jpg"
		photoMap[normName] = NLMembersBase + photoPath
	}

	if len(photoMap) == 0 {
		log.Printf("[members] NL photo enrichment: no entries parsed from %s", jsURL)
		return profiles
	}

	enriched := 0
	for i, p := range profiles {
		if p.PhotoURL != "" {
			continue
		}
		norm := normalisePersonName(p.Name)
		if photo, ok := photoMap[norm]; ok {
			profiles[i].PhotoURL = photo
			enriched++
		}
	}
	log.Printf("[members] NL photo enrichment: matched %d/%d members", enriched, len(profiles))
	return profiles
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
