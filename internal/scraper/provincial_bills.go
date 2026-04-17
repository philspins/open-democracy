package scraper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

var provincialBillNumberRe = regexp.MustCompile(`(?i)\bbill(?:\s+no\.?)?\s+([a-z]?-?\d+[a-z]?)\b`)
var provincialBillURLNumberRe = regexp.MustCompile(`(?i)(?:/bill-|/bill/|/bills?/)(\d{1,4}[a-z]?)(?:[/?#-]|$)`)
var provincialNestedBillURLNumberRe = regexp.MustCompile(`(?i)/\d{1,3}/\d{1,2}/(\d{1,4}[a-z]?)(?:/|$)`)
var provincialLeadingBillNumberRe = regexp.MustCompile(`(?m)^\s*(\d{1,4}[a-z]?)\s*(?:[.)]|\|)`)
var provincialStandaloneNumberRe = regexp.MustCompile(`(?i)^\s*(\d{1,4}[a-z]?)\s*$`)
var nlBillSessionDirLinkRe = regexp.MustCompile(`(?i)ga\d+session\d+/?$`)
var bcDynProgressIframeRe = regexp.MustCompile(`https?://dyn\.leg\.bc\.ca/progress-of-bills\?parliament=[^"']+&session=[^"']+`)
var albertaBillStatusPDFRe = regexp.MustCompile(`(?i)/LAO/Bills/bsr\d+-\d+\.pdf(?:\?[^"']*)?$`)
var albertaBillStatusEntryRe = regexp.MustCompile(`(?i)Bill\s+(Pr?\d+[A-Z]?)\s+--\s+(.+?)\s+First Reading\s+--`)
var manitobaCurrentSessionLinkRe = regexp.MustCompile(`(?i)\.\./\d+-\d+/index\.php$`)
var saskatchewanProgressPDFRe = regexp.MustCompile(`(?i)progress(?:-of)?-bills.*\.pdf$`)
var saskatchewanProgressEntryRe = regexp.MustCompile(`(?i)\b(\d{1,3}[A-Z]?)\s*(?:EN\s*)?(?:\*\s*)?((?:The|An|Loi).{1,260}?)(?:\s+(?:Member\s+)?[A-Z][A-Za-z'’.\-]+,\s+[A-Z][A-Za-z'’.\-]+(?:\s+[A-Z][A-Za-z'’.\-]+)?|\s+1st Reading|\s+Royal Rec\.)`)
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
		contextText := billContextText(a)
		if href == "" {
			return
		}
		if !linkMatcher.MatchString(text + " " + href) {
			return
		}

		billNumber := extractProvincialBillNumberWithContext(text, href, contextText)
		if billNumber == "" {
			return
		}

		id := ProvincialBillID(provinceCode, legislature, session, billNumber)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true

		detailURL := resolveRelativeURL(indexURL, href)
		title := billTitleFromContext(text, contextText, billNumber)
		if title == "" {
			title = "Bill " + billNumber
		}

		// Try to infer a date from the closest row/card text.
		lastActivity := utils.FindDateInText(contextText)

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

func billContextText(a *goquery.Selection) string {
	return strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(
		a.Closest("tr, li, article, section, div, table").Text(), "\u00a0", " ")), " "))
}

func extractProvincialBillNumberWithContext(text, href, contextText string) string {
	for _, candidate := range []string{text + " " + href, contextText, href} {
		if billNumber := ExtractProvincialBillNumber(candidate); billNumber != "" {
			return billNumber
		}
	}
	if m := provincialNestedBillURLNumberRe.FindStringSubmatch(href); len(m) == 2 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	if m := provincialBillURLNumberRe.FindStringSubmatch(href); len(m) == 2 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	if m := provincialLeadingBillNumberRe.FindStringSubmatch(contextText); len(m) == 2 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	return ""
}

func billTitleFromContext(anchorText, contextText, billNumber string) string {
	title := strings.TrimSpace(anchorText)
	if title != "" && !provincialStandaloneNumberRe.MatchString(title) {
		return title
	}
	contextText = strings.TrimSpace(contextText)
	if contextText == "" {
		return ""
	}
	if billNumber != "" {
		prefixPatterns := []string{
			"Bill " + billNumber,
			"Bill No. " + billNumber,
			billNumber + ".",
			billNumber + " |",
		}
		for _, prefix := range prefixPatterns {
			if strings.HasPrefix(contextText, prefix) {
				trimmed := strings.TrimSpace(strings.TrimPrefix(contextText, prefix))
				if trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return contextText
}

func CrawlAlbertaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.ab.ca/assembly-business/bills/bill-status"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	bills, err := crawlAlbertaBillsFromDashboard(indexURL, legislature, session, client)
	if err == nil && len(bills) > 0 {
		return bills, nil
	}
	bills, err = crawlAlbertaBillsFromStatusPDF(indexURL, legislature, session, client)
	if err == nil && len(bills) > 0 {
		return bills, nil
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "ab", legislature, session, "alberta", client, albertaBillLinkRe)
}

func CrawlBritishColumbiaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	if indexURL == "" || strings.Contains(indexURL, "leg.bc.ca/") {
		bills, err := crawlBritishColumbiaBillsFromLIMS(legislature, session, client)
		if err == nil && len(bills) > 0 {
			return bills, nil
		}
	}
	progressURL := indexURL
	if progressURL == "" || strings.Contains(progressURL, "leg.bc.ca/parliamentary-business/") {
		progressURL = fmt.Sprintf("https://dyn.leg.bc.ca/progress-of-bills?parliament=%s&session=%s",
			ordinalSuffix(legislature), ordinalSuffix(session))
	}
	doc, err := fetchDoc(progressURL, client)
	if err != nil {
		if indexURL != "" && progressURL != indexURL {
			return crawlProvincialBillsFromIndexWithMatcher(indexURL, "bc", legislature, session, "british_columbia", client, bcBillLinkRe)
		}
		return nil, fmt.Errorf("bc progress of bills: %w", err)
	}
	bills := parseStructuredProvincialBillRows(doc, progressURL, "bc", legislature, session, "british_columbia")
	if len(bills) == 0 && indexURL != "" {
		return crawlProvincialBillsFromIndexWithMatcher(indexURL, "bc", legislature, session, "british_columbia", client, bcBillLinkRe)
	}
	return bills, nil
}

func CrawlManitobaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://web2.gov.mb.ca/bills/sess/index.php"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	bills, err := crawlManitobaBillsCurrentSession(indexURL, legislature, session, client)
	if err == nil && len(bills) > 0 {
		return bills, nil
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "mb", legislature, session, "manitoba", client, manitobaBillLinkRe)
}

func CrawlNewBrunswickBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.legnb.ca/en/legislation/bills"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "nb", legislature, session, "new_brunswick", client, newBrunswickBillLinkRe)
}

func CrawlNewfoundlandAndLabradorBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.nl.ca/HouseBusiness/Bills/"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("nl bills index: %w", err)
	}
	wantSessionDir := fmt.Sprintf("ga%dsession%d/", legislature, session)
	sessionURLs := make([]string, 0)
	seen := make(map[string]bool)
	indexDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !nlBillSessionDirLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if !strings.HasSuffix(full, "/") {
			full += "/"
		}
		if seen[full] {
			return
		}
		seen[full] = true
		if strings.Contains(strings.ToLower(full), strings.ToLower(wantSessionDir)) {
			sessionURLs = append([]string{full}, sessionURLs...)
			return
		}
		sessionURLs = append(sessionURLs, full)
	})
	if len(sessionURLs) == 0 {
		return crawlProvincialBillsFromIndexWithMatcher(indexURL, "nl", legislature, session, "newfoundland_labrador", client, newfoundlandBillLinkRe)
	}
	if len(sessionURLs) > 4 {
		sessionURLs = sessionURLs[:4]
	}
	out := make([]ProvincialBillStub, 0)
	seenID := make(map[string]bool)
	for _, sessionURL := range sessionURLs {
		sessionDoc, derr := fetchDoc(sessionURL, client)
		if derr != nil {
			continue
		}
		for _, bill := range parseStructuredProvincialBillRows(sessionDoc, sessionURL, "nl", legislature, session, "newfoundland_labrador") {
			if seenID[bill.ID] {
				continue
			}
			seenID[bill.ID] = true
			out = append(out, bill)
		}
		if len(out) > 0 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) == 0 {
		return crawlProvincialBillsFromIndexWithMatcher(indexURL, "nl", legislature, session, "newfoundland_labrador", client, newfoundlandBillLinkRe)
	}
	return out, nil
}

func CrawlNovaScotiaBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://nslegislature.ca/legislative-business/bills-statutes/bills"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "ns", legislature, session, "nova_scotia", client, novaScotiaBillLinkRe)
}

func CrawlOntarioBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.ola.org/en/legislative-business"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "on", legislature, session, "ontario", client, genericBillLinkRe)
}

// peiWorkflowBills is the WDF workflow name for the PEI legislative bill search.
const peiWorkflowBills = "LegislativeAssemblyBillSearch"

// peiWDFBillItem is one bill record from the WDF bill-search workflow response.
// Field names match the camelCase JSON convention used by PEI's Vue.js WDF frontend.
type peiWDFBillItem struct {
	Title        string `json:"title"`
	BillNumber   string `json:"billNumber"`
	URL          string `json:"url"`
	PDFUrl       string `json:"pdfUrl"`
	Status       string `json:"status"`
	Date         string `json:"date"`
	LastActivity string `json:"lastActivityDate"`
	Year         int    `json:"year"`
}

// peiWDFBillsResponse is the top-level JSON envelope from the WDF bill-search endpoint.
type peiWDFBillsResponse struct {
	Items []peiWDFBillItem `json:"items"`
	Total int              `json:"totalCount"`
}

// crawlPEIBillsFromWorkflow queries the WDF bill-search workflow for PEI bill stubs.
// wdfBase overrides the WDF service root URL (useful for tests); it defaults to
// peiWDFAPIBase when empty. Returns (nil, nil) when the API is unavailable.
func crawlPEIBillsFromWorkflow(wdfBase string, year, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	xReferer := "https://www.assembly.pe.ca/legislative-business/house-records/bills"
	body, err := postPEIWorkflow(wdfBase, peiWorkflowBills, xReferer, year, client)
	if err != nil || body == nil {
		return nil, err
	}

	var data peiWDFBillsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		log.Printf("[pe-bills] wdf json decode: %v; falling back to HTML", err)
		return nil, nil
	}

	if len(data.Items) == 0 {
		log.Printf("[pe-bills] wdf returned 0 bill items; falling back to HTML")
		return nil, nil
	}

	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0, len(data.Items))
	for _, item := range data.Items {
		// Derive bill number from the dedicated field first, then fall back to
		// extracting it from the title text.
		billNumber := strings.ToUpper(strings.TrimSpace(item.BillNumber))
		if billNumber == "" {
			billNumber = ExtractProvincialBillNumber(item.Title)
		}
		if billNumber == "" {
			continue
		}

		id := ProvincialBillID("pe", legislature, session, billNumber)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		detailURL := resolveRelativeURL("https://www.assembly.pe.ca", item.URL)
		if detailURL == "" {
			detailURL = item.PDFUrl
		}

		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = "Bill " + billNumber
		}

		lastActivity := item.LastActivity
		if lastActivity == "" {
			lastActivity = item.Date
		}

		out = append(out, ProvincialBillStub{
			ID:               id,
			ProvinceCode:     "pe",
			Parliament:       legislature,
			Session:          session,
			Number:           billNumber,
			Title:            title,
			Chamber:          "pei",
			DetailURL:        detailURL,
			SourceURL:        strings.TrimRight(wdfBase, "/") + "/legislative-assembly/services/api/workflow",
			LastActivityDate: lastActivity,
			LastScraped:      utils.NowISO(),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	log.Printf("[pe-bills] wdf parsed %d bills", len(out))
	return out, nil
}

func CrawlPrinceEdwardIslandBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	defaultURL := indexURL == ""
	if defaultURL {
		indexURL = "https://www.assembly.pe.ca/legislative-business/house-records/bills"
	}
	// assembly.pe.ca is protected by a Radware bot-manager CAPTCHA. Use the same
	// browser-like transport used by CrawlPrinceEdwardIslandVotes when no client
	// is provided by the caller.
	if client == nil {
		client = newPEIHTTPClient()
	}

	// Attempt the WDF workflow API first. In production (defaultURL), use the
	// canonical WDF base. When a test server URL is passed, route the WDF call
	// through the same server so tests can mock both paths via the same mux.
	wdfBase := peiWDFAPIBase
	if !defaultURL {
		wdfBase = indexURL
	}
	year := time.Now().Year()
	bills, werr := crawlPEIBillsFromWorkflow(wdfBase, year, legislature, session, client)
	if werr == nil && len(bills) > 0 {
		return bills, nil
	}
	if werr != nil {
		log.Printf("[pe-bills] wdf api: %v; falling back to HTML", werr)
	}

	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "pe", legislature, session, "pei", client, peiBillLinkRe)
}

func CrawlQuebecBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.assnat.qc.ca/en/travaux-parlementaires/projets-loi/index.html"
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "qc", legislature, session, "quebec", client, quebecBillLinkRe)
}

func CrawlSaskatchewanBills(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if indexURL == "" {
		indexURL = "https://www.legassembly.sk.ca/legislative-business/bills/"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	bills, err := crawlSaskatchewanBillsFromProgressPDF(indexURL, legislature, session, client)
	if err == nil && len(bills) > 0 {
		return bills, nil
	}
	return crawlProvincialBillsFromIndexWithMatcher(indexURL, "sk", legislature, session, "saskatchewan", client, saskatchewanBillLinkRe)
}

type bcProgressBillFile struct {
	ReadingTypeID int    `json:"readingTypeId"`
	FileName      string `json:"fileName"`
}

type bcProgressBillRecord struct {
	BillNumber    int    `json:"billNumber"`
	Title         string `json:"title"`
	BillTypeID    int    `json:"billTypeId"`
	FirstReading  string `json:"firstReading"`
	SecondReading string `json:"secondReading"`
	Committee     string `json:"committeeReading"`
	Report        string `json:"reportReading"`
	Amended       string `json:"amendedReading"`
	ThirdReading  string `json:"thirdReading"`
	RoyalAssent   string `json:"royalAssent"`
	Files         struct {
		Nodes []bcProgressBillFile `json:"nodes"`
	} `json:"files"`
}

type bcGraphQLParliamentResponse struct {
	Data struct {
		AllParliaments struct {
			Nodes []struct {
				SessionsByParliamentID struct {
					Nodes []struct {
						ID     int `json:"id"`
						Number int `json:"number"`
					} `json:"nodes"`
				} `json:"sessionsByParliamentId"`
			} `json:"nodes"`
		} `json:"allParliaments"`
	} `json:"data"`
}

func crawlAlbertaBillsFromStatusPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	if strings.HasSuffix(strings.ToLower(indexURL), ".pdf") {
		return parseAlbertaBillStatusPDF(indexURL, legislature, session, client)
	}
	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, err
	}
	wantPDF := fmt.Sprintf("bsr%d-%d.pdf", legislature, session)
	pdfURL := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !albertaBillStatusPDFRe.MatchString(href) || !strings.Contains(strings.ToLower(href), strings.ToLower(wantPDF)) {
			return true
		}
		pdfURL = resolveRelativeURL(indexURL, href)
		return false
	})
	if pdfURL == "" {
		return nil, fmt.Errorf("alberta bill-status pdf not found for legislature %d session %d", legislature, session)
	}
	return parseAlbertaBillStatusPDF(pdfURL, legislature, session, client)
}

func crawlAlbertaBillsFromDashboard(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	dashboardURL := indexURL
	if dashboardURL == "" || strings.Contains(strings.ToLower(dashboardURL), "bill-status") || !strings.Contains(strings.ToLower(dashboardURL), "assembly-dashboard") {
		dashboardURL = fmt.Sprintf("https://www.assembly.ab.ca/assembly-business/assembly-dashboard?legl=%d&session=%d&sectionb=d&btn=i", legislature, session)
	}
	doc, err := fetchDoc(dashboardURL, client)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0)
	doc.Find("div.bills div.bill").Each(func(_ int, card *goquery.Selection) {
		item := card.Find("div.item").First()
		if item.Length() == 0 {
			return
		}
		numberText := strings.TrimSpace(strings.Join(strings.Fields(item.Find("a").First().Text()), " "))
		billNumber := ExtractProvincialBillNumber(numberText)
		if billNumber == "" {
			return
		}
		id := ProvincialBillID("ab", legislature, session, billNumber)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		title := strings.TrimSpace(strings.Join(strings.Fields(item.Find("div").Eq(1).Text()), " "))
		detailURL := dashboardURL
		if link := card.Find("div.doc_item a[href]").First(); link.Length() > 0 {
			detailURL = resolveRelativeURL(dashboardURL, link.AttrOr("href", ""))
		}
		out = append(out, ProvincialBillStub{
			ID:           id,
			ProvinceCode: "ab",
			Parliament:   legislature,
			Session:      session,
			Number:       billNumber,
			Title:        title,
			Chamber:      "alberta",
			DetailURL:    detailURL,
			SourceURL:    dashboardURL,
			LastScraped:  utils.NowISO(),
		})
	})
	return out, nil
}

func parseAlbertaBillStatusPDF(pdfURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	text, err := downloadAndExtractPDFText(pdfURL, "ab", client)
	if err != nil {
		return nil, err
	}
	matches := albertaBillStatusEntryRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	out := make([]ProvincialBillStub, 0, len(matches))
	seen := make(map[string]bool)
	for _, match := range matches {
		billNumber := strings.TrimSpace(match[1])
		title := strings.TrimSpace(match[2])
		id := ProvincialBillID("ab", legislature, session, billNumber)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ProvincialBillStub{
			ID:           id,
			ProvinceCode: "ab",
			Parliament:   legislature,
			Session:      session,
			Number:       billNumber,
			Title:        title,
			Chamber:      "alberta",
			DetailURL:    pdfURL,
			SourceURL:    pdfURL,
			LastScraped:  utils.NowISO(),
		})
	}
	return out, nil
}

func crawlBritishColumbiaBillsFromLIMS(legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	sessionID, err := fetchBritishColumbiaSessionID(legislature, session, client)
	if err != nil {
		return nil, err
	}
	resp, err := client.Get(fmt.Sprintf("%s/pdms/bills/progress-of-bills/%d", bcLIMSBase, sessionID))
	if err != nil {
		return nil, fmt.Errorf("bc progress api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bc progress api: status %d", resp.StatusCode)
	}
	var records []bcProgressBillRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, fmt.Errorf("bc progress api decode: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	out := make([]ProvincialBillStub, 0, len(records))
	for _, record := range records {
		billNumber := strconv.Itoa(record.BillNumber)
		if record.BillTypeID == 0 {
			billNumber = "M " + billNumber
		} else if record.BillTypeID == 3 {
			billNumber = "Pr" + billNumber
		}
		detailURL := fmt.Sprintf("https://www.leg.bc.ca/parliamentary-business/overview/%s-parliament/%s-session/bills/progress-of-bills", ordinalSuffix(legislature), ordinalSuffix(session))
		for _, file := range record.Files.Nodes {
			if path, ok := bcBillFileRoute(file.ReadingTypeID); ok && strings.TrimSpace(file.FileName) != "" {
				detailURL = fmt.Sprintf("https://www.leg.bc.ca/parliamentary-business/overview/%s-parliament/%s-session/bills/%s/%s", ordinalSuffix(legislature), ordinalSuffix(session), path, file.FileName)
				break
			}
		}
		lastActivity := strings.TrimSpace(record.RoyalAssent)
		for _, candidate := range []string{record.ThirdReading, record.Amended, record.Report, record.Committee, record.SecondReading, record.FirstReading} {
			if lastActivity == "" && strings.TrimSpace(candidate) != "" {
				lastActivity = strings.TrimSpace(candidate)
			}
		}
		out = append(out, ProvincialBillStub{
			ID:               ProvincialBillID("bc", legislature, session, billNumber),
			ProvinceCode:     "bc",
			Parliament:       legislature,
			Session:          session,
			Number:           billNumber,
			Title:            strings.TrimSpace(record.Title),
			Chamber:          "british_columbia",
			DetailURL:        detailURL,
			SourceURL:        fmt.Sprintf("%s/pdms/bills/progress-of-bills/%d", bcLIMSBase, sessionID),
			LastActivityDate: lastActivity,
			LastScraped:      utils.NowISO(),
		})
	}
	return out, nil
}

func fetchBritishColumbiaSessionID(legislature, session int, client *http.Client) (int, error) {
	body, err := json.Marshal(map[string]string{
		"query": fmt.Sprintf(`query { allParliaments(condition: { active: true, number: %d }, first: 1) { nodes { sessionsByParliamentId(condition: { active: true }, orderBy: NUMBER_DESC, first: 10) { nodes { id number } } } } }`, legislature),
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, bcLIMSBase+"/graphql", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bc graphql: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bc graphql: status %d", resp.StatusCode)
	}
	var payload bcGraphQLParliamentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("bc graphql decode: %w", err)
	}
	for _, parliament := range payload.Data.AllParliaments.Nodes {
		for _, candidate := range parliament.SessionsByParliamentID.Nodes {
			if candidate.Number == session {
				return candidate.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("bc session id not found for legislature %d session %d", legislature, session)
}

func bcBillFileRoute(readingTypeID int) (string, bool) {
	switch readingTypeID {
	case 1:
		return "1st_read", true
	case 2:
		return "amend", true
	case 3:
		return "3rd_read", true
	default:
		return "", false
	}
}

func crawlManitobaBillsCurrentSession(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, err
	}
	currentURL := indexURL
	indexDoc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !manitobaCurrentSessionLinkRe.MatchString(href) {
			return true
		}
		if goquery.NodeName(a.Parent()) != "li" || strings.ToLower(strings.TrimSpace(a.Parent().AttrOr("id", ""))) != "active" {
			return true
		}
		currentURL = resolveRelativeURL(indexURL, href)
		return false
	})
	if currentURL != indexURL {
		indexDoc, err = fetchDoc(currentURL, client)
		if err != nil {
			return nil, err
		}
	}
	return parseManitobaBillRows(indexDoc, currentURL, legislature, session), nil
}

func parseManitobaBillRows(doc *goquery.Document, sourceURL string, legislature, session int) []ProvincialBillStub {
	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0)
	doc.Find("table.index tr").Each(func(_ int, tr *goquery.Selection) {
		cells := tr.Find("td")
		if cells.Length() < 3 {
			return
		}
		billNumber := strings.TrimSpace(strings.Join(strings.Fields(cells.First().Text()), " "))
		if !provincialStandaloneNumberRe.MatchString(billNumber) {
			billNumber = extractProvincialBillNumberWithContext(billNumber, "", strings.TrimSpace(strings.Join(strings.Fields(tr.Text()), " ")))
		}
		if billNumber == "" {
			return
		}
		id := ProvincialBillID("mb", legislature, session, billNumber)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		titleCell := cells.Eq(2)
		title := strings.TrimSpace(strings.Join(strings.Fields(titleCell.Text()), " "))
		detailURL := sourceURL
		if link := titleCell.Find("a[href]").First(); link.Length() > 0 {
			detailURL = resolveRelativeURL(sourceURL, link.AttrOr("href", ""))
		}
		out = append(out, ProvincialBillStub{
			ID:           id,
			ProvinceCode: "mb",
			Parliament:   legislature,
			Session:      session,
			Number:       billNumber,
			Title:        title,
			Chamber:      "manitoba",
			DetailURL:    detailURL,
			SourceURL:    sourceURL,
			LastScraped:  utils.NowISO(),
		})
	})
	return out
}

func crawlSaskatchewanBillsFromProgressPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, err
	}
	pdfURL := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !saskatchewanProgressPDFRe.MatchString(href) {
			return true
		}
		pdfURL = resolveRelativeURL(indexURL, href)
		return false
	})
	if pdfURL == "" {
		return nil, nil
	}
	text, err := downloadAndExtractPDFText(pdfURL, "sk", client)
	if err != nil {
		return nil, err
	}
	normalized := strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	matches := saskatchewanProgressEntryRe.FindAllStringSubmatch(normalized, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0, len(matches))
	for _, match := range matches {
		billNumber := strings.TrimSpace(match[1])
		title := strings.TrimSpace(match[2])
		id := ProvincialBillID("sk", legislature, session, billNumber)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ProvincialBillStub{
			ID:           id,
			ProvinceCode: "sk",
			Parliament:   legislature,
			Session:      session,
			Number:       billNumber,
			Title:        title,
			Chamber:      "saskatchewan",
			DetailURL:    pdfURL,
			SourceURL:    pdfURL,
			LastScraped:  utils.NowISO(),
		})
	}
	return out, nil
}

func parseStructuredProvincialBillRows(doc *goquery.Document, sourceURL, provinceCode string, legislature, session int, chamber string) []ProvincialBillStub {
	seen := make(map[string]bool)
	out := make([]ProvincialBillStub, 0)
	doc.Find("table tr").Each(func(_ int, tr *goquery.Selection) {
		cells := tr.Find("th, td")
		if cells.Length() < 2 {
			return
		}
		numberText := strings.TrimSpace(strings.Join(strings.Fields(cells.First().Text()), " "))
		billNumber := extractProvincialBillNumberWithContext(numberText, tr.Find("a[href]").First().AttrOr("href", ""), strings.TrimSpace(strings.Join(strings.Fields(tr.Text()), " ")))
		if billNumber == "" {
			return
		}
		id := ProvincialBillID(provinceCode, legislature, session, billNumber)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		titleCell := cells.Eq(1)
		title := strings.TrimSpace(strings.Join(strings.Fields(titleCell.Text()), " "))
		if title == "" {
			title = "Bill " + billNumber
		}
		detailURL := sourceURL
		if link := titleCell.Find("a[href]").First(); link.Length() > 0 {
			detailURL = resolveRelativeURL(sourceURL, link.AttrOr("href", ""))
		}
		lastActivity := utils.FindDateInText(strings.TrimSpace(strings.Join(strings.Fields(tr.Text()), " ")))
		out = append(out, ProvincialBillStub{
			ID:               id,
			ProvinceCode:     provinceCode,
			Parliament:       legislature,
			Session:          session,
			Number:           billNumber,
			Title:            title,
			Chamber:          chamber,
			DetailURL:        detailURL,
			SourceURL:        sourceURL,
			LastActivityDate: lastActivity,
			LastScraped:      utils.NowISO(),
		})
	})
	return out
}

func ordinalSuffix(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return strconv.Itoa(n) + "th"
	}
	switch n % 10 {
	case 1:
		return strconv.Itoa(n) + "st"
	case 2:
		return strconv.Itoa(n) + "nd"
	case 3:
		return strconv.Itoa(n) + "rd"
	default:
		return strconv.Itoa(n) + "th"
	}
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
