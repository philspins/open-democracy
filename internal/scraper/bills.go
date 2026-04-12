// Package scraper implements all the data scrapers for Open Democracy.
//
// Bills:
//   - LEGISinfo RSS feed   → bill stubs
//   - LEGISinfo detail page → stage timeline, sponsor, status
//   - Library of Parliament → plain-English summaries
package scraper

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/mmcdole/gofeed"
	"github.com/philspins/open-democracy/internal/utils"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	RSSUrl          = "https://www.parl.ca/legisinfo/en/bills/rss"
	LegisInfoBase   = "https://www.parl.ca/legisinfo/en/bill"
	DocumentViewer  = "https://www.parl.ca/DocumentViewer/en/%d-%d/bill/%s/first-reading"
	// LibraryOfParliamentBase is the base URL for professional researcher summaries.
	// Use these instead of AI-generated summaries where available.
	LibraryOfParliamentBase = "https://lop.parl.ca/sites/PublicWebsite/default/en_CA/ResearchPublications/LegislativeSummaries"
)

// StageOrder defines the canonical reading order.
var StageOrder = []string{
	"1st_reading",
	"2nd_reading",
	"committee",
	"3rd_reading",
	"royal_assent",
}

// stageAnchorMap maps page-anchor substrings to canonical stage keys.
var stageAnchorMap = map[string]string{
	"firstreading":   "1st_reading",
	"1st-reading":    "1st_reading",
	"first-reading":  "1st_reading",
	"secondreading":  "2nd_reading",
	"2nd-reading":    "2nd_reading",
	"second-reading": "2nd_reading",
	"committee":      "committee",
	"thirdreading":   "3rd_reading",
	"3rd-reading":    "3rd_reading",
	"third-reading":  "3rd_reading",
	"royalassent":    "royal_assent",
	"royal-assent":   "royal_assent",
}

// ── types ─────────────────────────────────────────────────────────────────────

// BillStub is a lightweight bill record built from the RSS feed.
type BillStub struct {
	ID               string
	Title            string
	LegisInfoURL     string
	LastActivityDate string
}

// BillDetail holds enriched data scraped from the LEGISinfo bill detail page.
type BillDetail struct {
	CurrentStatus  string
	CurrentStage   string
	Stages         []StageRecord
	SponsorID      string
	BillType       string
	FullTextURL    string
	IntroducedDate string
	LastScraped    string
}

// StageRecord is one entry in a bill's legislative stage timeline.
type StageRecord struct {
	Stage   string
	Chamber string
	Date    string
}

// ── RSS feed ──────────────────────────────────────────────────────────────────

// CrawlBillsRSS fetches the LEGISinfo RSS feed and returns a list of bill stubs.
func CrawlBillsRSS(rssURL string, client *http.Client) ([]BillStub, error) {
	if rssURL == "" {
		rssURL = RSSUrl
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}

	log.Printf("[bills] fetching RSS: %s", rssURL)

	fp := gofeed.NewParser()
	fp.Client = client
	feed, err := fp.ParseURL(rssURL)
	if err != nil {
		return nil, fmt.Errorf("parse RSS %q: %w", rssURL, err)
	}

	var stubs []BillStub
	for _, item := range feed.Items {
		billID := utils.ExtractBillID(item.Link)
		if billID == "" {
			log.Printf("[bills] skipping RSS entry with unparseable link: %s", item.Link)
			continue
		}
		var lastActivity string
		if item.UpdatedParsed != nil {
			lastActivity = item.UpdatedParsed.Format("2006-01-02")
		} else if item.PublishedParsed != nil {
			lastActivity = item.PublishedParsed.Format("2006-01-02")
		}
		stubs = append(stubs, BillStub{
			ID:               billID,
			Title:            strings.TrimSpace(item.Title),
			LegisInfoURL:     item.Link,
			LastActivityDate: lastActivity,
		})
	}
	log.Printf("[bills] RSS contained %d bills", len(stubs))
	return stubs, nil
}

// ── Bill detail page ──────────────────────────────────────────────────────────

// CrawlBillDetail scrapes the LEGISinfo detail page for a single bill.
func CrawlBillDetail(billID, url string, client *http.Client) (BillDetail, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[bills] scraping detail: %s", url)

	resp, err := client.Get(url)
	if err != nil {
		return BillDetail{}, fmt.Errorf("GET %q: %w", url, err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return BillDetail{}, fmt.Errorf("parse HTML %q: %w", url, err)
	}

	// Current status
	currentStatus := doc.Find(
		".bill-latest-activity, .bill-progress-current",
	).First().Text()
	currentStatus = strings.TrimSpace(currentStatus)

	// Stage timeline
	var stages []StageRecord
	var currentStage string

	doc.Find("[id]").Each(func(_ int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		idLower := strings.ToLower(id)
		for anchor, canonical := range stageAnchorMap {
			if strings.Contains(idLower, anchor) {
				date := findNearbyDate(s)
				stages = append(stages, StageRecord{
					Stage:   canonical,
					Chamber: utils.BillChamber(utils.BillNumberFromID(billID)),
					Date:    date,
				})
				currentStage = canonical
				return
			}
		}
	})

	// Sort by stage order
	stages = sortStages(stages)
	if len(stages) > 0 {
		currentStage = stages[len(stages)-1].Stage
	}

	// Sponsor
	var sponsorID string
	doc.Find(".bill-profile-sponsor a, [class*='sponsor'] a").Each(func(_ int, s *goquery.Selection) {
		if sponsorID == "" {
			if href, exists := s.Attr("href"); exists {
				sponsorID = utils.ExtractMemberID(href)
			}
		}
	})

	// Bill type
	billType := strings.TrimSpace(doc.Find(
		".bill-type, [class*='billType']",
	).First().Text())

	// Introduced date (from 1st reading stage)
	var introducedDate string
	for _, st := range stages {
		if st.Stage == "1st_reading" {
			introducedDate = st.Date
			break
		}
	}

	// Full text URL: bill_id = "{parl}-{session}-{billNum}"
	parl, sess, ok := utils.ParliamentSessionFromBillID(billID)
	var fullTextURL string
	if ok {
		billNum := utils.BillNumberFromID(billID)
		fullTextURL = fmt.Sprintf(DocumentViewer, parl, sess, billNum)
	}

	return BillDetail{
		CurrentStatus:  currentStatus,
		CurrentStage:   currentStage,
		Stages:         stages,
		SponsorID:      sponsorID,
		BillType:       billType,
		FullTextURL:    fullTextURL,
		IntroducedDate: introducedDate,
		LastScraped:    utils.NowISO(),
	}, nil
}

// findNearbyDate looks for a date in the element, its next sibling, or its parent.
func findNearbyDate(s *goquery.Selection) string {
	if d := utils.FindDateInText(s.Text()); d != "" {
		return d
	}
	if d := utils.FindDateInText(s.Next().Text()); d != "" {
		return d
	}
	if d := utils.FindDateInText(s.Parent().Text()); d != "" {
		return d
	}
	return ""
}

// sortStages returns stages in canonical order (1st reading → royal assent).
func sortStages(in []StageRecord) []StageRecord {
	stageIndex := func(s string) int {
		for i, v := range StageOrder {
			if v == s {
				return i
			}
		}
		return 99
	}

	out := make([]StageRecord, len(in))
	copy(out, in)
	// Simple insertion sort — at most 5 stages
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && stageIndex(out[j].Stage) < stageIndex(out[j-1].Stage); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ── Library of Parliament summary ─────────────────────────────────────────────

// CrawlLibraryOfParliamentSummary attempts to fetch a plain-English bill
// summary from the Library of Parliament website. Returns an empty string when
// not available.
func CrawlLibraryOfParliamentSummary(billNumber string, parliament, session int, client *http.Client) string {
	if client == nil {
		client = utils.NewHTTPClient()
	}

	// URL structure: /LegislativeSummaries?ls=C47&Parl=45&Ses=1
	slug := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(billNumber, "")
	url := fmt.Sprintf("%s?ls=%s&Parl=%d&Ses=%d", LibraryOfParliamentBase, slug, parliament, session)
	log.Printf("[bills] fetching Library of Parliament summary: %s", url)

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("[bills] Library of Parliament unavailable for %s: %v", billNumber, err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return ""
	}

	var paragraphs []string
	doc.Find(
		".views-field-body .field-content p, .field-item p, article .field--type-text-with-summary p",
	).Each(func(i int, s *goquery.Selection) {
		if i < 3 {
			paragraphs = append(paragraphs, strings.TrimSpace(s.Text()))
		}
	})

	return strings.Join(paragraphs, " ")
}

// ── Fetching raw HTML (shared helper) ─────────────────────────────────────────

// fetchDoc is a convenience helper that fetches a URL and parses the body
// with goquery. The caller is responsible for providing a polite HTTP client.
func fetchDoc(url string, client *http.Client) (*goquery.Document, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %q: status %d — %s", url, resp.StatusCode, body)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}
