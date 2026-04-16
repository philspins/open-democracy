// Provincial votes scrapers: Ontario Votes & Proceedings and Saskatchewan Assembly Minutes.
package scraper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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
		href := normalizeNewBrunswickHref(a.AttrOr("href", ""))
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
		href := normalizeNewBrunswickHref(a.AttrOr("href", ""))
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

func normalizeNewBrunswickHref(href string) string {
	href = strings.TrimSpace(href)
	href = strings.ReplaceAll(href, `\`, "/")
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

func crawlNewBrunswickJournalPDF(pdfURL string, legislature, session, startDivisionNumber int, client *http.Client) ([]ProvincialDivisionResult, int, error) {
	resp, err := client.Get(pdfURL)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %q: %w", pdfURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("GET %q: status %d - %s", pdfURL, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	// Keep only one temp PDF on disk at a time and remove it immediately after parsing.
	tmp, err := os.CreateTemp("", "open-democracy-nb-*.pdf")
	if err != nil {
		return nil, 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, 0, err
	}
	if written >= 32<<20 {
		return nil, 0, fmt.Errorf("pdf too large (>32MB): %s", pdfURL)
	}
	if err := tmp.Sync(); err != nil {
		return nil, 0, err
	}

	text, err := extractNewBrunswickPDFText(tmpPath)
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

func extractNewBrunswickPDFText(pdfPath string) (string, error) {
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
var manitobaVotesLinkRe = regexp.MustCompile(`(?i)(recorded_votes|votes|journals?|hansard)`)
var newBrunswickVotesLinkRe = regexp.MustCompile(`(?i)(journals?(?:-e\.asp|/)|house-business/journals|votes|legis)`)
var newfoundlandVotesLinkRe = regexp.MustCompile(`(?i)(/business/votes|housebusiness|ga\d+session\d+|votes\.aspx|/votes(?:/|$))`)
var novaScotiaVotesLinkRe = regexp.MustCompile(`(?i)(journals?|proceedings|votes|hansard-debates)`)
var peiVotesLinkRe = regexp.MustCompile(`(?i)(legislative-business|votes|proceedings)`)

// CrawlAlbertaVotes crawls Alberta votes/proceedings pages.
func CrawlAlbertaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.ab.ca/assembly-business/assembly-records/votes-and-proceedings"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "ab", "alberta", legislature, session, client, albertaVotesLinkRe)
}

// CrawlBritishColumbiaVotes crawls BC votes/proceedings pages.
// NOTE: The leg.bc.ca votes-and-proceedings index page loads per-day vote links via
// JavaScript (Drupal/AJAX). The static HTML has no individual vote-day links, so the
// generic HTML scraper will always return 0 divisions. Fixing this requires either a
// headless-browser approach or discovering the LIMS (lims.leg.bc.ca) per-day HTML
// file pattern through a JS-capable client.
func CrawlBritishColumbiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.leg.bc.ca/parliamentary-business/overview/43rd-parliament/2nd-session/votes-and-proceedings"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "bc", "british_columbia", legislature, session, client, bcVotesLinkRe)
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

// CrawlManitobaVotes crawls Manitoba recorded votes/journal pages.
// NOTE: The votes_proceedings.html index page links to session-specific sub-pages
// (e.g. 43rd/43rd_3rd.html) whose URLs do not match manitobaVotesLinkRe, so those
// sub-pages are never discovered. Even if they were, the per-day votes are in PDFs
// (e.g. 3rd/votes_041.pdf) that the generic HTML parser cannot read. Fixing this
// requires a dedicated PDF scraper for the MB Votes and Proceedings format.
func CrawlManitobaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.gov.mb.ca/legislature/business/votes_proceedings.html"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "mb", "manitoba", legislature, session, client, manitobaVotesLinkRe)
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

// CrawlNewfoundlandAndLabradorVotes crawls NL votes pages.
// The journal index at /HouseBusiness/Journals/ links to per-GA session directories
// (e.g. ga50session2/) which in turn contain per-day PDF files (e.g. 26-04-14.pdf).
// Those PDFs use a two-column AYES/NAYS layout; the generic HTML parser will discover
// the session directories and PDF links but cannot parse vote totals from PDFs.
// Fixing this requires a dedicated PDF scraper similar to the NB implementation.
func CrawlNewfoundlandAndLabradorVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.nl.ca/HouseBusiness/Journals/"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "nl", "newfoundland_labrador", legislature, session, client, newfoundlandVotesLinkRe)
}

// CrawlNovaScotiaVotes crawls NS journals/proceedings pages.
// NOTE: The journals index at nslegislature.ca currently serves only PDFs from 2021
// (63rd Assembly, 3rd session). Current assembly data appears to be loaded dynamically
// via JavaScript. The generic HTML parser finds old PDF links but cannot parse them,
// so this function returns 0 divisions until the NS site exposes static HTML links for
// the current session or a dedicated PDF parser is implemented.
func CrawlNovaScotiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://nslegislature.ca/legislative-business/journals-votes-proceedings"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "ns", "nova_scotia", legislature, session, client, novaScotiaVotesLinkRe)
}

// CrawlPrinceEdwardIslandVotes crawls PEI votes/proceedings pages.
// NOTE: assembly.pe.ca is protected by Radware bot-manager. All automated HTTP requests
// receive a 302 redirect to a JavaScript CAPTCHA challenge, so no legislative content
// is accessible without a browser-level client. This function will always return 0
// divisions until PEI exposes a bot-accessible data source or an alternative API is used.
func CrawlPrinceEdwardIslandVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.pe.ca/legislative-business"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "pe", "pei", legislature, session, client, peiVotesLinkRe)
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
