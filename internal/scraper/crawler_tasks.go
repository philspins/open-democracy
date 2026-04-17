package scraper

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/utils"
)

// ProvincialSource defines one province-specific crawl configuration.
type ProvincialSource struct {
	Code     string
	Province string
	Chamber  string
	BillsURL string
	VotesURL string
	Special  string // "on" | "sk" | ""
}

// BillSummaryEnqueue is an optional callback used to feed crawled provincial
// bills into the AI summarization pipeline without creating package cycles.
type BillSummaryEnqueue func(billID, billTitle, fullTextURL, lastActivityDate string)

type provincialCrawlStats struct {
	BillsSeen            int
	BillsUpserted        int
	DivisionsSeen        int
	DivisionsUpserted    int
	MemberVotesSeen      int
	MemberVotesUpserted  int
	MemberVotesUnmatched int
	Errors               int
}

// ProvincialSources is the default source matrix used by CrawlProvincial.
var ProvincialSources = []ProvincialSource{
	{Code: "ab", Province: "Alberta", Chamber: "alberta", BillsURL: "https://www.assembly.ab.ca/assembly-business/bills/bill-status", VotesURL: "https://www.assembly.ab.ca/assembly-business/assembly-records/votes-and-proceedings"},
	{Code: "bc", Province: "British Columbia", Chamber: "british_columbia", BillsURL: "https://www.leg.bc.ca/parliamentary-business/bills-and-legislation", VotesURL: ""},
	{Code: "mb", Province: "Manitoba", Chamber: "manitoba", BillsURL: "https://web2.gov.mb.ca/bills/sess/index.php", VotesURL: "https://www.gov.mb.ca/legislature/business/votes_proceedings.html"},
	{Code: "nb", Province: "New Brunswick", Chamber: "new_brunswick", BillsURL: "https://www.legnb.ca/en/legislation/bills", VotesURL: "https://www.legnb.ca/en/house-business/journals"},
	{Code: "nl", Province: "Newfoundland and Labrador", Chamber: "newfoundland_labrador", BillsURL: "https://www.assembly.nl.ca/HouseBusiness/Bills/", VotesURL: "https://www.assembly.nl.ca/HouseBusiness/Journals/"},
	{Code: "ns", Province: "Nova Scotia", Chamber: "nova_scotia", BillsURL: "https://nslegislature.ca/legislative-business/bills-statutes/bills", VotesURL: "https://nslegislature.ca/legislative-business/journals"},
	{Code: "on", Province: "Ontario", Chamber: "ontario", BillsURL: "https://www.ola.org/en/legislative-business/bills/current", VotesURL: OntarioVPIndexURL, Special: "on"},
	{Code: "pe", Province: "Prince Edward Island", Chamber: "pei", BillsURL: "https://www.assembly.pe.ca/legislative-business/house-records/bills", VotesURL: "https://www.assembly.pe.ca/legislative-business/house-records/journals"},
	{Code: "qc", Province: "Quebec", Chamber: "quebec", BillsURL: "https://www.assnat.qc.ca/en/travaux-parlementaires/projets-loi/index.html", VotesURL: "https://www.assnat.qc.ca/en/travaux-parlementaires/registre-des-votes/index.html"},
	{Code: "sk", Province: "Saskatchewan", Chamber: "saskatchewan", BillsURL: "https://www.legassembly.sk.ca/legislative-business", VotesURL: SaskatchewanArchiveURL, Special: "sk"},
}

// CrawlBills runs federal bill crawl + detail enrichment + optional summary enqueue.
func CrawlBills(conn *sql.DB, client *http.Client, delay time.Duration, rssURL string, enqueueSummary BillSummaryEnqueue) error {
	stubs, err := CrawlBillsRSS(rssURL, client)
	if err != nil {
		return err
	}
	for _, stub := range stubs {
		detail, err := CrawlBillDetail(stub.ID, stub.LegisInfoURL, client)
		if err != nil {
			log.Printf("[bills] detail error for %s: %v", stub.ID, err)
		}
		time.Sleep(delay)

		parl, sess, ok := utils.ParliamentSessionFromBillID(stub.ID)
		var lopSummary string
		if ok {
			lopSummary = CrawlLibraryOfParliamentSummary(
				utils.BillNumberFromID(stub.ID), parl, sess, client,
			)
			time.Sleep(delay)
		}

		bill := db.Bill{
			ID:               stub.ID,
			Parliament:       parl,
			Session:          sess,
			Number:           utils.BillNumberFromID(stub.ID),
			Title:            stub.Title,
			Chamber:          utils.BillChamber(utils.BillNumberFromID(stub.ID)),
			LegisInfoURL:     stub.LegisInfoURL,
			LastActivityDate: stub.LastActivityDate,
			CurrentStage:     detail.CurrentStage,
			CurrentStatus:    detail.CurrentStatus,
			SponsorID:        detail.SponsorID,
			BillType:         detail.BillType,
			FullTextURL:      detail.FullTextURL,
			IntroducedDate:   detail.IntroducedDate,
			SummaryLoP:       lopSummary,
			LastScraped:      utils.NowISO(),
		}
		if !ok {
			bill.Parliament = 0
			bill.Session = 0
		}
		if err := db.UpsertBill(conn, bill); err != nil {
			log.Printf("[bills] upsert %s: %v", stub.ID, err)
		}
		for _, stage := range detail.Stages {
			db.UpsertBillStage(conn, db.BillStage{
				BillID:  stub.ID,
				Stage:   stage.Stage,
				Chamber: stage.Chamber,
				Date:    stage.Date,
			})
		}

		if enqueueSummary != nil && strings.TrimSpace(bill.FullTextURL) != "" {
			enqueueSummary(bill.ID, bill.Title, bill.FullTextURL, bill.LastActivityDate)
		}
	}
	return nil
}

// CrawlMembers fetches federal + provincial members and upserts to DB.
func CrawlMembers(conn *sql.DB, client *http.Client, delay time.Duration, apiURL string) error {
	profiles, err := CrawlMembersFromAPI(apiURL, client)
	if err != nil {
		return err
	}
	db.UpsertProfiles(conn, toDBMembers(profiles), delay)

	setSlugsSorted := make([]string, 0, len(ProvincialLegislatureAPIs))
	for slug := range ProvincialLegislatureAPIs {
		setSlugsSorted = append(setSlugsSorted, slug)
	}
	sort.Strings(setSlugsSorted)
	for _, setSlug := range setSlugsSorted {
		provProfiles, perr := CrawlProvincialMembersFromAPI(setSlug, "", client)
		if perr != nil {
			log.Printf("[members] provincial set %s: %v", setSlug, perr)
			continue
		}
		// The Represent API for nb-legislature currently returns 0 members.
		// Fall back to scraping the NB legislature website directly.
		if len(provProfiles) == 0 && setSlug == "nb-legislature" {
			log.Printf("[members] nb-legislature: Represent API returned 0 members; falling back to NB website scraper")
			nbProfiles, nberr := CrawlNewBrunswickMembersFromWebsite("", client)
			if nberr != nil {
				log.Printf("[members] nb-legislature website fallback: %v", nberr)
			} else {
				provProfiles = nbProfiles
			}
		}
		db.UpsertProfiles(conn, toDBMembers(provProfiles), delay)
	}
	return nil
}

func toDBMembers(profiles []MemberProfile) []db.Member {
	out := make([]db.Member, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, db.Member{
			ID:              profile.ID,
			Name:            profile.Name,
			Party:           profile.Party,
			Riding:          profile.Riding,
			Province:        profile.Province,
			Role:            profile.Role,
			PhotoURL:        profile.PhotoURL,
			Email:           profile.Email,
			Website:         profile.Website,
			Chamber:         profile.Chamber,
			Active:          profile.Active,
			LastScraped:     profile.LastScraped,
			GovernmentLevel: profile.GovernmentLevel,
		})
	}
	return out
}

// CrawlCalendar fetches sitting dates and upserts them for the current parliament/session.
func CrawlCalendar(conn *sql.DB, client *http.Client, _ time.Duration, sourceURL string) error {
	dates, err := CrawlSittingCalendar(sourceURL, client)
	if err != nil {
		return err
	}
	for _, d := range dates {
		if err := db.UpsertSittingDate(conn, CurrentParliament, CurrentSession, d); err != nil {
			log.Printf("[calendar] upsert %s: %v", d, err)
		}
	}
	return nil
}

// CrawlVotes indexes commons votes and fills detail rows when needed.
func CrawlVotes(conn *sql.DB, client *http.Client, delay time.Duration, indexURL string) error {
	divs, err := CrawlVotesIndex(indexURL, CurrentParliament, CurrentSession, client)
	if err != nil {
		return err
	}
	for _, div := range divs {
		existed, err := db.DivisionExists(conn, div.ID)
		if err != nil {
			log.Printf("[votes] exists check %s: %v", div.ID, err)
		}

		if err := db.UpsertDivision(conn, db.Division{
			ID:          div.ID,
			Parliament:  div.Parliament,
			Session:     div.Session,
			Number:      div.Number,
			Date:        div.Date,
			BillID:      utils.BillIDFromParts(div.Parliament, div.Session, div.BillNumber),
			Description: div.Description,
			Yeas:        div.Yeas,
			Nays:        div.Nays,
			Paired:      div.Paired,
			Result:      div.Result,
			Chamber:     div.Chamber,
			LastScraped: div.LastScraped,
		}); err != nil {
			log.Printf("[votes] upsert %s: %v", div.ID, err)
		}

		needsDetail := !existed
		if existed && div.DetailURL != "" {
			hasVotes, err := db.DivisionHasVotes(conn, div.ID)
			if err != nil {
				log.Printf("[votes] has-votes check %s: %v", div.ID, err)
			}
			needsDetail = !hasVotes
		}
		if needsDetail && div.DetailURL != "" {
			votes, err := CrawlDivisionDetail(div.ID, div.DetailURL, client)
			if err != nil {
				log.Printf("[votes] detail error %s: %v", div.ID, err)
			}
			for _, v := range votes {
				db.UpsertMemberVote(conn, v.DivisionID, v.MemberID, v.Vote)
			}
			time.Sleep(delay)
		}
	}
	return nil
}

// CrawlSenate indexes senate votes and fills detail rows when needed.
func CrawlSenate(conn *sql.DB, client *http.Client, delay time.Duration, indexURL string) error {
	divs, err := CrawlSenateVotesIndex(indexURL, CurrentParliament, CurrentSession, client)
	if err != nil {
		return err
	}
	for _, div := range divs {
		existed, err := db.DivisionExists(conn, div.ID)
		if err != nil {
			log.Printf("[senate] exists check %s: %v", div.ID, err)
		}

		if err := db.UpsertDivision(conn, db.Division{
			ID:          div.ID,
			Parliament:  div.Parliament,
			Session:     div.Session,
			Number:      div.Number,
			Date:        div.Date,
			BillID:      utils.BillIDFromParts(div.Parliament, div.Session, div.BillNumber),
			Description: div.Description,
			Yeas:        div.Yeas,
			Nays:        div.Nays,
			Paired:      div.Paired,
			Result:      div.Result,
			Chamber:     div.Chamber,
			LastScraped: div.LastScraped,
		}); err != nil {
			log.Printf("[senate] upsert %s: %v", div.ID, err)
		}

		needsDetail := !existed
		if existed && div.DetailURL != "" {
			hasVotes, err := db.DivisionHasVotes(conn, div.ID)
			if err != nil {
				log.Printf("[senate] has-votes check %s: %v", div.ID, err)
			}
			needsDetail = !hasVotes
		}
		if needsDetail && div.DetailURL != "" {
			votes, err := CrawlSenateDivisionDetail(div.ID, div.DetailURL, client)
			if err != nil {
				log.Printf("[senate] detail error %s: %v", div.ID, err)
			}
			for _, v := range votes {
				db.UpsertMemberVote(conn, v.DivisionID, v.MemberID, v.Vote)
			}
			time.Sleep(delay)
		}
	}
	return nil
}

// CrawlProvincial runs all configured provincial crawlers with bounded concurrency.
func CrawlProvincial(conn *sql.DB, client *http.Client, delay time.Duration, parallelism int, enqueueSummary BillSummaryEnqueue) error {
	fns := make([]func(), 0, len(ProvincialSources))
	for _, src := range ProvincialSources {
		src := src
		fns = append(fns, func() {
			if err := CrawlProvinceSource(conn, client, delay, src, enqueueSummary); err != nil {
				log.Printf("[provincial] %s: %v", src.Code, err)
			}
		})
	}
	RunParallel(parallelism, fns)
	return nil
}

// crawlBillsForSource dispatches to the correct province-specific bill crawler.
func crawlBillsForSource(src ProvincialSource, legislature, session int, client *http.Client) ([]ProvincialBillStub, error) {
	switch src.Code {
	case "ab":
		return CrawlAlbertaBills(src.BillsURL, legislature, session, client)
	case "bc":
		return CrawlBritishColumbiaBills(src.BillsURL, legislature, session, client)
	case "mb":
		return CrawlManitobaBills(src.BillsURL, legislature, session, client)
	case "nb":
		return CrawlNewBrunswickBills(src.BillsURL, legislature, session, client)
	case "nl":
		return CrawlNewfoundlandAndLabradorBills(src.BillsURL, legislature, session, client)
	case "ns":
		return CrawlNovaScotiaBills(src.BillsURL, legislature, session, client)
	case "on":
		return CrawlOntarioBills(src.BillsURL, legislature, session, client)
	case "pe":
		// Pass nil so the callee creates a browser-header client to bypass
		// the Radware bot-manager on assembly.pe.ca.
		return CrawlPrinceEdwardIslandBills(src.BillsURL, legislature, session, nil)
	case "qc":
		return CrawlQuebecBills(src.BillsURL, legislature, session, client)
	case "sk":
		return CrawlSaskatchewanBills(src.BillsURL, legislature, session, client)
	default:
		return CrawlProvincialBillsFromIndex(src.BillsURL, src.Code, legislature, session, src.Chamber, client)
	}
}

// crawlDivisionsForSource dispatches to the correct province-specific votes crawler
// for all non-special provinces (i.e. excluding ON and SK which use their own multi-step
// logic in CrawlProvinceSource).
func crawlDivisionsForSource(src ProvincialSource, legislature, session int, client *http.Client) ([]ProvincialDivisionResult, error) {
	switch src.Code {
	case "ab":
		return CrawlAlbertaVotes(src.VotesURL, legislature, session, client)
	case "bc":
		return CrawlBritishColumbiaVotes(src.VotesURL, legislature, session, client)
	case "mb":
		return CrawlManitobaVotes(src.VotesURL, legislature, session, client)
	case "nb":
		return CrawlNewBrunswickVotes(src.VotesURL, legislature, session, client)
	case "nl":
		return CrawlNewfoundlandAndLabradorVotes(src.VotesURL, legislature, session, client)
	case "ns":
		return CrawlNovaScotiaVotes(src.VotesURL, legislature, session, client)
	case "pe":
		// Pass nil so the callee creates a browser-header client to bypass
		// the Radware bot-manager CAPTCHA on assembly.pe.ca.
		return CrawlPrinceEdwardIslandVotes(src.VotesURL, legislature, session, nil)
	case "qc":
		return CrawlQuebecVotes(src.VotesURL, legislature, session, client)
	default:
		return CrawlGenericProvincialVotes(src.VotesURL, src.Code, src.Chamber, legislature, session, client)
	}
}

// CrawlProvinceSource crawls bills and votes for one province source and upserts
// normalized records into bills/divisions/member_votes tables.
func CrawlProvinceSource(conn *sql.DB, client *http.Client, delay time.Duration, src ProvincialSource, enqueueSummary BillSummaryEnqueue) error {
	log.Printf("[provincial] crawling %s", src.Province)
	legislature, session := resolveProvincialLegislatureSession(conn, src, client)
	log.Printf("[provincial][%s] detected legislature/session: %d/%d", src.Code, legislature, session)

	stats := provincialCrawlStats{}
	defer func() {
		if stats.MemberVotesSeen > 0 && stats.MemberVotesUpserted == 0 && stats.MemberVotesUnmatched == stats.MemberVotesSeen {
			var memberCount int
			_ = conn.QueryRow(
				`SELECT COUNT(1) FROM members WHERE government_level='provincial' AND lower(province)=lower(?)`,
				src.Province).Scan(&memberCount)
			if memberCount == 0 {
				log.Printf("[provincial][%s] hint: 0 provincial members in DB for %q — run --members first to enable vote matching", src.Code, src.Province)
			}
		}
		log.Printf("[provincial][%s] summary bills=%d/%d divisions=%d/%d votes=%d/%d unmatched=%d errors=%d",
			src.Code,
			stats.BillsUpserted, stats.BillsSeen,
			stats.DivisionsUpserted, stats.DivisionsSeen,
			stats.MemberVotesUpserted, stats.MemberVotesSeen,
			stats.MemberVotesUnmatched,
			stats.Errors,
		)
	}()

	var (
		bills []ProvincialBillStub
		berr  error
	)
	bills, berr = crawlBillsForSource(src, legislature, session, client)
	// If the current session returned no bills and the DB has no bills at all for
	// this province (fresh install or brand-new session), retry with the previous
	// session so the database is seeded with the most recent available data.
	if berr == nil && len(bills) == 0 && session > 1 && provinceBillCountInDB(conn, src.Code) == 0 {
		log.Printf("[provincial][%s] 0 bills for session %d; retrying with previous session %d to seed DB", src.Code, session, session-1)
		bills, berr = crawlBillsForSource(src, legislature, session-1, client)
		if berr == nil && len(bills) > 0 {
			session = session - 1
		}
	}
	if berr != nil {
		stats.Errors++
		log.Printf("[provincial] %s bills error: %v", src.Code, berr)
	} else {
		stats.BillsSeen = len(bills)
		for _, b := range bills {
			if err := db.UpsertBill(conn, db.Bill{
				ID:               b.ID,
				Parliament:       b.Parliament,
				Session:          b.Session,
				Number:           b.Number,
				Title:            b.Title,
				Chamber:          b.Chamber,
				LegisInfoURL:     b.DetailURL,
				FullTextURL:      b.DetailURL,
				LastActivityDate: b.LastActivityDate,
				LastScraped:      b.LastScraped,
			}); err != nil {
				stats.Errors++
				log.Printf("[provincial] %s bill upsert %s: %v", src.Code, b.ID, err)
			} else {
				stats.BillsUpserted++
				if enqueueSummary != nil && strings.TrimSpace(b.DetailURL) != "" {
					enqueueSummary(b.ID, b.Title, b.DetailURL, b.LastActivityDate)
				}
			}
			time.Sleep(delay)
		}
	}

	var divs []ProvincialDivisionResult
	switch src.Special {
	case "on":
		dates, err := CrawlOntarioVPSittingDates(src.VotesURL, legislature, session, client)
		if err != nil {
			stats.Errors++
			return fmt.Errorf("ontario dates: %w", err)
		}
		for _, d := range dates {
			dayURL := OntarioVPDayURL(legislature, session, d)
			dayDivs, derr := CrawlOntarioVPDay(dayURL, legislature, session, d, client)
			if derr != nil {
				if strings.Contains(derr.Error(), "status 404") {
					log.Printf("[provincial] on day %s: no votes-proceedings page; skipping", d)
					continue
				}
				stats.Errors++
				log.Printf("[provincial] on day %s: %v", d, derr)
				continue
			}
			divs = append(divs, dayDivs...)
			time.Sleep(delay)
		}
	case "sk":
		links, err := CrawlSaskatchewanMinutesLinks(src.VotesURL, client)
		if err != nil {
			// The SK archive URL may be temporarily unavailable or the site has changed.
			// Log the error and continue with 0 divisions rather than aborting.
			stats.Errors++
			log.Printf("[provincial] sk: cannot discover minutes links (archive URL may have changed): %v", err)
			break
		}
		for _, link := range links {
			dayDivs, derr := CrawlSaskatchewanMinutes(link, legislature, session, client)
			if derr != nil {
				stats.Errors++
				log.Printf("[provincial] sk minutes %s: %v", link, derr)
				continue
			}
			divs = append(divs, dayDivs...)
			time.Sleep(delay)
		}
	default:
		var (
			parsed []ProvincialDivisionResult
			err    error
		)
		parsed, err = crawlDivisionsForSource(src, legislature, session, client)
		if err != nil {
			stats.Errors++
			return err
		}
		// If no divisions were found for the current session and the DB has no
		// divisions at all for this province, retry with the previous session to
		// ensure the database is seeded with the most recent available data.
		if len(parsed) == 0 && session > 1 && provinceDivisionCountInDB(conn, src.Code) == 0 {
			log.Printf("[provincial][%s] 0 divisions for session %d; retrying with previous session %d to seed DB", src.Code, session, session-1)
			prevParsed, prevErr := crawlDivisionsForSource(src, legislature, session-1, client)
			if prevErr == nil && len(prevParsed) > 0 {
				parsed = prevParsed
			}
		}
		divs = parsed
	}
	stats.DivisionsSeen = len(divs)
	if count := provinceMemberCountInDB(conn, src.Province); stats.DivisionsSeen > 0 && count < 10 {
		if err := ensureProvincialMembersForSource(conn, client, delay, src); err != nil {
			log.Printf("[provincial][%s] member seed: %v", src.Code, err)
		}
	}

	for _, res := range divs {
		billID := provincialBillIDFromDescription(conn, src.Code, legislature, session, res.Division.Description)
		if err := db.UpsertDivision(conn, db.Division{
			ID:          res.Division.ID,
			Parliament:  res.Division.Parliament,
			Session:     res.Division.Session,
			Number:      res.Division.Number,
			Date:        res.Division.Date,
			BillID:      billID,
			Description: res.Division.Description,
			Yeas:        res.Division.Yeas,
			Nays:        res.Division.Nays,
			Paired:      res.Division.Paired,
			Result:      res.Division.Result,
			Chamber:     res.Division.Chamber,
			SittingURL:  res.Division.DetailURL,
			LastScraped: res.Division.LastScraped,
		}); err != nil {
			stats.Errors++
			log.Printf("[provincial] %s division upsert %s: %v", src.Code, res.Division.ID, err)
		} else {
			stats.DivisionsUpserted++
		}

		stats.MemberVotesSeen += len(res.Votes)
		for _, mv := range res.Votes {
			memberID, merr := resolveProvincialMemberID(conn, src.Province, mv.MemberName)
			if merr != nil {
				stats.Errors++
				continue
			}
			if memberID == "" {
				stats.MemberVotesUnmatched++
				continue
			}
			if err := db.UpsertMemberVote(conn, res.Division.ID, memberID, mv.Vote); err != nil {
				stats.Errors++
				log.Printf("[provincial] %s vote upsert %s/%s: %v", src.Code, res.Division.ID, memberID, err)
			} else {
				stats.MemberVotesUpserted++
			}
		}
		time.Sleep(delay)
	}

	return nil
}

type legislatureSession struct {
	Legislature int
	Session     int
	Score       int
}

var parliamentSessionRe = regexp.MustCompile(`(?i)(\d{1,3})(?:st|nd|rd|th)\s*parliament[^\d]{0,40}(\d{1,2})(?:st|nd|rd|th)\s*session`)
var legislatureSessionRe = regexp.MustCompile(`(?i)(\d{1,3})(?:st|nd|rd|th)\s*(?:legislature|general assembly)[^\d]{0,40}(\d{1,2})(?:st|nd|rd|th)?\s*session`)
var parliamentSessionURLRe = regexp.MustCompile(`(?i)(\d{1,3})(?:st|nd|rd|th)?[-_/]parliament[-_/](\d{1,2})(?:st|nd|rd|th)?[-_/]session`)
var compactLegSessionURLRe = regexp.MustCompile(`(?i)/(\d{1,3})-(\d{1,2})(?:/|$)`) // e.g. /43-2/

func candidateScore(text string, base int) int {
	score := base
	lower := strings.ToLower(text)
	if strings.Contains(lower, "current") || strings.Contains(lower, "overview") || strings.Contains(lower, "active") {
		score += 20
	}
	if strings.Contains(lower, "latest") || strings.Contains(lower, "today") {
		score += 10
	}
	if strings.Contains(lower, "archive") || strings.Contains(lower, "archives") || strings.Contains(lower, "historical") {
		score -= 30
	}
	if strings.Contains(lower, "journal indices") || strings.Contains(lower, "appendices") {
		score -= 20
	}
	return score
}

func resolveProvincialLegislatureSession(conn *sql.DB, src ProvincialSource, client *http.Client) (int, int) {
	if client == nil {
		client = http.DefaultClient
	}

	candidates := make([]legislatureSession, 0)
	for _, u := range []string{src.BillsURL, src.VotesURL} {
		if u == "" {
			continue
		}
		candidates = append(candidates, extractLegislatureSessionCandidates(src.Code, u, 70)...)
		doc, err := fetchDoc(u, client)
		if err != nil {
			continue
		}
		doc.Find("title, h1, h2, h3, h4, h5").Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text == "" {
				return
			}
			candidates = append(candidates, extractLegislatureSessionCandidates(src.Code, text, 90)...)
		})
		doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
			href, _ := s.Attr("href")
			text := strings.TrimSpace(s.Text())
			snippet := strings.TrimSpace(text + " " + href)
			lower := strings.ToLower(snippet)
			if !strings.Contains(lower, "parliament") && !strings.Contains(lower, "legislature") && !strings.Contains(lower, "session") && !strings.Contains(lower, "assembly") {
				return
			}
			if href, ok := s.Attr("href"); ok {
				text += " " + href
			}
			candidates = append(candidates, extractLegislatureSessionCandidates(src.Code, text, 55)...)
		})
	}

	if best, ok := maxLegislatureSession(candidates); ok {
		return best.Legislature, best.Session
	}

	if l, s, ok := latestLegislatureSessionFromDB(conn, src.Code); ok {
		return l, s
	}

	switch src.Special {
	case "on":
		return OntarioParliament, OntarioSession
	case "sk":
		return SaskatchewanLegislature, SaskatchewanSession
	default:
		return 1, 1
	}
}

func extractLegislatureSessionCandidates(provinceCode, text string, baseScore int) []legislatureSession {
	out := make([]legislatureSession, 0)
	for _, re := range []*regexp.Regexp{parliamentSessionRe, legislatureSessionRe, parliamentSessionURLRe} {
		matches := re.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			l, lerr := strconv.Atoi(m[1])
			s, serr := strconv.Atoi(m[2])
			if lerr != nil || serr != nil || l <= 0 || s <= 0 {
				continue
			}
			out = append(out, legislatureSession{Legislature: l, Session: s, Score: candidateScore(text, baseScore)})
		}
	}

	// Quebec commonly exposes session paths as /43-2/ under travaux-parlementaires.
	if provinceCode == "qc" {
		matches := compactLegSessionURLRe.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			l, lerr := strconv.Atoi(m[1])
			s, serr := strconv.Atoi(m[2])
			if lerr != nil || serr != nil || l <= 0 || s <= 0 {
				continue
			}
			if l > 99 || s > 9 {
				continue
			}
			out = append(out, legislatureSession{Legislature: l, Session: s, Score: candidateScore(text, baseScore+15)})
		}
	}
	return out
}

func maxLegislatureSession(candidates []legislatureSession) (legislatureSession, bool) {
	if len(candidates) == 0 {
		return legislatureSession{}, false
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.Score > best.Score ||
			(c.Score == best.Score && c.Legislature > best.Legislature) ||
			(c.Score == best.Score && c.Legislature == best.Legislature && c.Session > best.Session) {
			best = c
		}
	}
	return best, true
}

func latestLegislatureSessionFromDB(conn *sql.DB, provinceCode string) (int, int, bool) {
	if conn == nil {
		return 0, 0, false
	}
	var legislature, session int
	q := `SELECT COALESCE(MAX(parliament),0), COALESCE(MAX(session),0) FROM bills WHERE id LIKE ?`
	if err := conn.QueryRow(q, provinceCode+"-%").Scan(&legislature, &session); err != nil {
		return 0, 0, false
	}
	if legislature <= 0 || session <= 0 {
		return 0, 0, false
	}
	return legislature, session, true
}

// provinceBillCountInDB returns the number of bills in the DB for the given province code prefix.
func provinceBillCountInDB(conn *sql.DB, provinceCode string) int {
	if conn == nil {
		return 0
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(1) FROM bills WHERE id LIKE ?`, provinceCode+"-%").Scan(&n)
	return n
}

// provinceDivisionCountInDB returns the number of divisions in the DB for the given province code prefix.
func provinceDivisionCountInDB(conn *sql.DB, provinceCode string) int {
	if conn == nil {
		return 0
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(1) FROM divisions WHERE id LIKE ?`, provinceCode+"-%").Scan(&n)
	return n
}

func provinceMemberCountInDB(conn *sql.DB, province string) int {
	if conn == nil {
		return 0
	}
	var n int
	_ = conn.QueryRow(
		`SELECT COUNT(1) FROM members WHERE government_level='provincial' AND lower(province)=lower(?)`,
		province,
	).Scan(&n)
	return n
}

func provincialSetSlugForCode(code string) string {
	switch code {
	case "ab":
		return "alberta-legislature"
	case "bc":
		return "bc-legislature"
	case "mb":
		return "manitoba-legislature"
	case "nb":
		return "nb-legislature"
	case "nl":
		return "newfoundland-labrador-legislature"
	case "ns":
		return "nova-scotia-legislature"
	case "on":
		return "ontario-legislature"
	case "pe":
		return "pei-legislature"
	case "qc":
		return "quebec-assemblee-nationale"
	case "sk":
		return "saskatchewan-legislature"
	default:
		return ""
	}
}

func ensureProvincialMembersForSource(conn *sql.DB, client *http.Client, delay time.Duration, src ProvincialSource) error {
	if conn == nil {
		return nil
	}
	setSlug := provincialSetSlugForCode(src.Code)
	if setSlug == "" {
		return nil
	}
	profiles, err := CrawlProvincialMembersFromAPI(setSlug, "", client)
	if err != nil {
		return err
	}
	if len(profiles) == 0 && setSlug == "nb-legislature" {
		profiles, err = CrawlNewBrunswickMembersFromWebsite("", client)
		if err != nil {
			return err
		}
	}
	if len(profiles) == 0 {
		return nil
	}
	db.UpsertProfiles(conn, toDBMembers(profiles), delay)
	return nil
}

func provincialBillIDFromDescription(conn *sql.DB, provinceCode string, legislature, session int, description string) string {
	billNumber := ExtractProvincialBillNumber(description)
	if billNumber == "" {
		return ""
	}
	billID := ProvincialBillID(provinceCode, legislature, session, billNumber)
	if billID == "" {
		return ""
	}
	var count int
	if err := conn.QueryRow(`SELECT COUNT(1) FROM bills WHERE id = ?`, billID).Scan(&count); err != nil {
		return ""
	}
	if count == 0 {
		return ""
	}
	return billID
}

func normalisePersonName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func resolveProvincialMemberID(conn *sql.DB, province, sourceName string) (string, error) {
	want := normalisePersonName(sourceName)
	if want == "" {
		return "", nil
	}

	rows, err := conn.Query(`
		SELECT id, name FROM members
		WHERE government_level = 'provincial' AND lower(province) = lower(?)`, province)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type candidate struct {
		ID   string
		Name string
	}
	list := make([]candidate, 0)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			continue
		}
		list = append(list, c)
	}

	for _, c := range list {
		if normalisePersonName(c.Name) == want {
			return c.ID, nil
		}
	}

	// Ontario and some journals list only the surname in vote lists.
	parts := strings.Fields(want)
	if len(parts) == 1 {
		last := parts[0]
		for _, c := range list {
			nameParts := strings.Fields(normalisePersonName(c.Name))
			if len(nameParts) > 0 && nameParts[len(nameParts)-1] == last {
				return c.ID, nil
			}
		}
	}

	return "", nil
}

// DefaultParallelism reads CRAWLER_PARALLELISM and falls back to 5 when unset or invalid.
func DefaultParallelism() int {
	if v := os.Getenv("CRAWLER_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 5
}

// RunParallel executes each function in fns in its own goroutine, bounded by parallelism.
func RunParallel(parallelism int, fns []func()) {
	if parallelism < 1 {
		parallelism = 1
	}
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn()
		}()
	}
	wg.Wait()
}
