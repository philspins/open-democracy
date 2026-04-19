// Provincial votes scrapers: Ontario Votes & Proceedings and Saskatchewan Assembly Minutes.
package scraper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/philspins/open-democracy/internal/utils"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	// Ontario
	OntarioVPIndexURL = "https://www.ola.org/en/legislative-business/house-documents/parliament-44/session-1"
	OntarioParliament = 44
	OntarioSession    = 1

	// Saskatchewan
	// NOTE: The archive URL currently returns HTTP 500. The new SK minutes-votes page
	// (/legislative-business/minutes-votes/) loads document links via JavaScript and
	// has no static HTML equivalents. CrawlSaskatchewanMinutesLinks will fail; the
	// error is now logged as a warning and the crawl continues with 0 divisions.
	SaskatchewanArchiveURL  = "https://www.legassembly.sk.ca/legislative-business/archive/?Start=&End=&Type=Assembly"
	SaskatchewanLegislature = 30
	SaskatchewanSession     = 2
)

// ── types ─────────────────────────────────────────────────────────────────────

// ProvincialMemberVote records how a single MLA voted in a provincial division.
// MemberName holds the raw display name from the source page (e.g. "Smith" for
// Ontario, "Scott Moe" for Saskatchewan); callers resolve it to a DB member ID.
type ProvincialMemberVote struct {
	DivisionID string
	MemberName string
	Vote       string // "Yea" | "Nay"
}

// ProvincialDivisionResult bundles a parsed division stub with its raw member votes.
type ProvincialDivisionResult struct {
	Division DivisionStub
	Votes    []ProvincialMemberVote
}

// ProvincialDivisionID builds a namespaced division ID for a provincial division.
// Format: "{province}-{legislature}-{session}-{date}-{num}"
// e.g. "on-44-1-2026-04-14-1"
func ProvincialDivisionID(province string, legislature, session, num int, date string) string {
	return fmt.Sprintf("%s-%d-%d-%s-%d", province, legislature, session, date, num)
}

// ── Ontario Votes and Proceedings ────────────────────────────────────────────

// CrawlOntarioVPSittingDates fetches the Ontario legislature session index page
// and returns the list of sitting dates that have a Votes and Proceedings document.
func CrawlOntarioVPSittingDates(indexURL string, parliament, session int, client *http.Client) ([]string, error) {
	if indexURL == "" {
		indexURL = fmt.Sprintf(
			"https://www.ola.org/en/legislative-business/house-documents/parliament-%d/session-%d",
			parliament, session,
		)
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[ontario-votes] fetching session index: %s", indexURL)

	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("ontario VP index: %w", err)
	}

	seen := make(map[string]bool)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")

		// Legacy/expected link: .../YYYY-MM-DD/votes-proceedings
		if strings.HasSuffix(href, "/votes-proceedings") {
			withoutSuffix := strings.TrimSuffix(href, "/votes-proceedings")
			if i := strings.LastIndex(withoutSuffix, "/"); i >= 0 {
				date := withoutSuffix[i+1:]
				if len(date) == 10 && date[4] == '-' && date[7] == '-' {
					seen[date] = true
					return
				}
			}
		}

		// Current OLA index commonly links to /hansard for dates where V&P
		// content exists. /orders-notices dates can legitimately have no V&P page.
		if strings.HasSuffix(href, "/hansard") {
			if m := ontarioHouseDocDatePathRe.FindStringSubmatch(href); len(m) == 2 {
				seen[m[1]] = true
			}
			return
		}

		if m := ontarioHouseDocDatePathRe.FindStringSubmatch(href); len(m) == 2 && strings.Contains(href, "/votes-proceedings") {
			seen[m[1]] = true
		}
	})

	dates := make([]string, 0, len(seen))
	for d := range seen {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	log.Printf("[ontario-votes] found %d sitting dates with V&P", len(dates))
	return dates, nil
}

// OntarioVPDayURL returns the canonical URL for the Ontario V&P page on a given date.
func OntarioVPDayURL(parliament, session int, date string) string {
	return fmt.Sprintf(
		"https://www.ola.org/en/legislative-business/house-documents/parliament-%d/session-%d/%s/votes-proceedings",
		parliament, session, date,
	)
}

// CrawlOntarioVPDay scrapes a single Ontario Votes and Proceedings page for the
// given date. vpURL is the full URL for the page (use OntarioVPDayURL to build it).
func CrawlOntarioVPDay(vpURL string, parliament, session int, date string, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[ontario-votes] scraping V&P: %s", vpURL)

	doc, err := fetchDoc(vpURL, client)
	if err != nil {
		return nil, fmt.Errorf("ontario VP %s: %w", date, err)
	}

	return parseOntarioVPDoc(doc, parliament, session, date), nil
}

var ontarioDivCountRe = regexp.MustCompile(`\((\d+)\)`)
var ontarioHouseDocDatePathRe = regexp.MustCompile(`/parliament-\d+/session-\d+/(\d{4}-\d{2}-\d{2})/`)

// parseOntarioVPDoc is the pure HTML-parsing logic for Ontario V&P pages.
// Separated from CrawlOntarioVPDay so tests can call it without a network round-trip.
func parseOntarioVPDoc(doc *goquery.Document, parliament, session int, date string) []ProvincialDivisionResult {
	var results []ProvincialDivisionResult
	divNum := 0

	// Each recorded division is rendered inside a div.datawrapper that contains
	// alternating h5.divisionHeader / table.votesList pairs (Ayes then Nays).
	doc.Find("div.datawrapper").Each(func(_ int, wrapper *goquery.Selection) {
		divNum++
		divID := ProvincialDivisionID("on", parliament, session, divNum, date)

		var votes []ProvincialMemberVote
		yeas, nays := 0, 0
		currentVoteType := ""

		wrapper.Children().Each(func(_ int, child *goquery.Selection) {
			if child.Is("h5.divisionHeader") {
				headerText := child.Text()
				enText := strings.ToLower(strings.TrimSpace(child.Find("span[lang='en']").First().Text()))
				switch enText {
				case "ayes":
					currentVoteType = "Yea"
					if m := ontarioDivCountRe.FindStringSubmatch(headerText); len(m) == 2 {
						yeas, _ = strconv.Atoi(m[1])
					}
				case "nays":
					currentVoteType = "Nay"
					if m := ontarioDivCountRe.FindStringSubmatch(headerText); len(m) == 2 {
						nays, _ = strconv.Atoi(m[1])
					}
				}
				return
			}

			if !child.Is("table.votesList") || currentVoteType == "" {
				return
			}
			vt := currentVoteType
			child.Find("td div[lang='en']").Each(func(_ int, div *goquery.Selection) {
				if div.HasClass("docHide") {
					return
				}
				name := strings.TrimSpace(div.Text())
				if name == "" || name == "\u00a0" {
					return
				}
				votes = append(votes, ProvincialMemberVote{
					DivisionID: divID,
					MemberName: name,
					Vote:       vt,
				})
			})
		})

		// Description: English text from the preceding sibling table, with the
		// "Carried on the following division:" tail stripped.
		desc := ""
		wrapper.Closest("table").PrevAll().Filter("table").First().Each(func(_ int, t *goquery.Selection) {
			t.Find("td[lang='en']").Each(func(_ int, cell *goquery.Selection) {
				text := strings.TrimSpace(cell.Text())
				if i := strings.Index(text, "Carried on the following division:"); i >= 0 {
					text = strings.TrimSpace(text[:i])
				}
				if text != "" && desc == "" {
					desc = text
				}
			})
		})

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  parliament,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: desc,
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     "ontario",
				LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
	})

	log.Printf("[ontario-votes] %s: parsed %d divisions", date, len(results))
	return results
}

// ── Saskatchewan Assembly Minutes ────────────────────────────────────────────

// CrawlSaskatchewanMinutesLinks fetches the Saskatchewan legislature archive page
// and returns the list of Assembly Minutes HTML document URLs.
func CrawlSaskatchewanMinutesLinks(archiveURL string, client *http.Client) ([]string, error) {
	if archiveURL == "" {
		archiveURL = SaskatchewanArchiveURL
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[sk-votes] fetching archive: %s", archiveURL)

	doc, err := fetchDoc(archiveURL, client)
	if err != nil {
		return nil, fmt.Errorf("sk archive: %w", err)
	}

	var links []string
	seen := make(map[string]bool)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if strings.Contains(href, "legdocs/Assembly/Minutes/") && strings.HasSuffix(href, "Minutes-HTML.htm") {
			if !seen[href] {
				seen[href] = true
				links = append(links, href)
			}
		}
	})

	log.Printf("[sk-votes] found %d Assembly Minutes HTML links", len(links))
	return links, nil
}

var skDateFromURLRe = regexp.MustCompile(`/(\d{8})Minutes-HTML\.htm`)
var skCountRe = regexp.MustCompile(`(?:YEAS|NAYS)[^\d]*(\d+)`)
var isoDateFromURLRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}|\d{8})`)

// CrawlSaskatchewanMinutes scrapes a single Saskatchewan Assembly Minutes HTML document.
// legislature and session are used to build division IDs.
func CrawlSaskatchewanMinutes(minutesURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[sk-votes] scraping Minutes: %s", minutesURL)

	m := skDateFromURLRe.FindStringSubmatch(minutesURL)
	if len(m) != 2 {
		return nil, fmt.Errorf("sk Minutes: cannot extract date from URL %s", minutesURL)
	}
	raw := m[1] // "20260414"
	date := fmt.Sprintf("%s-%s-%s", raw[:4], raw[4:6], raw[6:8])

	doc, err := fetchDoc(minutesURL, client)
	if err != nil {
		return nil, fmt.Errorf("sk Minutes %s: %w", date, err)
	}

	return parseSaskatchewanMinutesDoc(doc, legislature, session, date), nil
}

// parseSaskatchewanMinutesDoc is the pure HTML-parsing logic for Saskatchewan Minutes.
func parseSaskatchewanMinutesDoc(doc *goquery.Document, legislature, session int, date string) []ProvincialDivisionResult {
	var results []ProvincialDivisionResult
	divNum := 0

	doc.Find("table").Each(func(_ int, t *goquery.Selection) {
		if !strings.Contains(t.Text(), "YEAS") {
			return
		}

		divNum++
		divID := ProvincialDivisionID("sk", legislature, session, divNum, date)

		var votes []ProvincialMemberVote
		yeas, nays := 0, 0

		t.Find("td").Each(func(_ int, cell *goquery.Selection) {
			cellText := cell.Text()

			var voteType string
			if strings.Contains(cellText, "YEAS") {
				voteType = "Yea"
			} else if strings.Contains(cellText, "NAYS") {
				voteType = "Nay"
			}
			if voteType == "" {
				return
			}

			// Extract count from "YEAS / POUR – N"
			if cm := skCountRe.FindStringSubmatch(cellText); len(cm) == 2 {
				n, _ := strconv.Atoi(cm[1])
				if voteType == "Yea" {
					yeas = n
				} else {
					nays = n
				}
			}

			// Extract member names from <p> elements.
			cell.Find("p").Each(func(_ int, p *goquery.Selection) {
				// Prefer text from the first span with lang=EN-GB; fall back to the paragraph text.
				name := ""
				p.Find("span").Each(func(_ int, s *goquery.Selection) {
					if name != "" {
						return
					}
					lang, _ := s.Attr("lang")
					if !strings.EqualFold(lang, "en-gb") {
						return
					}
					// Only the outermost EN-GB span carries the name.
					if s.ParentsFiltered("span[lang]").Length() > 0 {
						return
					}
					name = strings.TrimSpace(s.Text())
				})
				if name == "" {
					name = strings.TrimSpace(p.Text())
				}
				// Normalise whitespace and drop non-breaking spaces.
				name = strings.Join(strings.Fields(strings.ReplaceAll(name, "\u00a0", " ")), " ")
				// Skip the header row and blank entries.
				upper := strings.ToUpper(name)
				if name == "" || strings.Contains(upper, "YEAS") || strings.Contains(upper, "NAYS") ||
					strings.Contains(upper, "POUR") || strings.Contains(upper, "CONTRE") {
					return
				}
				votes = append(votes, ProvincialMemberVote{
					DivisionID: divID,
					MemberName: name,
					Vote:       voteType,
				})
			})
		})

		// Description: English text from the nearest preceding paragraph that
		// mentions a bill number or motion.
		desc := ""
		t.PrevAll().Filter("p").Each(func(_ int, p *goquery.Selection) {
			if desc != "" {
				return
			}
			text := strings.TrimSpace(p.Text())
			text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\u00a0", " ")), " ")
			if text != "" && !strings.Contains(strings.ToLower(text), "recorded division") {
				desc = text
			}
		})

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  legislature,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: desc,
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     "saskatchewan",
				LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
	})

	log.Printf("[sk-votes] %s: parsed %d divisions", date, len(results))
	return results
}

type quebecVoteListing struct {
	DateVote string `json:"DateVote"`
	Titre    string `json:"Titre"`
	Numero   string `json:"Numero"`
	VoteURL  string `json:"VoteURL"`
}

type quebecVotesSearchData struct {
	NumeroPage         int                 `json:"NumeroPage"`
	QuantiteParPage    int                 `json:"QuantiteParPage"`
	NombreTotalDonnees int                 `json:"NombreTotalDonnees"`
	NomRequete         string              `json:"NomRequete"`
	Donnees            []quebecVoteListing `json:"Donnees"`
}

type quebecVotesEnvelope struct {
	D quebecVotesSearchData `json:"d"`
}

func quebecSessionLegislatureValue(doc *goquery.Document, legislature, session int) string {
	if doc == nil {
		return ""
	}
	legRe := regexp.MustCompile(fmt.Sprintf(`(?i)\b%d(?:st|nd|rd|th)?\s+legislature\b`, legislature))
	sessionRe := regexp.MustCompile(fmt.Sprintf(`(?i)\b%d(?:st|nd|rd|th)?\s+session\b`, session))

	fallback := ""
	doc.Find("select.sessionLegislature option").Each(func(_ int, opt *goquery.Selection) {
		if fallback != "" {
			return
		}
		value, _ := opt.Attr("value")
		value = strings.TrimSpace(value)
		if value != "" && value != "-1" {
			fallback = value
		}
	})

	resolved := ""
	doc.Find("select.sessionLegislature option").Each(func(_ int, opt *goquery.Selection) {
		if resolved != "" {
			return
		}
		value, _ := opt.Attr("value")
		value = strings.TrimSpace(value)
		if value == "" || value == "-1" {
			return
		}
		title, _ := opt.Attr("title")
		text := strings.TrimSpace(title + " " + opt.Text())
		if legRe.MatchString(text) && sessionRe.MatchString(text) {
			resolved = value
		}
	})

	if resolved != "" {
		return resolved
	}
	return fallback
}

func quebecVotesEndpoint(indexURL, endpointPath string) string {
	base := "https://www.assnat.qc.ca"
	if u, err := neturl.Parse(indexURL); err == nil && u.Scheme != "" && u.Host != "" {
		base = u.Scheme + "://" + u.Host
	}
	return base + endpointPath
}

func quebecSearchVotes(indexURL, sessionLegislature string, page, perPage int, refresh bool, client *http.Client) (quebecVotesSearchData, error) {
	payload := map[string]string{
		"motsCles":                 "",
		"sessionLegislature":       sessionLegislature,
		"colonneTri":               "thDefaut",
		"directionTri":             "1",
		"numPage":                  strconv.Itoa(page),
		"quantiteParPage":          strconv.Itoa(perPage),
		"codeLangue":               "en",
		"rafraichirEtatPagination": strconv.FormatBool(refresh),
	}
	var envelope quebecVotesEnvelope
	if err := quebecPostJSON(client, indexURL, "/Gabarits/RegistreDesVotes.aspx/Rechercher", payload, &envelope); err != nil {
		return quebecVotesSearchData{}, fmt.Errorf("qc votes search: %w", err)
	}
	if envelope.D.QuantiteParPage <= 0 {
		envelope.D.QuantiteParPage = perPage
	}
	return envelope.D, nil
}

func quebecPaginateVotes(indexURL, queryName string, page, perPage int, client *http.Client) (quebecVotesSearchData, error) {
	payload := map[string]string{
		"nomRequete":      queryName,
		"numPage":         strconv.Itoa(page),
		"quantiteParPage": strconv.Itoa(perPage),
		"codeLangue":      "en",
	}
	var envelope quebecVotesEnvelope
	if err := quebecPostJSON(client, indexURL, "/Gabarits/RegistreDesVotes.aspx/PaginerRecherche", payload, &envelope); err != nil {
		return quebecVotesSearchData{}, fmt.Errorf("qc votes paginate page=%d: %w", page, err)
	}
	if envelope.D.QuantiteParPage <= 0 {
		envelope.D.QuantiteParPage = perPage
	}
	return envelope.D, nil
}

func quebecPostJSON(client *http.Client, indexURL, endpointPath string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := quebecVotesEndpoint(indexURL, endpointPath)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", indexURL)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST %q: status %d - %s", url, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func parseQuebecVoteDetailDoc(doc *goquery.Document, divisionID string) ([]ProvincialMemberVote, int, int) {
	yeas, _ := strconv.Atoi(strings.TrimSpace(doc.Find("#nbPour").AttrOr("value", "0")))
	nays, _ := strconv.Atoi(strings.TrimSpace(doc.Find("#nbContre").AttrOr("value", "0")))

	votes := make([]ProvincialMemberVote, 0, yeas+nays)
	seen := make(map[string]bool)
	appendPanel := func(selector, vote string) {
		doc.Find(selector).Each(func(_ int, member *goquery.Selection) {
			name := strings.TrimSpace(member.Find("span.nom").First().Text())
			if name == "" {
				name = strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(member.Text(), "\u00a0", " ")), " "))
			}
			if name == "" {
				return
			}
			key := vote + "|" + strings.ToLower(name)
			if seen[key] {
				return
			}
			seen[key] = true
			votes = append(votes, ProvincialMemberVote{DivisionID: divisionID, MemberName: name, Vote: vote})
		})
	}

	appendPanel("#ctl00_ColCentre_ContenuColonneGauche_pnlPour .depute", "Yea")
	appendPanel("#ctl00_ColCentre_ContenuColonneGauche_pnlContre .depute", "Nay")
	return votes, yeas, nays
}

var newBrunswickJournalSessionLinkRe = regexp.MustCompile(`(?i)/en/house-business/journals/\d+/\d+/?$`)
var newBrunswickJournalPDFLinkRe = regexp.MustCompile(`(?i)\.pdf(?:\?.*)?$`)
var newBrunswickPDFVoteCountRe = regexp.MustCompile(`(?is)(?:YEAS?|POUR)\s*[:\-]?\s*(\d{1,3}).{0,280}?(?:NAYS?|CONTRE)\s*[:\-]?\s*(\d{1,3})`)
var newBrunswickVoteSectionRe = regexp.MustCompile(`(?is)(?:RECORDED\s+DIVISION\s+)?(YEAS?|POUR)\s*[-:–]\s*\d{1,3}\s+`)
var newBrunswickVoteCountPairRe = regexp.MustCompile(`(?is)(YEAS?|POUR)\s*[-:–]\s*(\d{1,3}).*?(NAYS?|CONTRE)\s*[-:–]\s*(\d{1,3})`)
var newBrunswickNameTokenRe = regexp.MustCompile(`(?i)(?:Hon\.\s+)?(?:Mr\.|Ms\.)\s+[A-Z][A-Za-z\.'\-]+(?:\s*\-\s*[A-Z][A-Za-z\.'\-]+)*`)

func crawlNewBrunswickVotesFromPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("nb votes index: %w", err)
	}

	sessionLinks := discoverNewBrunswickJournalSessionLinks(indexDoc, indexURL)
	if len(sessionLinks) == 0 {
		sessionLinks = []string{indexURL}
	}

	pdfLinks := make([]string, 0)
	seenPDF := make(map[string]bool)
	for _, sessionURL := range sessionLinks {
		doc, derr := fetchDoc(sessionURL, client)
		if derr != nil {
			log.Printf("[nb-votes] skip session %s: %v", sessionURL, derr)
			continue
		}
		for _, pdfURL := range discoverNewBrunswickJournalPDFLinks(doc, sessionURL) {
			if seenPDF[pdfURL] {
				continue
			}
			seenPDF[pdfURL] = true
			pdfLinks = append(pdfLinks, pdfURL)
		}
	}

	sort.Strings(pdfLinks)
	if len(pdfLinks) > 60 {
		pdfLinks = pdfLinks[len(pdfLinks)-60:]
	}
	if len(pdfLinks) == 0 {
		log.Printf("[nb-votes] no journal PDFs discovered; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "nb", "new_brunswick", legislature, session, client, newBrunswickVotesLinkRe)
	}

	results := make([]ProvincialDivisionResult, 0)
	nextDivNum := 1
	for _, pdfURL := range pdfLinks {
		divs, consumed, derr := crawlNewBrunswickJournalPDF(pdfURL, legislature, session, nextDivNum, client)
		if derr != nil {
			log.Printf("[nb-votes] skip pdf %s: %v", pdfURL, derr)
			continue
		}
		results = append(results, divs...)
		nextDivNum += consumed
	}
	if len(results) == 0 {
		log.Printf("[nb-votes] no divisions parsed from PDFs; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "nb", "new_brunswick", legislature, session, client, newBrunswickVotesLinkRe)
	}

	log.Printf("[nb-votes] parsed %d divisions from %d PDFs", len(results), len(pdfLinks))
	return results, nil
}

func discoverNewBrunswickJournalSessionLinks(doc *goquery.Document, baseURL string) []string {
	seen := make(map[string]bool)
	links := make([]string, 0)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !newBrunswickJournalSessionLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(baseURL, href)
		if seen[full] {
			return
		}
		seen[full] = true
		links = append(links, full)
	})
	sort.Strings(links)
	if len(links) > 6 {
		links = links[len(links)-6:]
	}
	return links
}

func discoverNewBrunswickJournalPDFLinks(doc *goquery.Document, baseURL string) []string {
	seen := make(map[string]bool)
	links := make([]string, 0)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !newBrunswickJournalPDFLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(baseURL, href)
		if seen[full] {
			return
		}
		seen[full] = true
		links = append(links, full)
	})
	sort.Strings(links)
	return links
}

func normalizeHref(href string) string {
	href = strings.TrimSpace(href)
	href = strings.ReplaceAll(href, `\`, "/")
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

func downloadAndExtractPDFText(pdfURL, province string, client *http.Client) (string, error) {
	resp, err := client.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("GET %q: %w", pdfURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GET %q: status %d - %s", pdfURL, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	tmp, err := os.CreateTemp("", "open-democracy-"+province+"-*.pdf")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if written >= 32<<20 {
		return "", fmt.Errorf("pdf too large (>32MB): %s", pdfURL)
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	return extractProvincialPDFText(tmpPath)
}

func crawlNewBrunswickJournalPDF(pdfURL string, legislature, session, startDivisionNumber int, client *http.Client) ([]ProvincialDivisionResult, int, error) {
	text, err := downloadAndExtractPDFText(pdfURL, "nb", client)
	if err != nil {
		return nil, 0, err
	}

	date := extractDateFromURL(pdfURL)
	if date == "" {
		date = utils.TodayISO()
	}
	parsed := parseNewBrunswickPDFDivisions(text, pdfURL, legislature, session, startDivisionNumber, date)
	return parsed, len(parsed), nil
}

func extractProvincialPDFText(pdfPath string) (string, error) {
	dir, err := os.MkdirTemp("", "open-democracy-nb-content-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	if err := api.ExtractContentFile(pdfPath, dir, nil, nil); err != nil {
		return "", err
	}

	contentFiles, err := filepath.Glob(filepath.Join(dir, "*_Content_page_*.txt"))
	if err != nil {
		return "", err
	}
	sort.Strings(contentFiles)

	var text strings.Builder
	for _, contentPath := range contentFiles {
		fp, err := os.Open(contentPath)
		if err != nil {
			return "", err
		}

		scanner := bufio.NewScanner(fp)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasSuffix(line, "TJ") || strings.HasSuffix(line, "Tj") {
				for _, match := range pdfParenTextRe.FindAllStringSubmatch(line, -1) {
					if len(match) < 2 {
						continue
					}
					text.WriteString(decodePDFStringToken(match[1]))
				}
				text.WriteByte(' ')
			}
		}
		_ = fp.Close()
		if err := scanner.Err(); err != nil {
			return "", err
		}
		text.WriteByte('\f')
	}

	normalized := strings.Join(strings.Fields(strings.ReplaceAll(text.String(), "\u00a0", " ")), " ")
	return normalized, nil
}

var pdfParenTextRe = regexp.MustCompile(`\(([^()]*)\)`)

func decodePDFStringToken(token string) string {
	token = strings.ReplaceAll(token, `\\(`, "(")
	token = strings.ReplaceAll(token, `\\)`, ")")
	token = strings.ReplaceAll(token, `\\n`, " ")
	token = strings.ReplaceAll(token, `\\r`, " ")
	token = strings.ReplaceAll(token, `\\t`, " ")
	token = strings.ReplaceAll(token, `\\`, "")
	return token
}

func parseNewBrunswickPDFDivisions(text, detailURL string, legislature, session, startDivisionNumber int, date string) []ProvincialDivisionResult {
	sections := splitNewBrunswickVoteSections(text)
	blocks := make([][4]string, 0, len(sections))
	for _, section := range sections {
		m := newBrunswickVoteCountPairRe.FindStringSubmatch(section)
		if len(m) != 5 {
			continue
		}
		yeasBlock := section
		naysBlock := ""
		if split := regexp.MustCompile(`(?is)(NAYS?|CONTRE)\s*[-:–]\s*\d{1,3}\s+`).FindStringIndex(section); split != nil {
			yeasBlock = section[:split[0]]
			naysBlock = section[split[0]:]
		}
		blocks = append(blocks, [4]string{m[2], m[4], yeasBlock, naysBlock})
	}
	if len(blocks) == 0 {
		// Fallback to count-only extraction if block extraction misses a layout variant.
		matches := newBrunswickPDFVoteCountRe.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			return nil
		}
		results := make([]ProvincialDivisionResult, 0, len(matches))
		for i, m := range matches {
			yeas, _ := strconv.Atoi(text[m[2]:m[3]])
			nays, _ := strconv.Atoi(text[m[4]:m[5]])
			if yeas == 0 && nays == 0 {
				continue
			}
			divNum := startDivisionNumber + i
			divID := ProvincialDivisionID("nb", legislature, session, divNum, date)
			desc := newBrunswickDescriptionFromContext(text, m[0])
			if desc == "" {
				desc = "Recorded division"
			}
			result := "Carried"
			if nays > yeas {
				result = "Negatived"
			}
			results = append(results, ProvincialDivisionResult{
				Division: DivisionStub{
					ID:          divID,
					Parliament:  legislature,
					Session:     session,
					Number:      divNum,
					Date:        date,
					Description: desc,
					Yeas:        yeas,
					Nays:        nays,
					Result:      result,
					Chamber:     "new_brunswick",
					DetailURL:   detailURL,
					LastScraped: utils.NowISO(),
				},
				Votes: nil,
			})
		}
		return results
	}

	results := make([]ProvincialDivisionResult, 0, len(blocks))
	for i, block := range blocks {
		yeas, _ := strconv.Atoi(strings.TrimSpace(block[0]))
		nays, _ := strconv.Atoi(strings.TrimSpace(block[1]))
		if yeas == 0 && nays == 0 {
			continue
		}

		divNum := startDivisionNumber + i
		divID := ProvincialDivisionID("nb", legislature, session, divNum, date)
		desc := newBrunswickDescriptionFromContext(text, strings.Index(text, block[2]))
		if desc == "" {
			desc = "Recorded division"
		}

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		votes := make([]ProvincialMemberVote, 0, yeas+nays)
		for _, name := range parseNewBrunswickVoteNames(block[2]) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Yea"})
		}
		for _, name := range parseNewBrunswickVoteNames(block[3]) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Nay"})
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  legislature,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: desc,
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     "new_brunswick",
				DetailURL:   detailURL,
				LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
	}

	return results
}

func splitNewBrunswickVoteSections(text string) []string {
	idxs := newBrunswickVoteSectionRe.FindAllStringIndex(text, -1)
	if len(idxs) == 0 {
		return nil
	}
	sections := make([]string, 0, len(idxs))
	for i, span := range idxs {
		start := span[0]
		end := len(text)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		section := strings.TrimSpace(text[start:end])
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections
}

func parseNewBrunswickVoteNames(blockText string) []string {
	if strings.TrimSpace(blockText) == "" {
		return nil
	}
	clean := strings.Join(strings.Fields(strings.ReplaceAll(blockText, "\u00a0", " ")), " ")
	nameMatches := newBrunswickNameTokenRe.FindAllString(clean, -1)
	if len(nameMatches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	results := make([]string, 0, len(nameMatches))
	for _, raw := range nameMatches {
		name := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
		if name == "" {
			continue
		}
		name = strings.TrimSpace(strings.TrimPrefix(name, "Hon. "))
		name = strings.TrimSpace(strings.TrimPrefix(name, "Mr. "))
		name = strings.TrimSpace(strings.TrimPrefix(name, "Ms. "))
		name = strings.TrimSpace(strings.TrimPrefix(name, "Dr. "))
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, name)
	}
	return results
}

// ParseNewBrunswickPDFDivisionsForTest is test-only access to NB PDF parsing logic.
func ParseNewBrunswickPDFDivisionsForTest(text, detailURL string, legislature, session, startDivisionNumber int, date string) []ProvincialDivisionResult {
	return parseNewBrunswickPDFDivisions(text, detailURL, legislature, session, startDivisionNumber, date)
}

func newBrunswickDescriptionFromContext(text string, matchStart int) string {
	start := matchStart - 260
	if start < 0 {
		start = 0
	}
	snippet := strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(text[start:matchStart], "\u00a0", " ")), " "))
	if snippet == "" {
		return ""
	}
	parts := strings.Split(snippet, ".")
	desc := strings.TrimSpace(parts[len(parts)-1])
	if len(desc) > 220 {
		desc = desc[len(desc)-220:]
	}
	return strings.TrimSpace(desc)
}

// ── Generic provincial votes parser (remaining provinces) ──────────────────

var genericVotesLinkRe = regexp.MustCompile(`(?i)(votes|proceedings|journal|journals|registre-votes|recorded_votes|minutes)`)
var genericYeaRe = regexp.MustCompile(`(?i)(?:yeas?|ayes?|pour)\D*(\d+)`)
var genericNayRe = regexp.MustCompile(`(?i)(?:nays?|contre)\D*(\d+)`)
var albertaVotesLinkRe = regexp.MustCompile(`(?i)(assembly-records/votes-and-proceedings|votes-and-proceedings|/votes(?:/|$))`)
var bcVotesLinkRe = regexp.MustCompile(`(?i)(votes-and-proceedings|journals?|/votes(?:/|$))`)
var quebecVotesLinkRe = regexp.MustCompile(`(?i)(registre-des-votes|registre-votes|votes-nominaux|votes\.html|votes-appels-nominaux|/votes(?:/|$))`)
var manitobaVotesLinkRe = regexp.MustCompile(
	`(?i)(recorded_votes|votes|journals?|hansard|\d+(?:rd|th|st|nd)/\d+(?:rd|th|st|nd)_\d+\.html|/\d+(?:rd|th|st|nd)/votes_\d+\.pdf)`)
var newBrunswickVotesLinkRe = regexp.MustCompile(`(?i)(journals?(?:-e\.asp|/)|house-business/journals|votes|legis)`)
var newfoundlandVotesLinkRe = regexp.MustCompile(`(?i)(/business/votes|housebusiness|ga\d+session\d+|votes\.aspx|/votes(?:/|$))`)
var novaScotiaVotesLinkRe = regexp.MustCompile(`(?i)(journals?|proceedings|votes|hansard-debates)`)
var peiVotesLinkRe = regexp.MustCompile(`(?i)(legislative-business|votes|proceedings)`)

// ── Shared YEAS/AYES/NAYS parsing helpers ─────────────────────────────────────

// genericYeasNaysVoteSectionRe detects the start of a YEAS/AYES vote block in PDF text.
var genericYeasNaysVoteSectionRe = regexp.MustCompile(`(?is)(?:RECORDED\s+DIVISION\s+)?(YEAS?|AYES?|POUR)\s*[-:–—]\s*\d{1,3}\s+`)

// genericYeasNaysVoteCountPairRe extracts both counts from a vote section.
var genericYeasNaysVoteCountPairRe = regexp.MustCompile(`(?is)(YEAS?|AYES?|POUR)\s*[-:–—]\s*(\d{1,3}).*?(NAYS?|CONTRE)\s*[-:–—]\s*(\d{1,3})`)

// genericNaysHalfRe marks the start of the NAYS/CONTRE block within a section.
var genericNaysHalfRe = regexp.MustCompile(`(?is)(NAYS?|CONTRE)\s*[-:–—]\s*\d{1,3}\s+`)

// genericPDFVoteCountRe is a fallback for count-only extraction.
var genericPDFVoteCountRe = regexp.MustCompile(`(?is)(?:YEAS?|AYES?|POUR)\s*[-:–—]?\s*(\d{1,3}).{0,280}?(?:NAYS?|CONTRE)\s*[-:–—]?\s*(\d{1,3})`)

// splitVoteSectionsGeneric splits normalised PDF text into per-division blocks
// using the given section start pattern.
func splitVoteSectionsGeneric(text string, sectionRe *regexp.Regexp) []string {
	idxs := sectionRe.FindAllStringIndex(text, -1)
	if len(idxs) == 0 {
		return nil
	}
	sections := make([]string, 0, len(idxs))
	for i, span := range idxs {
		start := span[0]
		end := len(text)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		section := strings.TrimSpace(text[start:end])
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections
}

// voteKeywords is the set of uppercase tokens in Canadian legislature PDF vote
// sections that are NOT member surnames. Used by extractPlainVoteNames.
var voteKeywords = map[string]bool{
	"FOR": true, "THE": true, "AMENDMENT": true, "MOTION": true, "BILL": true,
	"AGAINST": true, "QUESTION": true, "READING": true, "THIRD": true,
	"SECOND": true, "FIRST": true, "DIVISION": true, "VOTES": true,
	"PROCEEDINGS": true, "LEGISLATURE": true, "SESSION": true, "AN": true,
	"ACT": true, "TO": true, "AND": true, "OF": true, "IN": true, "ON": true,
	"OR": true, "THAT": true, "SCHEDULE": true, "CARRIED": true,
	"NEGATIVED": true, "MR": true, "MS": true, "HON": true, "DR": true,
	"YEAS": true, "YEA": true, "NAYS": true, "NAY": true, "POUR": true,
	"CONTRE": true, "AYES": true, "AYE": true, "FAVOUR": true, "OPPOSED": true,
	"MAJORITY": true, "RESOLUTION": true, "ORDER": true, "NO": true,
	"YES": true, "A": true, "AS": true, "AT": true, "BE": true, "BY": true,
	"PARLIAMENT": true, "LEGISLATIVE": true, "ASSEMBLY": true, "HOUSE": true,
	"JOURNALS": true, "JOURNAL": true, "SITTING": true,
	"RECORDED": true, "CHAIR": true, "SPEAKER": true, "DEPUTY": true,
	"MINUTES": true, "REPORT": true, "PAGE": true,
}

// extractPlainVoteNames extracts member surnames from a vote-block text where
// names appear as plain capitalized tokens without "Mr./Ms." prefixes (AB, MB, NS).
func extractPlainVoteNames(blockText string) []string {
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, tok := range strings.Fields(strings.ReplaceAll(blockText, "\u00a0", " ")) {
		tok = strings.TrimRight(tok, ".,;:)'\"")
		tok = strings.TrimLeft(tok, "('\"")
		if len(tok) < 2 {
			continue
		}
		// Must start with an ASCII uppercase letter.
		if tok[0] < 'A' || tok[0] > 'Z' {
			continue
		}
		// Skip known vote-section keywords.
		if voteKeywords[strings.ToUpper(tok)] {
			continue
		}
		// Skip purely numeric tokens.
		allDigit := true
		for _, c := range tok {
			if c < '0' || c > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			continue
		}
		// Skip all-uppercase tokens > 3 chars (headings / acronyms).
		if len(tok) > 3 && strings.ToUpper(tok) == tok {
			continue
		}
		key := strings.ToLower(tok)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, tok)
	}
	return names
}

// parsePDFDivisionsYeasNays parses recorded vote divisions from normalised PDF text
// using YEAS/AYES/NAYS/CONTRE section markers. Used by MB, NS, and NL journal parsers.
// extractNames is called on each yea/nay block to produce member names; pass nil to
// get outcome-only results (yea/nay counts with no member rows).
func parsePDFDivisionsYeasNays(
	text, detailURL, province, chamber string,
	legislature, session, startDivisionNumber int,
	date string,
	extractNames func(string) []string,
) []ProvincialDivisionResult {
	sections := splitVoteSectionsGeneric(text, genericYeasNaysVoteSectionRe)
	blocks := make([][4]string, 0, len(sections))
	for _, section := range sections {
		m := genericYeasNaysVoteCountPairRe.FindStringSubmatch(section)
		if len(m) != 5 {
			continue
		}
		yeasBlock := section
		naysBlock := ""
		if split := genericNaysHalfRe.FindStringIndex(section); split != nil {
			yeasBlock = section[:split[0]]
			naysBlock = section[split[0]:]
		}
		blocks = append(blocks, [4]string{m[2], m[4], yeasBlock, naysBlock})
	}
	if len(blocks) == 0 {
		// Fallback: count-only (no member name lists).
		matches := genericPDFVoteCountRe.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			return nil
		}
		results := make([]ProvincialDivisionResult, 0, len(matches))
		for i, m := range matches {
			yeas, _ := strconv.Atoi(text[m[2]:m[3]])
			nays, _ := strconv.Atoi(text[m[4]:m[5]])
			if yeas == 0 && nays == 0 {
				continue
			}
			divNum := startDivisionNumber + i
			divID := ProvincialDivisionID(province, legislature, session, divNum, date)
			desc := newBrunswickDescriptionFromContext(text, m[0])
			if desc == "" {
				desc = "Recorded division"
			}
			result := "Carried"
			if nays > yeas {
				result = "Negatived"
			}
			results = append(results, ProvincialDivisionResult{
				Division: DivisionStub{
					ID: divID, Parliament: legislature, Session: session,
					Number: divNum, Date: date, Description: desc,
					Yeas: yeas, Nays: nays, Result: result,
					Chamber: chamber, DetailURL: detailURL, LastScraped: utils.NowISO(),
				},
			})
		}
		return results
	}

	results := make([]ProvincialDivisionResult, 0, len(blocks))
	for i, block := range blocks {
		yeas, _ := strconv.Atoi(strings.TrimSpace(block[0]))
		nays, _ := strconv.Atoi(strings.TrimSpace(block[1]))
		if yeas == 0 && nays == 0 {
			continue
		}
		divNum := startDivisionNumber + i
		divID := ProvincialDivisionID(province, legislature, session, divNum, date)
		desc := newBrunswickDescriptionFromContext(text, strings.Index(text, block[2]))
		if desc == "" {
			desc = "Recorded division"
		}
		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}
		votes := make([]ProvincialMemberVote, 0, yeas+nays)
		if extractNames != nil {
			for _, name := range extractNames(block[2]) {
				votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Yea"})
			}
			for _, name := range extractNames(block[3]) {
				votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Nay"})
			}
		}
		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID: divID, Parliament: legislature, Session: session,
				Number: divNum, Date: date, Description: desc,
				Yeas: yeas, Nays: nays, Result: result,
				Chamber: chamber, DetailURL: detailURL, LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
	}
	return results
}

// ParsePDFDivisionsYeasNaysForTest is test-only access to the generic YEAS/NAYS parser.
func ParsePDFDivisionsYeasNaysForTest(text, detailURL, province, chamber string, legislature, session, startDivNum int, date string) []ProvincialDivisionResult {
	return parsePDFDivisionsYeasNays(text, detailURL, province, chamber, legislature, session, startDivNum, date, extractPlainVoteNames)
}

// ── 5A.2 Alberta ─────────────────────────────────────────────────────────────

// albertaVotesPDFLinkRe matches VP PDF hrefs on the AB votes-and-proceedings page.
// AB index pages embed backslash-escaped paths; normalizeHref converts them first.
var albertaVotesPDFLinkRe = regexp.MustCompile(`(?i)docs\.assembly\.ab\.ca[^"'\s]*_vp\.pdf`)

// abForCountRe / abAgainstCountRe extract vote totals from AB V&P PDF text.
var abForCountRe = regexp.MustCompile(`(?i)For\s+the\s+[^:]{1,60}:\s*(\d+)`)
var abAgainstCountRe = regexp.MustCompile(`(?i)Against\s+the\s+[^:]{1,60}:\s*(\d+)`)
var abDivisionSplitRe = regexp.MustCompile(`(?i)DIVISION\s+\d+`)

// parseAlbertaVPDivisions parses recorded vote divisions from normalised AB V&P PDF text.
// Alberta uses "For the [phrase]: N" / "Against the [phrase]: N" format with plain surname lists.
func parseAlbertaVPDivisions(text, detailURL string, legislature, session, startDivisionNumber int, date string) []ProvincialDivisionResult {
	divBlocks := abDivisionSplitRe.FindAllStringIndex(text, -1)
	if len(divBlocks) == 0 {
		return nil
	}
	results := make([]ProvincialDivisionResult, 0, len(divBlocks))
	for i, span := range divBlocks {
		end := len(text)
		if i+1 < len(divBlocks) {
			end = divBlocks[i+1][0]
		}
		block := text[span[0]:end]

		forM := abForCountRe.FindStringSubmatchIndex(block)
		agaM := abAgainstCountRe.FindStringSubmatchIndex(block)
		if forM == nil || agaM == nil {
			continue
		}
		yeas, _ := strconv.Atoi(block[forM[2]:forM[3]])
		nays, _ := strconv.Atoi(block[agaM[2]:agaM[3]])
		if yeas == 0 && nays == 0 {
			continue
		}

		// Names appear between end-of-"For..." and start-of-"Against...".
		yeaBlock := ""
		nayBlock := ""
		forEnd := forM[1]
		agaStart := agaM[0]
		agaEnd := agaM[1]
		if forEnd <= agaStart {
			yeaBlock = block[forEnd:agaStart]
		}
		if agaEnd < len(block) {
			nayBlock = block[agaEnd:]
		}

		divNum := startDivisionNumber + i
		divID := ProvincialDivisionID("ab", legislature, session, divNum, date)

		// Description: text between division heading and "For the..."
		desc := strings.TrimSpace(block[len(abDivisionSplitRe.FindString(block)):forM[0]])
		desc = strings.Join(strings.Fields(strings.ReplaceAll(desc, "\u00a0", " ")), " ")
		if len(desc) > 220 {
			desc = desc[len(desc)-220:]
		}
		if desc == "" {
			desc = "Recorded division"
		}

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		votes := make([]ProvincialMemberVote, 0, yeas+nays)
		for _, name := range extractPlainVoteNames(yeaBlock) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Yea"})
		}
		for _, name := range extractPlainVoteNames(nayBlock) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Nay"})
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID: divID, Parliament: legislature, Session: session,
				Number: divNum, Date: date, Description: desc,
				Yeas: yeas, Nays: nays, Result: result,
				Chamber: "alberta", DetailURL: detailURL, LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
	}
	log.Printf("[ab-votes] parsed %d divisions", len(results))
	return results
}

// ParseAlbertaVPDivisionsForTest is test-only access to AB V&P parsing logic.
func ParseAlbertaVPDivisionsForTest(text, detailURL string, legislature, session, startDivNum int, date string) []ProvincialDivisionResult {
	return parseAlbertaVPDivisions(text, detailURL, legislature, session, startDivNum, date)
}

// crawlAlbertaVotesFromPDF fetches the AB V&P index page, discovers per-day PDF links
// (fixing the backslash-escaped hrefs), and parses each PDF.
func crawlAlbertaVotesFromPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[ab-votes] fetching index: %s", indexURL)
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("ab votes index: %w", err)
	}

	var pdfLinks []string
	seen := make(map[string]bool)
	indexDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !albertaVotesPDFLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if seen[full] {
			return
		}
		seen[full] = true
		pdfLinks = append(pdfLinks, full)
	})

	sort.Strings(pdfLinks)
	if len(pdfLinks) > 60 {
		pdfLinks = pdfLinks[len(pdfLinks)-60:]
	}
	if len(pdfLinks) == 0 {
		log.Printf("[ab-votes] no VP PDFs discovered")
		return nil, nil
	}

	var results []ProvincialDivisionResult
	nextDivNum := 1
	for _, pdfURL := range pdfLinks {
		text, terr := downloadAndExtractPDFText(pdfURL, "ab", client)
		if terr != nil {
			log.Printf("[ab-votes] skip pdf %s: %v", pdfURL, terr)
			continue
		}
		date := extractDateFromURL(pdfURL)
		if date == "" {
			date = utils.FindDateInText(text)
		}
		if date == "" {
			date = utils.TodayISO()
		}
		divs := parseAlbertaVPDivisions(text, pdfURL, legislature, session, nextDivNum, date)
		results = append(results, divs...)
		nextDivNum += len(divs)
		if len(divs) == 0 {
			nextDivNum++
		}
	}
	log.Printf("[ab-votes] parsed %d divisions from %d PDFs", len(results), len(pdfLinks))
	return results, nil
}

// CrawlAlbertaVotes crawls Alberta votes/proceedings pages.
// The AB assembly page links to per-day VP PDFs via backslash-escaped hrefs; this
// function normalises those hrefs and parses each PDF using the Alberta-specific
// "For the [phrase]: N / Against the [phrase]: N" vote format.
func CrawlAlbertaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.ab.ca/assembly-business/assembly-records/votes-and-proceedings"
	}
	return crawlAlbertaVotesFromPDF(indexURL, legislature, session, client)
}

// ── 5A.3 British Columbia ─────────────────────────────────────────────────────

// bcLIMSBase is the base URL for the BC LIMS document-store REST API.
const bcLIMSBase = "https://lims.leg.bc.ca"

// bcLIMSVotesFile describes a single V&P HTML document returned by the BC LIMS API.
type bcLIMSVotesFile struct {
	FileName                string `json:"fileName"`
	FilePath                string `json:"filePath"`
	Published               bool   `json:"published"`
	Date                    string `json:"date"`
	VotesAttributesByFileId struct {
		Nodes []struct {
			VoteNumbers string `json:"voteNumbers"`
		} `json:"nodes"`
	} `json:"votesAttributesByFileId"`
}

// bcLIMSVotesResponse is the JSON envelope from
// https://lims.leg.bc.ca/pdms/votes-and-proceedings/{parliament}{session}.
type bcLIMSVotesResponse struct {
	AllParliamentaryFileAttributes struct {
		Nodes []bcLIMSVotesFile `json:"nodes"`
	} `json:"allParliamentaryFileAttributes"`
}

// parliamentOrdinal converts an integer to its English ordinal string (1→"1st",
// 2→"2nd", 3→"3rd", 4→"4th", …).  Used to build the BC LIMS API path.
func parliamentOrdinal(n int) string {
	var suffix string
	switch {
	case n%100 >= 11 && n%100 <= 13:
		suffix = "th"
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	default:
		suffix = "th"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// parseBCDivisionTable parses a single BC VP <table class="division"> element
// and returns yea count, nay count, yea names, and nay names.
//
// The table layout is:
//
//	<tr><td class="head" colspan="4">Yeas — 48</td></tr>
//	<tr><td>Name <br> Name <br></td> … (4 columns) </tr>
//	<tr><td class="head" colspan="4">Nays — 40</td></tr>
//	<tr><td>Name <br> Name <br></td> … (4 columns) </tr>
func parseBCDivisionTable(table *goquery.Selection) (yeas, nays int, yeaNames, nayNames []string) {
	var currentSide string // "yea" or "nay"
	table.Find("tr").Each(func(_ int, row *goquery.Selection) {
		headCell := row.Find("td.head")
		if headCell.Length() > 0 {
			headText := strings.TrimSpace(headCell.Text())
			headLower := strings.ToLower(headText)
			var count int
			if m := genericYeaRe.FindStringSubmatch(headText); len(m) == 2 {
				count, _ = strconv.Atoi(m[1])
			} else if m := genericNayRe.FindStringSubmatch(headText); len(m) == 2 {
				count, _ = strconv.Atoi(m[1])
			}
			if strings.Contains(headLower, "yea") || strings.Contains(headLower, "aye") {
				currentSide = "yea"
				yeas = count
			} else if strings.Contains(headLower, "nay") {
				currentSide = "nay"
				nays = count
			}
			return
		}
		// Data row: collect member names from each <td> cell.
		row.Find("td").Each(func(_ int, cell *goquery.Selection) {
			// Names are separated by <br> elements; get raw HTML and split on <br>.
			cellHTML, _ := cell.Html()
			// Replace <br> variants with newline then strip any remaining tags.
			brRe := regexp.MustCompile(`(?i)<br\s*/?>`)
			tagRe := regexp.MustCompile(`<[^>]+>`)
			cleaned := tagRe.ReplaceAllString(brRe.ReplaceAllString(cellHTML, "\n"), "")
			for _, raw := range strings.Split(cleaned, "\n") {
				name := strings.TrimSpace(raw)
				if name == "" || name == "/" {
					continue
				}
				switch currentSide {
				case "yea":
					yeaNames = append(yeaNames, name)
				case "nay":
					nayNames = append(nayNames, name)
				}
			}
		})
	})
	return
}

// parseBCVotesDivisions parses all recorded divisions from a BC V&P HTML document.
func parseBCVotesDivisions(doc *goquery.Document, sourceURL, date, province string, legislature, session, startDivNum int) []ProvincialDivisionResult {
	var results []ProvincialDivisionResult
	divNum := startDivNum

	// Collect all <p> elements and <table class="division"> in document order.
	doc.Find("p, table.division").Each(func(_ int, sel *goquery.Selection) {
		if goquery.NodeName(sel) != "table" {
			return
		}
		// Found a division table.  Grab the immediately-preceding <p> for the description.
		desc := strings.TrimSpace(sel.Prev().Text())
		desc = strings.Join(strings.Fields(desc), " ")

		yeas, nays, yeaNames, nayNames := parseBCDivisionTable(sel)
		if yeas == 0 && nays == 0 {
			return // no recorded counts → skip (voice vote or committee)
		}

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		divID := ProvincialDivisionID("bc", legislature, session, divNum, date)
		mv := make([]ProvincialMemberVote, 0, len(yeaNames)+len(nayNames))
		for _, name := range yeaNames {
			mv = append(mv, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "yea"})
		}
		for _, name := range nayNames {
			mv = append(mv, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "nay"})
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  legislature,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: desc,
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     "british_columbia",
				DetailURL:   sourceURL,
				LastScraped: utils.NowISO(),
			},
			Votes: mv,
		})
		divNum++
	})
	return results
}

// crawlBritishColumbiaVotesFromLIMS fetches the BC LIMS document index and then
// parses each per-day VP HTML file for recorded divisions.
//
// limsBase is the base URL of the LIMS document store (normally bcLIMSBase).
// API: GET {limsBase}/pdms/votes-and-proceedings/{parl}{sess}
// Returns JSON listing all VP HTML files for the parliament/session pair.
// Each file is at:  {limsBase}/pdms/ldp/{parl}{sess}/votes/{fileName}
func crawlBritishColumbiaVotesFromLIMS(limsBase, parliament, session string, legislature, sessionNum int, client *http.Client) ([]ProvincialDivisionResult, error) {
	indexURL := fmt.Sprintf("%s/pdms/votes-and-proceedings/%s%s", limsBase, parliament, session)
	log.Printf("[bc-votes] fetching LIMS index: %s", indexURL)

	req, err := http.NewRequest("GET", indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bc votes LIMS index: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bc votes LIMS index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bc votes LIMS index: status %d", resp.StatusCode)
	}

	var apiResp bcLIMSVotesResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("bc votes LIMS index JSON: %w", err)
	}

	files := apiResp.AllParliamentaryFileAttributes.Nodes
	log.Printf("[bc-votes] LIMS index: %d VP files for %s%s", len(files), parliament, session)

	var results []ProvincialDivisionResult
	nextDivNum := 1

	for _, f := range files {
		if !f.Published {
			continue
		}
		fileURL := fmt.Sprintf("%s/pdms/ldp/%s%s/votes/%s", limsBase, parliament, session, f.FileName)
		date := ""
		if len(f.Date) >= 10 {
			date = f.Date[:10]
		}
		if date == "" {
			date = extractDateFromURL(fileURL)
		}
		if date == "" {
			date = utils.TodayISO()
		}

		fileResp, ferr := client.Get(fileURL)
		if ferr != nil {
			log.Printf("[bc-votes] skip %s: %v", fileURL, ferr)
			continue
		}
		fileDoc, derr := goquery.NewDocumentFromReader(fileResp.Body)
		fileResp.Body.Close()
		if derr != nil {
			log.Printf("[bc-votes] parse error %s: %v", fileURL, derr)
			continue
		}

		divs := parseBCVotesDivisions(fileDoc, fileURL, date, "british_columbia", legislature, sessionNum, nextDivNum)
		if len(divs) > 0 {
			log.Printf("[bc-votes] %s: parsed %d divisions", date, len(divs))
			results = append(results, divs...)
			nextDivNum += len(divs)
		}
	}

	log.Printf("[bc-votes] parsed %d divisions from %d files", len(results), len(files))
	return results, nil
}

// CrawlBritishColumbiaVotes crawls BC V&P data from the LIMS document-store API.
//
// Discovery (completed 2026-04):
//   - The V&P index page at leg.bc.ca embeds an <iframe> pointing to the React SPA
//     dyn.leg.bc.ca/votes-and-proceedings?parliament=43rd&session=2nd.
//   - The SPA fetches its data from https://lims.leg.bc.ca/pdms/votes-and-proceedings/{parl}{sess}
//     (a plain JSON REST endpoint, no authentication required).
//   - Each VP file is at https://lims.leg.bc.ca/pdms/ldp/{parl}{sess}/votes/{fileName}.htm
//   - Recorded divisions appear as <table class="division"> with Yeas/Nays headers.
//
// indexURL, when non-empty, overrides the LIMS base URL. This is used in tests to
// point the scraper at a local HTTP server instead of lims.leg.bc.ca.
func CrawlBritishColumbiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	limsBase := bcLIMSBase
	if indexURL != "" {
		limsBase = indexURL
	}
	parl := parliamentOrdinal(legislature)
	sess := parliamentOrdinal(session)
	return crawlBritishColumbiaVotesFromLIMS(limsBase, parl, sess, legislature, session, client)
}

// ParseBCVotesDivisionsForTest is test-only access to the BC VP HTML division parser.
// It accepts a raw HTML string and parses it for recorded divisions.
func ParseBCVotesDivisionsForTest(htmlContent, sourceURL, date string, legislature, session, startDivNum int) []ProvincialDivisionResult {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}
	return parseBCVotesDivisions(doc, sourceURL, date, "british_columbia", legislature, session, startDivNum)
}

// ParliamentOrdinalForTest is test-only access to the ordinal-suffix helper.
func ParliamentOrdinalForTest(n int) string { return parliamentOrdinal(n) }

// ── 5A.4 Manitoba ─────────────────────────────────────────────────────────────

// mbVotesPDFLinkRe matches per-day Votes and Proceedings PDF links on MB session pages.
var mbVotesPDFLinkRe = regexp.MustCompile(`(?i)\d+(?:rd|th|st|nd)/votes_\d+\.pdf`)

// mbSessionPageLinkRe matches session-index page links on the MB V&P index page.
// Links have the form "43rd/43rd_3rd.html" (ordinal suffix on both the legislature
// and session components), so the session number must also end with an ordinal.
var mbSessionPageLinkRe = regexp.MustCompile(`(?i)\d+(?:rd|th|st|nd)/\d+(?:rd|th|st|nd)_\d+(?:rd|th|st|nd)\.html`)

// crawlManitobaVotesFromPDF performs a two-level crawl:
//
//	votes_proceedings.html → 43rd/43rd_3rd.html → 3rd/votes_NNN.pdf → parsePDFDivisionsYeasNays
func crawlManitobaVotesFromPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[mb-votes] fetching index: %s", indexURL)
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("mb votes index: %w", err)
	}

	// Level 1: find session-index pages.
	var sessionLinks []string
	seenSession := make(map[string]bool)
	indexDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !mbSessionPageLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if seenSession[full] {
			return
		}
		seenSession[full] = true
		sessionLinks = append(sessionLinks, full)
	})
	if len(sessionLinks) == 0 {
		log.Printf("[mb-votes] no session pages discovered; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "mb", "manitoba", legislature, session, client, manitobaVotesLinkRe)
	}
	sort.Strings(sessionLinks)
	if len(sessionLinks) > 6 {
		sessionLinks = sessionLinks[len(sessionLinks)-6:]
	}

	// Level 2: for each session page, collect PDF links.
	var pdfLinks []string
	seenPDF := make(map[string]bool)
	for _, sessURL := range sessionLinks {
		sessDoc, serr := fetchDoc(sessURL, client)
		if serr != nil {
			log.Printf("[mb-votes] skip session %s: %v", sessURL, serr)
			continue
		}
		sessDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href := normalizeHref(a.AttrOr("href", ""))
			if href == "" || !mbVotesPDFLinkRe.MatchString(href) {
				return
			}
			full := resolveRelativeURL(sessURL, href)
			if seenPDF[full] {
				return
			}
			seenPDF[full] = true
			pdfLinks = append(pdfLinks, full)
		})
	}

	sort.Strings(pdfLinks)
	if len(pdfLinks) > 80 {
		pdfLinks = pdfLinks[len(pdfLinks)-80:]
	}
	if len(pdfLinks) == 0 {
		log.Printf("[mb-votes] no VP PDFs discovered; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "mb", "manitoba", legislature, session, client, manitobaVotesLinkRe)
	}

	var results []ProvincialDivisionResult
	nextDivNum := 1
	for _, pdfURL := range pdfLinks {
		text, terr := downloadAndExtractPDFText(pdfURL, "mb", client)
		if terr != nil {
			log.Printf("[mb-votes] skip pdf %s: %v", pdfURL, terr)
			continue
		}
		date := extractDateFromURL(pdfURL)
		if date == "" {
			date = utils.FindDateInText(text)
		}
		if date == "" {
			date = utils.TodayISO()
		}
		divs := parsePDFDivisionsYeasNays(text, pdfURL, "mb", "manitoba", legislature, session, nextDivNum, date, extractPlainVoteNames)
		results = append(results, divs...)
		nextDivNum += len(divs)
		if len(divs) == 0 {
			nextDivNum++
		}
	}
	log.Printf("[mb-votes] parsed %d divisions from %d PDFs", len(results), len(pdfLinks))
	return results, nil
}

// CrawlManitobaVotes crawls Manitoba recorded votes/journal pages.
// Performs a two-level crawl: votes_proceedings.html → session-index page
// (e.g. 43rd/43rd_3rd.html) → per-day PDF (e.g. 3rd/votes_041.pdf).
// Each PDF is parsed for YEAS/NAYS recorded divisions using a format
// adapted from the New Brunswick journal parser.
func CrawlManitobaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.gov.mb.ca/legislature/business/votes_proceedings.html"
	}
	return crawlManitobaVotesFromPDF(indexURL, legislature, session, client)
}

// CrawlQuebecVotes crawls Quebec registre/votes pages.
func CrawlQuebecVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assnat.qc.ca/en/travaux-parlementaires/registre-des-votes/index.html"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}

	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("qc votes index: %w", err)
	}

	sessionLegislature := quebecSessionLegislatureValue(indexDoc, legislature, session)
	if sessionLegislature == "" {
		log.Printf("[qc-votes] sessionLegislature not found; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "qc", "quebec", legislature, session, client, quebecVotesLinkRe)
	}

	firstPage, err := quebecSearchVotes(indexURL, sessionLegislature, 0, 25, true, client)
	if err != nil {
		log.Printf("[qc-votes] JSON search failed (%v); falling back to generic parser", err)
		return crawlGenericProvincialVotesWithMatcher(indexURL, "qc", "quebec", legislature, session, client, quebecVotesLinkRe)
	}

	votes := append([]quebecVoteListing{}, firstPage.Donnees...)
	if firstPage.NombreTotalDonnees > len(firstPage.Donnees) {
		totalPages := (firstPage.NombreTotalDonnees + firstPage.QuantiteParPage - 1) / firstPage.QuantiteParPage
		for page := 1; page < totalPages; page++ {
			nextPage, perr := quebecPaginateVotes(indexURL, firstPage.NomRequete, page, firstPage.QuantiteParPage, client)
			if perr != nil {
				log.Printf("[qc-votes] pagination page=%d failed: %v", page, perr)
				continue
			}
			votes = append(votes, nextPage.Donnees...)
		}
	}

	results := make([]ProvincialDivisionResult, 0, len(votes))
	fallbackNum := 0
	for _, v := range votes {
		fallbackNum++
		divNum, _ := strconv.Atoi(strings.TrimSpace(v.Numero))
		if divNum <= 0 {
			divNum = fallbackNum
		}

		detailURL := resolveRelativeURL(indexURL, strings.TrimSpace(v.VoteURL))
		if detailURL == "" {
			continue
		}

		date := strings.TrimSpace(v.DateVote)
		if date == "" {
			date = extractDateFromURL(detailURL)
		}
		if date == "" {
			date = utils.TodayISO()
		}

		detailDoc, derr := fetchDoc(detailURL, client)
		if derr != nil {
			log.Printf("[qc-votes] skip vote detail %s: %v", detailURL, derr)
			continue
		}

		divID := ProvincialDivisionID("qc", legislature, session, divNum, date)
		memberVotes, yeas, nays := parseQuebecVoteDetailDoc(detailDoc, divID)

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  legislature,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: strings.TrimSpace(strings.Join(strings.Fields(v.Titre), " ")),
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     "quebec",
				DetailURL:   detailURL,
				LastScraped: utils.NowISO(),
			},
			Votes: memberVotes,
		})
	}

	log.Printf("[qc-votes] parsed %d divisions", len(results))
	return results, nil
}

// CrawlNewBrunswickVotes crawls NB journals/votes pages.
func CrawlNewBrunswickVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.legnb.ca/en/house-business/journals"
	}
	if client == nil {
		client = utils.NewHTTPClient()
	}
	return crawlNewBrunswickVotesFromPDF(indexURL, legislature, session, client)
}

// ── 5A.5 Newfoundland and Labrador ────────────────────────────────────────────

// nlSessionDirLinkRe matches NL journal session-directory links like "ga51session1/".
var nlSessionDirLinkRe = regexp.MustCompile(`(?i)ga\d+session\d+/?$`)

// nlJournalPDFLinkRe matches per-day NL journal PDF filenames like "26-04-14.pdf".
var nlJournalPDFLinkRe = regexp.MustCompile(`(?i)\d{2}-\d{2}-\d{2}\.pdf$`)

// nlShortDateRe extracts a YY-MM-DD date from an NL journal PDF URL.
var nlShortDateRe = regexp.MustCompile(`(\d{2})-(\d{2})-(\d{2})\.pdf`)

// nlCarriedRe matches outcome text in NL journal PDFs.
var nlCarriedRe = regexp.MustCompile(`(?i)(?:motion|amendment|bill|resolution)\s+(?:was\s+)?(?:agreed\s+to|carried)`)
var nlNegativedRe = regexp.MustCompile(`(?i)(?:motion|amendment|bill|resolution)\s+(?:was\s+)?(?:defeated|negatived|lost)`)

// nlMotionDescRe captures the motion text for NL journal divisions.
var nlMotionDescRe = regexp.MustCompile(`(?i)on\s+the\s+(?:motion|amendment|question)\s+(?:that\s+)?(.{0,180}?)(?:,\s+the\s+question\s+was\s+put|was\s+(?:agreed|carried|defeated|negatived))`)

// expandNLShortDate converts an NL journal filename date "YY-MM-DD" to ISO "20YY-MM-DD".
func expandNLShortDate(yy, mm, dd string) string {
	return "20" + yy + "-" + mm + "-" + dd
}

// parseNLJournalDivisions extracts division outcomes from an NL journal PDF text.
// NL Journals record proceedings minutes; per-member vote names are not present
// in the accessible static PDF format. This function records outcomes only
// (Yeas/Nays fields set to 0, MemberVotes empty).
func parseNLJournalDivisions(text, detailURL string, legislature, session, startDivisionNumber int, date string) []ProvincialDivisionResult {
	// Try YEAS/AYES member-level data first.
	if genericYeasNaysVoteSectionRe.MatchString(text) {
		return parsePDFDivisionsYeasNays(text, detailURL, "nl", "newfoundland_labrador", legislature, session, startDivisionNumber, date, parseNewBrunswickVoteNames)
	}

	// Outcome-only: find "agreed to" / "defeated" patterns.
	carriedIdxs := nlCarriedRe.FindAllStringIndex(text, -1)
	negIdxs := nlNegativedRe.FindAllStringIndex(text, -1)

	type outcome struct {
		pos    int
		result string
	}
	outcomes := make([]outcome, 0, len(carriedIdxs)+len(negIdxs))
	for _, m := range carriedIdxs {
		outcomes = append(outcomes, outcome{m[0], "Carried"})
	}
	for _, m := range negIdxs {
		outcomes = append(outcomes, outcome{m[0], "Negatived"})
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].pos < outcomes[j].pos })

	// Deduplicate outcomes that are within 400 chars of each other (same motion text).
	deduped := make([]outcome, 0, len(outcomes))
	for _, o := range outcomes {
		if len(deduped) > 0 && o.pos-deduped[len(deduped)-1].pos < 400 {
			continue
		}
		deduped = append(deduped, o)
	}

	results := make([]ProvincialDivisionResult, 0, len(deduped))
	for i, o := range deduped {
		divNum := startDivisionNumber + i
		divID := ProvincialDivisionID("nl", legislature, session, divNum, date)

		start := o.pos - 300
		if start < 0 {
			start = 0
		}
		snippet := text[start:o.pos]
		desc := ""
		if m := nlMotionDescRe.FindStringSubmatch(snippet); len(m) == 2 {
			desc = strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(m[1], "\u00a0", " ")), " "))
		}
		if desc == "" {
			desc = strings.TrimSpace(snippet)
			if len(desc) > 200 {
				desc = desc[len(desc)-200:]
			}
			desc = strings.TrimSpace(desc)
		}
		if desc == "" {
			desc = "Division"
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID: divID, Parliament: legislature, Session: session,
				Number: divNum, Date: date, Description: desc,
				Yeas: 0, Nays: 0, Result: o.result,
				Chamber: "newfoundland_labrador", DetailURL: detailURL, LastScraped: utils.NowISO(),
			},
		})
	}
	log.Printf("[nl-votes] %s: parsed %d divisions (outcome-only)", date, len(results))
	return results
}

// ParseNLJournalDivisionsForTest is test-only access to NL journal parsing.
func ParseNLJournalDivisionsForTest(text, detailURL string, legislature, session, startDivisionNumber int, date string) []ProvincialDivisionResult {
	return parseNLJournalDivisions(text, detailURL, legislature, session, startDivisionNumber, date)
}

// crawlNLVotesFromPDF performs a two-level crawl of the NL assembly journals:
//
//	/HouseBusiness/Journals/ → ga51session1/ → YY-MM-DD.pdf → parseNLJournalDivisions
func crawlNLVotesFromPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[nl-votes] fetching journals index: %s", indexURL)
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("nl journals index: %w", err)
	}

	// Level 1: session directories.
	var sessionDirs []string
	seenDir := make(map[string]bool)
	indexDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !nlSessionDirLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if !strings.HasSuffix(full, "/") {
			full += "/"
		}
		if seenDir[full] {
			return
		}
		seenDir[full] = true
		sessionDirs = append(sessionDirs, full)
	})
	if len(sessionDirs) == 0 {
		log.Printf("[nl-votes] no session directories found; falling back to generic parser")
		return crawlGenericProvincialVotesWithMatcher(indexURL, "nl", "newfoundland_labrador", legislature, session, client, newfoundlandVotesLinkRe)
	}
	sort.Strings(sessionDirs)
	if len(sessionDirs) > 4 {
		sessionDirs = sessionDirs[len(sessionDirs)-4:]
	}

	// Level 2: per-day PDF links in each session directory.
	var pdfLinks []string
	seenPDF := make(map[string]bool)
	for _, dirURL := range sessionDirs {
		dirDoc, derr := fetchDoc(dirURL, client)
		if derr != nil {
			log.Printf("[nl-votes] skip session dir %s: %v", dirURL, derr)
			continue
		}
		dirDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href := normalizeHref(a.AttrOr("href", ""))
			if href == "" || !nlJournalPDFLinkRe.MatchString(href) {
				return
			}
			full := resolveRelativeURL(dirURL, href)
			if seenPDF[full] {
				return
			}
			seenPDF[full] = true
			pdfLinks = append(pdfLinks, full)
		})
	}

	sort.Strings(pdfLinks)
	if len(pdfLinks) > 80 {
		pdfLinks = pdfLinks[len(pdfLinks)-80:]
	}
	if len(pdfLinks) == 0 {
		log.Printf("[nl-votes] no journal PDFs discovered")
		return nil, nil
	}

	var results []ProvincialDivisionResult
	nextDivNum := 1
	for _, pdfURL := range pdfLinks {
		text, terr := downloadAndExtractPDFText(pdfURL, "nl", client)
		if terr != nil {
			log.Printf("[nl-votes] skip pdf %s: %v", pdfURL, terr)
			continue
		}
		date := ""
		if m := nlShortDateRe.FindStringSubmatch(pdfURL); len(m) == 4 {
			date = expandNLShortDate(m[1], m[2], m[3])
		}
		if date == "" {
			date = extractDateFromURL(pdfURL)
		}
		if date == "" {
			date = utils.FindDateInText(text)
		}
		if date == "" {
			date = utils.TodayISO()
		}
		divs := parseNLJournalDivisions(text, pdfURL, legislature, session, nextDivNum, date)
		results = append(results, divs...)
		nextDivNum += len(divs)
		if len(divs) == 0 {
			nextDivNum++
		}
	}
	log.Printf("[nl-votes] parsed %d divisions from %d PDFs", len(results), len(pdfLinks))
	return results, nil
}

// CrawlNewfoundlandAndLabradorVotes crawls NL assembly journal PDFs for division outcomes.
// NL Journal PDFs contain proceedings minutes; per-member AYES/NAYS name lists are not
// present in the accessible static PDF format. Division results (Carried/Negatived) are
// extracted from the motion outcome text; yea/nay counts are not available.
// Two-level crawl: /HouseBusiness/Journals/ → ga51session1/ → YY-MM-DD.pdf
func CrawlNewfoundlandAndLabradorVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.nl.ca/HouseBusiness/Journals/"
	}
	return crawlNLVotesFromPDF(indexURL, legislature, session, client)
}

// ── 5A.6 Nova Scotia ─────────────────────────────────────────────────────────

// nsVotesPDFLinkRe matches NS journal PDF links under the default files path.
var nsVotesPDFLinkRe = regexp.MustCompile(`(?i)/sites/default/files/pdfs/proceedings/journals/[^"'\s]+\.pdf`)

// crawlNovaScotiaVotesFromPDF fetches the NS journals index page (using an extended
// HTTP timeout because the Drupal page is ~368KB) and parses each discovered PDF.
func crawlNovaScotiaVotesFromPDF(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	log.Printf("[ns-votes] fetching index: %s", indexURL)
	indexDoc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("ns votes index: %w", err)
	}

	var pdfLinks []string
	seen := make(map[string]bool)
	indexDoc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href := normalizeHref(a.AttrOr("href", ""))
		if href == "" || !nsVotesPDFLinkRe.MatchString(href) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if seen[full] {
			return
		}
		seen[full] = true
		pdfLinks = append(pdfLinks, full)
	})

	sort.Strings(pdfLinks)
	if len(pdfLinks) > 100 {
		pdfLinks = pdfLinks[len(pdfLinks)-100:]
	}
	if len(pdfLinks) == 0 {
		log.Printf("[ns-votes] no journal PDFs discovered (64th/65th Assembly data not yet accessible as static files)")
		return nil, nil
	}

	var results []ProvincialDivisionResult
	nextDivNum := 1
	for _, pdfURL := range pdfLinks {
		text, terr := downloadAndExtractPDFText(pdfURL, "ns", client)
		if terr != nil {
			log.Printf("[ns-votes] skip pdf %s: %v", pdfURL, terr)
			continue
		}
		date := extractDateFromURL(pdfURL)
		if date == "" {
			date = utils.FindDateInText(text)
		}
		if date == "" {
			date = utils.TodayISO()
		}
		divs := parsePDFDivisionsYeasNays(text, pdfURL, "ns", "nova_scotia", legislature, session, nextDivNum, date, extractPlainVoteNames)
		results = append(results, divs...)
		nextDivNum += len(divs)
		if len(divs) == 0 {
			nextDivNum++
		}
	}
	log.Printf("[ns-votes] parsed %d divisions from %d PDFs", len(results), len(pdfLinks))
	return results, nil
}

// CrawlNovaScotiaVotes crawls NS journals/proceedings pages.
//
// The NS journals index page (nslegislature.ca) is a ~368KB Drupal response and
// reliably times out with the default 15s HTTP client. A 45s timeout is used when
// the caller does not supply a client. Historical session data (through 63rd
// Assembly, 3rd Session, April 2021) is available as static PDFs under
// /sites/default/files/pdfs/proceedings/journals/. The 64th and 65th Assembly
// journals are not yet accessible as static files; the scraper logs a note when
// no PDFs are found.
func CrawlNovaScotiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://nslegislature.ca/legislative-business/journals-votes-proceedings"
	}
	if client == nil {
		// The NS journals index page is large (~368KB); use an extended timeout.
		client = utils.NewHTTPClientWithTimeout(45 * time.Second)
	}
	return crawlNovaScotiaVotesFromPDF(indexURL, legislature, session, client)
}

// ── 5A.7 Prince Edward Island ─────────────────────────────────────────────────

// peiCaptchaSignature is a substring present in Radware bot-manager CAPTCHA pages
// returned by assembly.pe.ca for automated clients.
const peiCaptchaSignature = "captcha.perfdrive.com"

// peiWDFAPIBase is the base URL for the PEI Web Data Framework (WDF) service that
// hosts the legislative assembly workflow API.
const peiWDFAPIBase = "https://wdf.princeedwardisland.ca"

// peiAssemblyBase is the root URL for the PEI Legislative Assembly website.
const peiAssemblyBase = "https://www.assembly.pe.ca"

// peiJournalsIndexURL is the default index page for PEI journals (votes and proceedings).
const peiJournalsIndexURL = peiAssemblyBase + "/legislative-business/house-records/journals"

// peiDefaultDelay is the per-request rate-limit interval for production PEI
// crawls: 6 seconds ≈ 10 requests per minute, reducing the chance of triggering
// Radware bot-manager detection.
const peiDefaultDelay = 6 * time.Second

// peiWorkflowJournals is the WDF workflow name for the PEI legislative journals search.
const peiWorkflowJournals = "LegislativeAssemblyJournals"

// peiGeneralAssembly and peiAssemblySession are last-resort fallback values for
// the current PEI legislature and session. They are used only when the WDF API
// auto-detection and the DB lookup both fail. Keep them up to date manually if
// automatic detection stops working.
// 67th General Assembly, 3rd Session (opened March 25, 2026).
var peiGeneralAssembly = 67
var peiAssemblySession = 3

// fetchPEICurrentAssemblySession queries the WDF bills workflow to determine
// the current PEI legislature (General Assembly) and session numbers. It scans
// the query params and router-link paths on bill links for known patterns.
//
// Returns (legislature, session, true) on success, or (0, 0, false) when the
// WDF API is unavailable or no assembly/session information can be extracted.
// In test environments (no Node.js / pei_fetch.js), this always returns false
// and the caller falls back to peiGeneralAssembly / peiAssemblySession.
func fetchPEICurrentAssemblySession() (int, int, bool) {
	queryVars := map[string]string{
		"year":          strconv.Itoa(time.Now().Year()),
		"search":        "year",
		"search_bills":  "true",
		"wdf_url_query": "true",
	}
	data, err := invokePEIFetchJS(peiWorkflowBills, peiWDFActivityBills, queryVars)
	if err != nil || data == nil {
		return 0, 0, false
	}
	var resp wdfTreeResponse
	if err := json.Unmarshal(data, &resp); err != nil || resp.Data == nil {
		return 0, 0, false
	}
	for _, row := range wdfCollectRows(resp.Data) {
		for _, cell := range row.Children {
			for _, child := range cell.Children {
				if child.Type != "LinkV2" {
					continue
				}
				var ld wdfLinkData
				if json.Unmarshal(child.Data, &ld) != nil {
					continue
				}
				// Try query params: the Angular router may pass assembly/session
				// numbers as explicit query parameters on each bill link.
				if l, s, ok := peiAssemblySessionFromQueryParams(ld.QueryParams); ok {
					return l, s, true
				}
				// Fall back to pattern matching on the router-link path.
				if ld.RouterLink != nil && *ld.RouterLink != "" {
					if candidates := extractLegislatureSessionCandidates("pe", *ld.RouterLink, 50); len(candidates) > 0 {
						best := candidates[0]
						for _, c := range candidates[1:] {
							if c.Score > best.Score {
								best = c
							}
						}
						return best.Legislature, best.Session, true
					}
				}
			}
		}
	}
	return 0, 0, false
}

// peiAssemblySessionFromQueryParams extracts the legislature and session numbers
// from Angular router query params on a WDF bill link. It recognises keys that
// contain "assembly", "legislature", or "ga" (for General Assembly) as the
// legislature number, and keys that contain "session" as the session number.
func peiAssemblySessionFromQueryParams(params map[string]string) (int, int, bool) {
	if len(params) == 0 {
		return 0, 0, false
	}
	var legislature, session int
	for k, v := range params {
		kl := strings.ToLower(k)
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n <= 0 {
			continue
		}
		switch {
		case strings.Contains(kl, "assembly") || strings.Contains(kl, "legislature") || kl == "ga":
			legislature = n
		case strings.Contains(kl, "session"):
			session = n
		}
	}
	if legislature > 0 && session > 0 {
		return legislature, session, true
	}
	return 0, 0, false
}


// The Type field identifies the component (TableV2Row, LinkV2, Paginator, etc.).
// Data is kept as raw JSON because its structure varies by node type.
type wdfNode struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data"`
	Children []wdfNode       `json:"children"`
}

// wdfTreeResponse is the top-level envelope of a WDF workflow API response.
type wdfTreeResponse struct {
	ProcessInstanceID string `json:"processInstanceId"`
	Messages          struct {
		Error []string `json:"error"`
	} `json:"messages"`
	Data []wdfNode `json:"data"`
}

// wdfCellData holds the typed fields of a TableV2Cell node's data object.
type wdfCellData struct {
	Text *string `json:"text"`
}

// wdfLinkData holds the typed fields of a LinkV2 node's data object.
type wdfLinkData struct {
	Text        string            `json:"text"`
	Href        *string           `json:"href"`
	RouterLink  *string           `json:"routerLink"`
	QueryParams map[string]string `json:"queryParams"`
}

// wdfCollectRows recursively collects all TableV2Row nodes from a WDF node tree.
func wdfCollectRows(nodes []wdfNode) []wdfNode {
	var out []wdfNode
	for _, n := range nodes {
		if n.Type == "TableV2Row" {
			out = append(out, n)
		}
		out = append(out, wdfCollectRows(n.Children)...)
	}
	return out
}

// peiWDFActivityJournals is the WDF activity name for the PEI journals search.
const peiWDFActivityJournals = "LegislativeAssemblyJournalsSearch"

// postPEIWorkflow POSTs a workflow request to the PEI WDF API and returns the raw
// response body. It sends CORS-mode headers so the Spring Boot backend routes the
// request correctly; a no-redirect policy makes Radware bot-manager 302 challenges
// visible as non-200 status codes. Returns (nil, nil) when the API is unavailable.
//
// postPEIWorkflow fetches WDF API data for PEI. In production (wdfBase ==
// peiWDFAPIBase) it invokes the pei_fetch.js Node subprocess which uses a real
// Chrome browser to bypass Radware bot-manager. In tests (wdfBase != "") it
// falls back to a direct HTTP POST to the mock server using the correct WDF
// body structure. Returns (nil, nil) when the API is unavailable.
func postPEIWorkflow(wdfBase, workflowName, activityName string, queryVars map[string]string, client *http.Client, delay time.Duration) ([]byte, error) {
	// Use the Node/Chrome bridge for any real remote URL; fall back to direct
	// HTTP only for local test servers (127.0.0.1 / localhost).
	isTestServer := strings.HasPrefix(wdfBase, "http://127.0.0.1") || strings.HasPrefix(wdfBase, "http://localhost")
	if !isTestServer {
		data, err := invokePEIFetchJS(workflowName, activityName, queryVars)
		if err != nil {
			log.Printf("[pe-wdf] pei_fetch.js error: %v; will fall back to HTML", err)
			return nil, nil
		}
		if data != nil {
			time.Sleep(delay)
		}
		return data, nil
	}
	// Test path: direct HTTP to mock server.
	return postPEIWorkflowHTTP(wdfBase, workflowName, activityName, queryVars, delay)
}

// invokePEIFetchJS calls scripts/pei_fetch.js via Node to fetch WDF data
// through a real Chrome browser. Returns (nil, nil) when Node or the script
// is not available, so callers can fall back to HTML scraping.
func invokePEIFetchJS(workflowName, activityName string, queryVars map[string]string) ([]byte, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, nil
	}
	scriptPath := filepath.Join("scripts", "pei_fetch.js")
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, nil
	}
	qvJSON, err := json.Marshal(queryVars)
	if err != nil {
		return nil, fmt.Errorf("marshal queryVars: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, scriptPath, workflowName, activityName, string(qvJSON))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[pe-wdf] pei_fetch.js: %v stderr=%s", err, stderr.String())
		return nil, nil
	}
	return stdout.Bytes(), nil
}

// postPEIWorkflowHTTP posts the WDF body directly to wdfBase (used by tests).
func postPEIWorkflowHTTP(wdfBase, workflowName, activityName string, queryVars map[string]string, delay time.Duration) ([]byte, error) {
	apiURL := strings.TrimRight(wdfBase, "/") + "/legislative-assembly/services/api/workflow"

	// Build queryVars with service/activity merged in.
	merged := make(map[string]interface{}, len(queryVars)+2)
	merged["service"] = workflowName
	merged["activity"] = activityName
	for k, v := range queryVars {
		merged[k] = v
	}
	payload := map[string]interface{}{
		"appName":     workflowName,
		"featureName": workflowName,
		"metaVars":    map[string]interface{}{"service_id": nil, "save_location": nil},
		"queryVars":   merged,
		"queryName":   activityName,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("pe wdf marshal: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("pe wdf request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Client-Show-Status", "true")

	noRedirect := &http.Client{
		Transport:     http.DefaultTransport,
		Timeout:       20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pe wdf do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[pe-wdf] %s returned HTTP %d; will fall back to HTML", workflowName, resp.StatusCode)
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("pe wdf read: %w", err)
	}
	time.Sleep(delay)
	return data, nil
}

// ── PEI journal PDF division parser ──────────────────────────────────────────

// peiDivTriggerRe matches the recorded-division announcement in PEI journals.
var peiDivTriggerRe = regexp.MustCompile(`(?i)A\s+Recorded\s+Division\s+being\s+sought[^.]*?the\s+names\s+were\s+recorded[^.]*?as\s+follows:`)

// peiDivCountRe matches vote-count markers in normalized PEI PDF text, e.g.
// "Nays 12 \" or "Yeas ( 12 \". Parentheses are optional because pdfcpu
// extraction sometimes strips them while preserving the trailing backslash.
var peiDivCountRe = regexp.MustCompile(`(?i)(Nays?|Yeas?)\s*\(?\s*(\d[\d\s]*)\s*\\`)

// peiDivOutcomeRe matches the outcome sentence that closes a recorded division.
var peiDivOutcomeRe = regexp.MustCompile(`(?i)(?:Motion\s+(?:was\s+)?(?:CARRIED|NEGATIVED)|CARRIED\s+UNANIMOUSLY|Motion\s+resolved\s+in\s+the)`)

// peiJournalPageHeaderRe matches the running header injected by pdfcpu into the
// extracted text at every page boundary.
var peiJournalPageHeaderRe = regexp.MustCompile(`JOURNAL OF THE LEGISLATIVE ASSEMBLY`)

// peiOctalEscapeRe matches PDF octal-escape sequences like \222 (→ apostrophe).
var peiOctalEscapeRe = regexp.MustCompile(`\\(\d{3})`)

// peiPremierRe matches the "Hon. Premier" title which appears without a riding suffix.
var peiPremierRe = regexp.MustCompile(`(?i)Hon\.\s+Premier`)

// peiTitlePrefixes are leadership titles that precede a member's name.
var peiTitlePrefixes = []string{
	"Hon. Leader of the Opposition",
	"Hon. Leader of the Third Party",
	"Leader of the Third Party",
	"Leader of the Opposition",
}

// peiRidingStartWords is the set of first words of all known PEI electoral
// district names (67th and 66th General Assemblies). It is used to detect the
// boundary between a member's name and the following riding name in journal
// PDF text, where names and ridings appear consecutively without a delimiter.
var peiRidingStartWords = map[string]struct{}{
	// 67th General Assembly districts (2023–present)
	"Charlottetown": {}, "Summerside": {}, "Stanhope": {}, "Mermaid": {},
	"Morell": {}, "Kellys": {}, "New": {}, "Borden": {}, "Brackley": {},
	"Evangeline": {}, "Alberton": {}, "Tignish": {}, "O'Leary": {},
	"Tyne": {}, "Kensington": {}, "Crapaud": {}, "Georgetown": {},
	"Vernon": {}, "Murray": {}, "Rustico": {},
	// Additional first words from 66th General Assembly districts
	"Cornwall": {}, "Souris": {}, "Stratford": {}, "Kinkora": {},
}

// parsePEIJournalDivisions extracts recorded division results from the normalized
// text of a PEI legislative journal PDF. Division numbers begin at startDivNum.
func parsePEIJournalDivisions(rawText, pdfURL string, legislature, session, startDivNum int, date string) []ProvincialDivisionResult {
	// Decode PDF octal escapes (\222 → ') so the only remaining backslashes are
	// the riding-name terminators used by pdfcpu to encode closing string parens.
	text := peiOctalEscapeRe.ReplaceAllStringFunc(rawText, func(m string) string {
		n, err := strconv.ParseInt(m[1:], 8, 32)
		if err != nil || n < 32 || n > 255 {
			return "'"
		}
		return string(rune(n))
	})
	text = peiJournalPageHeaderRe.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")

	triggers := peiDivTriggerRe.FindAllStringIndex(text, -1)
	if len(triggers) == 0 {
		return nil
	}

	var results []ProvincialDivisionResult
	divNum := startDivNum
	for i, trigger := range triggers {
		blockStart := trigger[1]
		blockEnd := len(text)
		if i+1 < len(triggers) {
			blockEnd = triggers[i+1][0]
		}
		// Trim block to just after the outcome sentence.
		if m := peiDivOutcomeRe.FindStringIndex(text[blockStart:blockEnd]); m != nil {
			end := blockStart + m[1] + 80
			if end < blockEnd {
				blockEnd = end
			}
		}
		block := text[blockStart:blockEnd]

		counts := peiDivCountRe.FindAllStringSubmatchIndex(block, -1)
		if len(counts) == 0 {
			continue
		}

		var yeas, nays int
		var yeasBlock, naysBlock string

		firstLabel := strings.ToLower(block[counts[0][2]:counts[0][3]])
		firstCountStr := strings.ReplaceAll(block[counts[0][4]:counts[0][5]], " ", "")
		firstCount, _ := strconv.Atoi(firstCountStr)

		if len(counts) >= 2 {
			firstMemberBlock := block[counts[0][1]:counts[1][0]]
			_ = strings.ToLower(block[counts[1][2]:counts[1][3]]) // label unused; position drives block split
			secondCountStr := strings.ReplaceAll(block[counts[1][4]:counts[1][5]], " ", "")
			secondCount, _ := strconv.Atoi(secondCountStr)
			secondMemberBlock := block[counts[1][1]:]

			if strings.HasPrefix(firstLabel, "yea") {
				yeas, yeasBlock = firstCount, firstMemberBlock
				nays, naysBlock = secondCount, secondMemberBlock
			} else {
				nays, naysBlock = firstCount, firstMemberBlock
				yeas, yeasBlock = secondCount, secondMemberBlock
			}
		} else {
			memberBlock := block[counts[0][1]:]
			if strings.HasPrefix(firstLabel, "yea") {
				yeas, yeasBlock = firstCount, memberBlock
			} else {
				nays, naysBlock = firstCount, memberBlock
			}
		}

		if yeas == 0 && nays == 0 {
			continue
		}

		// Extract description from the context just before the division trigger.
		desc := "Recorded division"
		descStart := trigger[0] - 250
		if descStart < 0 {
			descStart = 0
		}
		if ctx := strings.TrimSpace(text[descStart:trigger[0]]); ctx != "" {
			if idx := strings.LastIndexAny(ctx, ".;"); idx >= 0 {
				ctx = strings.TrimSpace(ctx[idx+1:])
			}
			if len(ctx) > 200 {
				ctx = ctx[:200]
			}
			if ctx = strings.Join(strings.Fields(ctx), " "); ctx != "" {
				desc = ctx
			}
		}

		divID := ProvincialDivisionID("pe", legislature, session, divNum, date)
		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		var votes []ProvincialMemberVote
		for _, name := range parsePEIJournalMembers(yeasBlock) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Yea"})
		}
		for _, name := range parsePEIJournalMembers(naysBlock) {
			votes = append(votes, ProvincialMemberVote{DivisionID: divID, MemberName: name, Vote: "Nay"})
		}

		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID: divID, Parliament: legislature, Session: session,
				Number: divNum, Date: date, Description: desc,
				Yeas: yeas, Nays: nays, Result: result,
				Chamber: "pei", DetailURL: pdfURL, LastScraped: utils.NowISO(),
			},
			Votes: votes,
		})
		divNum++
	}
	return results
}

// parsePEIJournalMembers extracts MLA names from a Yeas or Nays member-list block.
// Each entry ends with a "(Constituency\" riding/portfolio suffix; "Hon. Premier"
// appears without a riding suffix and is handled specially.
func parsePEIJournalMembers(block string) []string {
	if strings.TrimSpace(block) == "" {
		return nil
	}
	parts := strings.Split(block, `\`)
	seen := make(map[string]bool)
	var names []string
	for _, part := range parts {
		for _, n := range peiExtractNamesFromChunk(part) {
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

// peiExtractNamesFromChunk derives one or two member names from the text chunk
// that precedes a riding/portfolio suffix. Handles "Hon. Premier" (no riding)
// and optional title prefixes like "Leader of the Third Party".
func peiExtractNamesFromChunk(chunk string) []string {
	chunk = strings.TrimSpace(chunk)
	chunk = strings.Join(strings.Fields(chunk), " ")
	chunk = strings.ReplaceAll(chunk, " - ", "-")
	chunk = regexp.MustCompile(`(?i)^(Nays?|Yeas?)\s*\(?\s*\d+\s*`).ReplaceAllString(chunk, "")
	// Skip empty chunks and orphaned riding fragments like "- Inverness".
	if chunk == "" || strings.HasPrefix(chunk, "- ") {
		return nil
	}

	var results []string

	// "Hon. Premier" is a title, not a stable member name. Remove it and continue
	// extracting the actual MLA name that usually follows in the same chunk.
	if peiPremierRe.MatchString(chunk) {
		chunk = strings.TrimSpace(peiPremierRe.ReplaceAllString(chunk, ""))
		if chunk == "" {
			return results
		}
	}

	// Strip leadership title prefixes that precede the member's actual name.
	for _, title := range peiTitlePrefixes {
		if strings.HasPrefix(chunk, title) {
			chunk = strings.TrimSpace(chunk[len(title):])
			break
		}
	}
	chunk = strings.TrimPrefix(chunk, "Hon. ")
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return results
	}
	lower := strings.ToLower(chunk)
	if strings.HasPrefix(lower, "the motion") || strings.HasPrefix(lower, "motion resolved") || strings.HasPrefix(lower, "ordered") || strings.HasPrefix(lower, "journal of") {
		return results
	}

	words := strings.Fields(chunk)
	if len(words) < 2 {
		return results
	}

	name := make([]string, 0, 3)
	for i, w := range words {
		clean := strings.Trim(w, ",.;:()")
		if clean == "" {
			continue
		}
		if i > 0 {
			if _, isRiding := peiRidingStartWords[clean]; isRiding && len(name) >= 2 {
				break
			}
		}
		if len(name) == 3 {
			break
		}
		name = append(name, clean)
		if len(name) >= 2 && i+1 < len(words) {
			next := strings.Trim(words[i+1], ",.;:()")
			if _, isRiding := peiRidingStartWords[next]; isRiding {
				break
			}
		}
	}

	if len(name) >= 2 {
		results = append(results, strings.Join(name, " "))
	}
	return results
}

// ParsePEIJournalDivisionsForTest is test-only access to the PEI journal parser.
func ParsePEIJournalDivisionsForTest(text, pdfURL string, legislature, session, startDivNum int, date string) []ProvincialDivisionResult {
	return parsePEIJournalDivisions(text, pdfURL, legislature, session, startDivNum, date)
}

// crawlPEIVotesFromWorkflow queries the WDF journals workflow for PEI journal
// PDF links and parses each PDF for recorded divisions.
// Returns (nil, nil) when the workflow API is unavailable or returns no items.
func crawlPEIVotesFromWorkflow(wdfBase string, year, legislature, session int, client *http.Client, delay time.Duration) ([]ProvincialDivisionResult, error) {
	queryVars := map[string]string{
		"year":          strconv.Itoa(year),
		"search":        "year",
		"wdf_url_query": "true",
	}
	body, err := postPEIWorkflow(wdfBase, peiWorkflowJournals, peiWDFActivityJournals, queryVars, client, delay)
	if err != nil || body == nil {
		return nil, err
	}

	var resp wdfTreeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("[pe-votes] wdf tree decode: %v; falling back to HTML", err)
		return nil, nil
	}
	if resp.Data == nil {
		log.Printf("[pe-votes] wdf returned null data; falling back to HTML")
		return nil, nil
	}

	rows := wdfCollectRows(resp.Data)
	if len(rows) == 0 {
		log.Printf("[pe-votes] wdf returned 0 journal rows; falling back to HTML")
		return nil, nil
	}

	// WDF returns newest-first; reverse to process oldest-first for sequential numbering.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	var results []ProvincialDivisionResult
	nextDivNum := 1
	for _, row := range rows {
		if len(row.Children) == 0 {
			continue
		}
		// Cell 0: date link — PDF href from docs.assembly.pe.ca or an HTML page link.
		var link, date string
		if len(row.Children[0].Children) > 0 {
			lnk := row.Children[0].Children[0]
			if lnk.Type == "LinkV2" {
				var ld wdfLinkData
				if json.Unmarshal(lnk.Data, &ld) == nil {
					if ld.Href != nil && *ld.Href != "" {
						link = *ld.Href
					}
					date = utils.FindDateInText(ld.Text)
				}
			}
		}
		if link == "" {
			continue
		}
		if date == "" {
			date = extractDateFromURL(link)
		}

		if strings.Contains(link, "docs.assembly.pe.ca") {
			// The WDF href may contain unencoded spaces in query params (e.g. in
			// the fileName value). Re-encode via url.Values.Encode() so the HTTP
			// client sends a valid request.
			safeLink := link
			if u, uerr := neturl.Parse(link); uerr == nil {
				u.RawQuery = u.Query().Encode()
				safeLink = u.String()
			}
			text, terr := downloadAndExtractPDFText(safeLink, "pe", client)
			if terr != nil {
				log.Printf("[pe-votes] wdf journal %s: %v; skipping", link, terr)
				continue
			}
			parsed := parsePEIJournalDivisions(text, link, legislature, session, nextDivNum, date)
			nextDivNum += len(parsed)
			results = append(results, parsed...)
			time.Sleep(delay)
			continue
		}

		fullLink := resolveRelativeURL(peiAssemblyBase, link)
		doc, derr := fetchDoc(fullLink, client)
		if derr != nil {
			log.Printf("[pe-votes] wdf journal %s: %v", fullLink, derr)
			continue
		}
		parsed := parseGenericProvincialVotesDoc(doc, "pe", "pei", legislature, session, date)
		nextDivNum += len(parsed)
		results = append(results, parsed...)
		time.Sleep(delay)
	}

	log.Printf("[pe-votes] wdf parsed %d divisions from %d journals", len(results), len(rows))
	return results, nil
}

// peiTransport adds browser-like request headers to bypass Radware bot-manager.
// delay is inserted after every roundtrip (including errors) to rate-limit
// requests to assembly.pe.ca.
type peiTransport struct {
	base  http.RoundTripper
	delay time.Duration
}

func (t *peiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	clone.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	clone.Header.Set("Accept-Language", "en-CA,en-US;q=0.7,en;q=0.3")
	clone.Header.Set("Sec-Fetch-Dest", "document")
	clone.Header.Set("Sec-Fetch-Mode", "navigate")
	clone.Header.Set("Sec-Fetch-Site", "none")
	resp, err := t.base.RoundTrip(clone)
	// Rate-limit HTML page fetches regardless of error status; a failed request
	// should not trigger an immediate retry burst.
	time.Sleep(t.delay)
	return resp, err
}

// crawlPEIVotes is the inner PEI crawl that checks for CAPTCHA and falls back
// to the generic HTML scraper when the site is accessible.
func crawlPEIVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	log.Printf("[pe-votes] fetching index: %s", indexURL)
	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, fmt.Errorf("pe votes index: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("pe votes index read: %w", err)
	}

	if strings.Contains(string(body), peiCaptchaSignature) {
		log.Printf("[pe-votes] CAPTCHA detected — assembly.pe.ca is protected by Radware bot-manager; returning 0 divisions. See docs/implementation-plan-detailed.md § 5A.7 for escalation path.")
		return nil, nil
	}

	// Real HTML received; parse with goquery and the generic HTML vote parser.
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("pe votes parse: %w", err)
	}
	links := discoverProvincialVoteLinksWithMatcher(doc, indexURL, peiVotesLinkRe)
	if len(links) == 0 {
		links = []string{indexURL}
	}
	var results []ProvincialDivisionResult
	for _, link := range links {
		dayDoc, derr := fetchDoc(link, client)
		if derr != nil {
			log.Printf("[pe-votes] skip day link %s: %v", link, derr)
			continue
		}
		date := extractDateFromURL(link)
		parsed := parseGenericProvincialVotesDoc(dayDoc, "pe", "pei", legislature, session, date)
		results = append(results, parsed...)
	}
	log.Printf("[pe-votes] parsed %d divisions", len(results))
	return results, nil
}

// CrawlPrinceEdwardIslandVotes crawls PEI votes/proceedings pages.
//
// assembly.pe.ca is protected by a Radware bot-manager CAPTCHA challenge. This
// function uses a browser-like User-Agent and request headers to attempt bypass.
// When a nil client is passed (production), a dedicated transport is used that
// sends Chrome-style headers. When a non-nil client is passed (tests), that client
// is used as-is so test servers remain reachable.
//
// If the CAPTCHA page is detected (contains "captcha.perfdrive.com"), the function
// logs a warning and returns 0 divisions with no error, preserving crawl continuity.
// See docs/implementation-plan-detailed.md § 5A.7 for the escalation path
// (headless Chromium) if header spoofing continues to fail.
// newPEIHTTPClient returns an HTTP client with browser-like headers for assembly.pe.ca.
// It is used by both the bills and votes crawlers for that province.
// delay is embedded in the transport so every request through this client is
// rate-limited independently of any other provincial crawler.
func newPEIHTTPClient(delay time.Duration) *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &peiTransport{base: http.DefaultTransport, delay: delay},
	}
}

func CrawlPrinceEdwardIslandVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	defaultURL := indexURL == ""
	if defaultURL {
		indexURL = peiJournalsIndexURL
	}
	// When no client is supplied (production), create a PEI-specific client with
	// browser-like headers and the production rate-limit delay.  When the caller
	// provides their own client (e.g. tests), use a zero delay so the test suite
	// runs at full speed and the rate limiter has no effect outside of PEI crawls.
	delay := time.Duration(0)
	if client == nil {
		delay = peiDefaultDelay
		client = newPEIHTTPClient(delay)
	}

	// Attempt WDF workflow API first. In production (defaultURL), use the canonical
	// WDF base. When a test server URL is passed, route the WDF call through the same
	// server so tests can mock both paths.
	wdfBase := peiWDFAPIBase
	if !defaultURL {
		wdfBase = indexURL
	}
	year := time.Now().Year()
	divs, err := crawlPEIVotesFromWorkflow(wdfBase, year, legislature, session, client, delay)
	if err == nil && len(divs) > 0 {
		return divs, nil
	}
	if err != nil {
		log.Printf("[pe-votes] wdf api: %v; falling back to HTML", err)
	}

	return crawlPEIVotes(indexURL, legislature, session, client)
}

// CrawlGenericProvincialVotes fetches a provincial votes/proceedings index page,
// discovers likely per-day links, then parses divisions from each page using
// resilient heuristics that work across multiple legislature layouts.
func CrawlGenericProvincialVotes(indexURL, provinceCode, chamber string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	return crawlGenericProvincialVotesWithMatcher(indexURL, provinceCode, chamber, legislature, session, client, genericVotesLinkRe)
}

func crawlGenericProvincialVotesWithMatcher(indexURL, provinceCode, chamber string, legislature, session int, client *http.Client, linkMatcher *regexp.Regexp) ([]ProvincialDivisionResult, error) {
	if client == nil {
		client = utils.NewHTTPClient()
	}
	log.Printf("[%s-votes] fetching index: %s", provinceCode, indexURL)

	doc, err := fetchDoc(indexURL, client)
	if err != nil {
		return nil, fmt.Errorf("%s generic index: %w", provinceCode, err)
	}

	links := discoverProvincialVoteLinksWithMatcher(doc, indexURL, linkMatcher)
	if len(links) == 0 {
		links = []string{indexURL}
	}

	results := make([]ProvincialDivisionResult, 0)
	for _, link := range links {
		dayDoc, derr := fetchDoc(link, client)
		if derr != nil {
			log.Printf("[%s-votes] skip day link %s: %v", provinceCode, link, derr)
			continue
		}
		date := extractDateFromURL(link)
		parsed := parseGenericProvincialVotesDoc(dayDoc, provinceCode, chamber, legislature, session, date)
		results = append(results, parsed...)
	}

	log.Printf("[%s-votes] parsed %d divisions", provinceCode, len(results))
	return results, nil
}

func discoverProvincialVoteLinks(doc *goquery.Document, indexURL string) []string {
	return discoverProvincialVoteLinksWithMatcher(doc, indexURL, genericVotesLinkRe)
}

func discoverProvincialVoteLinksWithMatcher(doc *goquery.Document, indexURL string, matcher *regexp.Regexp) []string {
	if matcher == nil {
		matcher = genericVotesLinkRe
	}
	seen := make(map[string]bool)
	links := make([]string, 0)

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if href == "" {
			return
		}
		text := a.Text() + " " + href
		if !matcher.MatchString(text) {
			return
		}
		full := resolveRelativeURL(indexURL, href)
		if seen[full] {
			return
		}
		seen[full] = true
		links = append(links, full)
	})

	sort.Strings(links)
	// Keep the most recent slice for speed/safety on very large archives.
	if len(links) > 40 {
		links = links[len(links)-40:]
	}
	return links
}

func parseGenericProvincialVotesDoc(doc *goquery.Document, provinceCode, chamber string, legislature, session int, fallbackDate string) []ProvincialDivisionResult {
	results := make([]ProvincialDivisionResult, 0)
	divNum := 0

	seenFingerprint := make(map[string]bool)
	doc.Find("table, section, article, div").Each(func(_ int, node *goquery.Selection) {
		text := strings.Join(strings.Fields(strings.ReplaceAll(node.Text(), "\u00a0", " ")), " ")
		if text == "" {
			return
		}
		if !genericYeaRe.MatchString(text) || !genericNayRe.MatchString(text) {
			return
		}

		fingerprint := text
		if len(fingerprint) > 200 {
			fingerprint = fingerprint[:200]
		}
		if seenFingerprint[fingerprint] {
			return
		}
		seenFingerprint[fingerprint] = true

		yeas := firstCount(genericYeaRe, text)
		nays := firstCount(genericNayRe, text)
		if yeas == 0 && nays == 0 {
			return
		}

		divNum++
		date := fallbackDate
		if d := utils.FindDateInText(text); d != "" {
			date = d
		}
		if date == "" {
			date = utils.TodayISO()
		}

		desc := strings.TrimSpace(node.PrevAll().Filter("h1,h2,h3,h4,h5,p").First().Text())
		if desc == "" {
			desc = text
			if len(desc) > 200 {
				desc = desc[:200]
			}
		}

		result := "Carried"
		if nays > yeas {
			result = "Negatived"
		}

		divID := ProvincialDivisionID(provinceCode, legislature, session, divNum, date)
		results = append(results, ProvincialDivisionResult{
			Division: DivisionStub{
				ID:          divID,
				Parliament:  legislature,
				Session:     session,
				Number:      divNum,
				Date:        date,
				Description: desc,
				Yeas:        yeas,
				Nays:        nays,
				Result:      result,
				Chamber:     chamber,
				LastScraped: utils.NowISO(),
			},
			Votes: parseGenericProvincialMemberVotes(node, divID),
		})
	})

	return results
}

func parseGenericProvincialMemberVotes(node *goquery.Selection, divisionID string) []ProvincialMemberVote {
	results := make([]ProvincialMemberVote, 0)
	seen := make(map[string]bool)

	node.Find("li, td, p").Each(func(_ int, s *goquery.Selection) {
		name := strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(s.Text(), "\u00a0", " ")), " "))
		if name == "" || len(name) < 3 {
			return
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "YEA") || strings.Contains(upper, "NAY") || strings.Contains(upper, "POUR") || strings.Contains(upper, "CONTRE") {
			return
		}

		context := strings.ToUpper(strings.Join(strings.Fields(strings.ReplaceAll(s.Parent().Text(), "\u00a0", " ")), " "))
		vote := ""
		switch {
		case strings.Contains(context, "YEA"), strings.Contains(context, "AYE"), strings.Contains(context, "POUR"):
			vote = "Yea"
		case strings.Contains(context, "NAY"), strings.Contains(context, "CONTRE"):
			vote = "Nay"
		default:
			return
		}

		key := vote + "|" + strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		results = append(results, ProvincialMemberVote{DivisionID: divisionID, MemberName: name, Vote: vote})
	})

	return results
}

func firstCount(re *regexp.Regexp, text string) int {
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func extractDateFromURL(rawURL string) string {
	m := isoDateFromURLRe.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return ""
	}
	raw := m[1]
	if len(raw) == 8 && raw[4] != '-' {
		return fmt.Sprintf("%s-%s-%s", raw[:4], raw[4:6], raw[6:8])
	}
	return raw
}
