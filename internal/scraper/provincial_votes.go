// Provincial votes scrapers: Ontario Votes & Proceedings and Saskatchewan Assembly Minutes.
package scraper

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/philspins/open-democracy/internal/utils"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	// Ontario
	OntarioVPIndexURL = "https://www.ola.org/en/legislative-business/house-documents/parliament-44/session-1"
	OntarioParliament = 44
	OntarioSession    = 1

	// Saskatchewan
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

	suffix := "/votes-proceedings"
	seen := make(map[string]bool)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if !strings.HasSuffix(href, suffix) {
			return
		}
		// Extract date from ".../parliament-N/session-N/YYYY-MM-DD/votes-proceedings"
		withoutSuffix := strings.TrimSuffix(href, suffix)
		if i := strings.LastIndex(withoutSuffix, "/"); i >= 0 {
			date := withoutSuffix[i+1:]
			if len(date) == 10 && date[4] == '-' && date[7] == '-' {
				seen[date] = true
			}
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

// ── Generic provincial votes parser (remaining provinces) ──────────────────

var genericVotesLinkRe = regexp.MustCompile(`(?i)(votes|proceedings|journal|journals|registre-votes|recorded_votes|minutes)`)
var genericYeaRe = regexp.MustCompile(`(?i)(?:yeas?|ayes?|pour)\D*(\d+)`)
var genericNayRe = regexp.MustCompile(`(?i)(?:nays?|contre)\D*(\d+)`)
var albertaVotesLinkRe = regexp.MustCompile(`(?i)(assembly-records/votes-and-proceedings|votes-and-proceedings|/votes(?:/|$))`)
var bcVotesLinkRe = regexp.MustCompile(`(?i)(votes-and-proceedings|journals?|/votes(?:/|$))`)
var quebecVotesLinkRe = regexp.MustCompile(`(?i)(registre-votes|votes-nominaux|votes\.html|votes-appels-nominaux|/votes(?:/|$))`)
var manitobaVotesLinkRe = regexp.MustCompile(`(?i)(recorded_votes|votes|journals?|hansard)`)
var newBrunswickVotesLinkRe = regexp.MustCompile(`(?i)(journals?-e\.asp|votes|legis)`)
var newfoundlandVotesLinkRe = regexp.MustCompile(`(?i)(/business/votes|housebusiness|votes\.aspx|/votes(?:/|$))`)
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
func CrawlBritishColumbiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.leg.bc.ca/parliamentary-business/overview/43rd-parliament/2nd-session/votes-and-proceedings"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "bc", "british_columbia", legislature, session, client, bcVotesLinkRe)
}

// CrawlQuebecVotes crawls Quebec registre/votes pages.
func CrawlQuebecVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assnat.qc.ca/en/travaux-parlementaires/registre-votes/registre-votes-resume.html"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "qc", "quebec", legislature, session, client, quebecVotesLinkRe)
}

// CrawlManitobaVotes crawls Manitoba recorded votes/journal pages.
func CrawlManitobaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.gov.mb.ca/legislature/house/recorded_votes.html"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "mb", "manitoba", legislature, session, client, manitobaVotesLinkRe)
}

// CrawlNewBrunswickVotes crawls NB journals/votes pages.
func CrawlNewBrunswickVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.gnb.ca/legis/1/hoa/e/journals-e.asp"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "nb", "new_brunswick", legislature, session, client, newBrunswickVotesLinkRe)
}

// CrawlNewfoundlandAndLabradorVotes crawls NL votes pages.
func CrawlNewfoundlandAndLabradorVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.nl.ca/business/votes"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "nl", "newfoundland_labrador", legislature, session, client, newfoundlandVotesLinkRe)
}

// CrawlNovaScotiaVotes crawls NS journals/proceedings pages.
func CrawlNovaScotiaVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://nslegislature.ca/legislative-business/journals-votes-proceedings"
	}
	return crawlGenericProvincialVotesWithMatcher(indexURL, "ns", "nova_scotia", legislature, session, client, novaScotiaVotesLinkRe)
}

// CrawlPrinceEdwardIslandVotes crawls PEI votes/proceedings pages.
func CrawlPrinceEdwardIslandVotes(indexURL string, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	if indexURL == "" {
		indexURL = "https://www.assembly.pe.ca/legislative-business/votes-and-proceedings"
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
