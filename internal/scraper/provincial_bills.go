package scraper

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/philspins/open-democracy/internal/utils"
)

// ProvincialBillStub is a lightweight bill record scraped from a provincial
// legislature listing page.
type ProvincialBillStub struct {
	ID               string
	ProvinceCode     string
	Parliament       int
	Session          int
	Number           string
	Title            string
	Chamber          string
	DetailURL        string
	SourceURL        string
	LastActivityDate string
	LastScraped      string
}

var provincialBillNumberRe = regexp.MustCompile(`(?i)\bbill\s+([a-z]?-?\d+[a-z]?)\b`)
var genericBillLinkRe = regexp.MustCompile(`(?i)(bill|legislation|legislative-business|housebusiness|bills-and-legislation|legis)`)
var albertaBillLinkRe = regexp.MustCompile(`(?i)(assembly-business|bill|bills)`)
var bcBillLinkRe = regexp.MustCompile(`(?i)(bills-and-legislation|bill)`)
var manitobaBillLinkRe = regexp.MustCompile(`(?i)(businessofthehouse|bill|legislature)`)
var newBrunswickBillLinkRe = regexp.MustCompile(`(?i)(legis|bill|projet)`)
var newfoundlandBillLinkRe = regexp.MustCompile(`(?i)(housebusiness|bill|legislation)`)
var novaScotiaBillLinkRe = regexp.MustCompile(`(?i)(bills-statutes|bill|legislative-business)`)
var peiBillLinkRe = regexp.MustCompile(`(?i)(legislative-business|bill)`)
var quebecBillLinkRe = regexp.MustCompile(`(?i)(travaux-parlementaires|projets-de-loi|bill)`)
var saskatchewanBillLinkRe = regexp.MustCompile(`(?i)(legislative-business/bills|/bills/)`)

// ExtractProvincialBillNumber extracts a bill number from provincial text.
// Examples: "Bill 12" -> "12", "bill a-23" -> "A-23".
func ExtractProvincialBillNumber(text string) string {
	if m := provincialBillNumberRe.FindStringSubmatch(text); len(m) == 2 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	// Fall back to federal bill-number format (C-47 / S-209) when present.
	return utils.ExtractBillNumber(text)
}

// ProvincialBillID builds a deterministic provincial bill ID.
// Format: "{province}-{legislature}-{session}-{bill_number}".
func ProvincialBillID(province string, legislature, session int, billNumber string) string {
	clean := strings.ToLower(strings.TrimSpace(billNumber))
	clean = strings.ReplaceAll(clean, " ", "")
	clean = strings.ReplaceAll(clean, "/", "-")
	clean = strings.ReplaceAll(clean, "–", "-")
	clean = strings.ReplaceAll(clean, "—", "-")
	if clean == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d-%d-%s", province, legislature, session, clean)
}

// CrawlProvincialBillsFromIndex scrapes a provincial legislative-business page
// and returns bill stubs discovered from links containing bill numbers.
func CrawlProvincialBillsFromIndex(indexURL, provinceCode string, legislature, session int, chamber string, client *http.Client) ([]ProvincialBillStub, error) {
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, provinceCode, legislature, session, chamber, client, genericBillLinkRe)
}

func crawlProvincialBillsFromIndexWithMatcher(indexURL, provinceCode string, legislature, session int, chamber string, client *http.Client, linkMatcher *regexp.Regexp) ([]ProvincialBillStub, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("provincial bills index: %w", err)
	}
	return parseProvincialBillsIndexDoc(doc, indexURL, provinceCode, legislature, session, chamber, linkMatcher), nil
}

func parseProvincialBillsIndexDoc(doc *goquery.Document, indexURL, provinceCode string, legislature, session int, chamber string, linkMatcher *regexp.Regexp) []ProvincialBillStub {
	if linkMatcher == nil {
		linkMatcher = genericBillLinkRe
	}
	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0)

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		text := strings.TrimSpace(strings.Join(strings.Fields(a.Text()), " "))
		if href == "" {
			return
		}
		if !linkMatcher.MatchString(text + " " + href) {
			return
		}

		candidate := text + " " + href
		billNumber := ExtractProvincialBillNumber(candidate)
		if billNumber == "" {
			return
		}

		id := ProvincialBillID(provinceCode, legislature, session, billNumber)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true

		detailURL := resolveRelativeURL(indexURL, href)
		title := text
		if title == "" {
			title = "Bill " + billNumber
		}

		// Try to infer a date from the closest row/card text.
		dateText := strings.TrimSpace(a.Closest("tr, li, article, section, div").Text())
		lastActivity := utils.FindDateInText(dateText)

		out = append(out, ProvincialBillStub{
			ID:               id,
			ProvinceCode:     provinceCode,
			Parliament:       legislature,
			Session:          session,
			Number:           billNumber,
			Title:            title,
			Chamber:          chamber,
			DetailURL:        detailURL,
			SourceURL:        indexURL,
			LastActivityDate: lastActivity,
			LastScraped:      utils.NowISO(),
		})
	})

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func CrawlAlbertaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.ab.ca/assembly-business"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "ab", legislature, session, "alberta", client, albertaBillLinkRe)
}

func CrawlBritishColumbiaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.leg.bc.ca/parliamentary-business/bills-and-legislation"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "bc", legislature, session, "british_columbia", client, bcBillLinkRe)
}

func CrawlManitobaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.gov.mb.ca/legislature/businessofthehouse/index.html"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "mb", legislature, session, "manitoba", client, manitobaBillLinkRe)
}

func CrawlNewBrunswickBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.gnb.ca/legis/legis-e.asp"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "nb", legislature, session, "new_brunswick", client, newBrunswickBillLinkRe)
}

func CrawlNewfoundlandAndLabradorBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.nl.ca/HouseBusiness/"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "nl", legislature, session, "newfoundland_labrador", client, newfoundlandBillLinkRe)
}

func CrawlNovaScotiaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://nslegislature.ca/legislative-business"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "ns", legislature, session, "nova_scotia", client, novaScotiaBillLinkRe)
}

func CrawlOntarioBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.ola.org/en/legislative-business"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "on", legislature, session, "ontario", client, genericBillLinkRe)
}

func CrawlPrinceEdwardIslandBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.pe.ca/legislative-business"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "pe", legislature, session, "pei", client, peiBillLinkRe)
}

func CrawlQuebecBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assnat.qc.ca/en/travaux-parlementaires/"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "qc", legislature, session, "quebec", client, quebecBillLinkRe)
}

func CrawlSaskatchewanBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.legassembly.sk.ca/legislative-business/bills/"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "sk", legislature, session, "saskatchewan", client, saskatchewanBillLinkRe)
}

func resolveRelativeURL(baseURL, href string) string {
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}
	rel, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(rel).String()
}
