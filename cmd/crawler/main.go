// Command crawler is the CivicTracker data-crawling CLI.
//
// Usage:
//
//	crawler [flags]
//
// Flags:
//
//	--bills       Crawl bills only (LEGISinfo RSS + detail)
//	--votes       Crawl Commons votes only
//	--senate      Crawl Senate votes only
//	--members     Crawl MP profiles only
//	--calendar    Crawl sitting calendar only
//	--schedule    Run the APScheduler (blocks indefinitely)
//	--db PATH     Path to SQLite database file (default: civictracker.db)
//	--delay MS    Milliseconds between HTTP requests (default: 500)
//	-v            Verbose logging
//
// If no specific domain flag is provided, all crawlers run once.
package main

import (
	"database/sql"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/scheduler"
	"github.com/philspins/open-democracy/internal/utils"
)

func main() {
	billsFlag    := flag.Bool("bills", false, "Crawl bills only")
	votesFlag    := flag.Bool("votes", false, "Crawl Commons votes only")
	senateFlag   := flag.Bool("senate", false, "Crawl Senate votes only")
	membersFlag  := flag.Bool("members", false, "Crawl MP profiles only")
	calendarFlag := flag.Bool("calendar", false, "Crawl sitting calendar only")
	scheduleFlag := flag.Bool("schedule", false, "Run the APScheduler (blocks indefinitely)")
	dbPath       := flag.String("db", db.DefaultPath, "Path to SQLite database file")
	delayMS      := flag.Int("delay", 500, "Milliseconds between HTTP requests")
	verbose      := flag.Bool("v", false, "Verbose logging")
	flag.Parse()

	if !*verbose {
		log.SetFlags(log.LstdFlags)
	}

	conn, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("db.Open: %v", err)
	}
	defer conn.Close()

	delay := time.Duration(*delayMS) * time.Millisecond
	client := utils.NewHTTPClient()

	// ── Scheduler mode ───────────────────────────────────────────────────────
	if *scheduleFlag {
		scheduler.Start(scheduler.Config{
			DB: conn,
			FullCrawlFn: func(sdb *sql.DB) error {
				return runAll(sdb, client, delay)
			},
			FrequentVoteCheck: func(sdb *sql.DB) error {
				return runFrequentVoteCheck(sdb, client, delay, "")
			},
		})
		return // never reached
	}

	// ── One-shot mode ────────────────────────────────────────────────────────
	shouldRunAll := !(*billsFlag || *votesFlag || *senateFlag || *membersFlag || *calendarFlag)

	if *calendarFlag || shouldRunAll {
		if err := crawlCalendar(conn, client, delay, ""); err != nil {
			log.Printf("[main] calendar error: %v", err)
		}
	}
	if *billsFlag || shouldRunAll {
		if err := crawlBills(conn, client, delay, ""); err != nil {
			log.Printf("[main] bills error: %v", err)
		}
	}
	if *membersFlag || shouldRunAll {
		if err := crawlMembers(conn, client, delay, "", ""); err != nil {
			log.Printf("[main] members error: %v", err)
		}
	}
	if *votesFlag || shouldRunAll {
		if err := crawlVotes(conn, client, delay, ""); err != nil {
			log.Printf("[main] votes error: %v", err)
		}
	}
	if *senateFlag || shouldRunAll {
		if err := crawlSenate(conn, client, delay, ""); err != nil {
			log.Printf("[main] senate error: %v", err)
		}
	}

	log.Println("[main] done")
}

// ── domain crawlers ───────────────────────────────────────────────────────────
//
// Each function accepts an optional sourceURL parameter. When empty ("") the
// crawler falls back to the real government website URL. Passing a non-empty
// value injects a test server, making each function independently testable
// without hitting the network.

func crawlCalendar(conn *sql.DB, client *http.Client, delay time.Duration, sourceURL string) error {
	dates, err := scraper.CrawlSittingCalendar(sourceURL, client)
	if err != nil {
		return err
	}
	for _, d := range dates {
		if err := db.UpsertSittingDate(conn, scraper.CurrentParliament, scraper.CurrentSession, d); err != nil {
			log.Printf("[calendar] upsert %s: %v", d, err)
		}
	}
	return nil
}

func crawlBills(conn *sql.DB, client *http.Client, delay time.Duration, rssURL string) error {
	stubs, err := scraper.CrawlBillsRSS(rssURL, client)
	if err != nil {
		return err
	}
	for _, stub := range stubs {
		detail, err := scraper.CrawlBillDetail(stub.ID, stub.LegisInfoURL, client)
		if err != nil {
			log.Printf("[bills] detail error for %s: %v", stub.ID, err)
		}
		time.Sleep(delay)

		parl, sess, ok := utils.ParliamentSessionFromBillID(stub.ID)
		var lopSummary string
		if ok {
			lopSummary = scraper.CrawlLibraryOfParliamentSummary(
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
	}
	return nil
}

func crawlMembers(conn *sql.DB, client *http.Client, delay time.Duration, listURL, profileBaseURL string) error {
	stubs, err := scraper.CrawlMembersList(listURL, client)
	if err != nil {
		return err
	}
	for _, stub := range stubs {
		profileURL := profileBaseURL // use override if set; otherwise CrawlMemberProfile constructs the real URL
		profile, err := scraper.CrawlMemberProfile(stub.ID, profileURL, client)
		if err != nil {
			log.Printf("[members] profile error for %s: %v", stub.ID, err)
		}
		// Fill fallback fields from stub if profile is sparse
		if profile.Party == "" {
			profile.Party = stub.Party
		}
		if profile.Riding == "" {
			profile.Riding = stub.Riding
		}
		if profile.Province == "" {
			profile.Province = stub.Province
		}
		if err := db.UpsertMember(conn, db.Member{
			ID:          profile.ID,
			Name:        profile.Name,
			Party:       profile.Party,
			Riding:      profile.Riding,
			Province:    profile.Province,
			Role:        profile.Role,
			PhotoURL:    profile.PhotoURL,
			Email:       profile.Email,
			Website:     profile.Website,
			Chamber:     profile.Chamber,
			Active:      profile.Active,
			LastScraped: profile.LastScraped,
		}); err != nil {
			log.Printf("[members] upsert %s: %v", stub.ID, err)
		}
		time.Sleep(delay)
	}
	return nil
}

func crawlVotes(conn *sql.DB, client *http.Client, delay time.Duration, indexURL string) error {
	divs, err := scraper.CrawlVotesIndex(indexURL, scraper.CurrentParliament, scraper.CurrentSession, client)
	if err != nil {
		return err
	}
	for _, div := range divs {
		isNew, err := db.DivisionExists(conn, div.ID)
		if err != nil {
			log.Printf("[votes] exists check %s: %v", div.ID, err)
		}
		isNew = !isNew // DivisionExists returns true when it exists; we want isNew=true when it doesn't

		if err := db.UpsertDivision(conn, db.Division{
			ID:          div.ID,
			Parliament:  div.Parliament,
			Session:     div.Session,
			Number:      div.Number,
			Date:        div.Date,
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

		if isNew && div.DetailURL != "" {
			votes, err := scraper.CrawlDivisionDetail(div.ID, div.DetailURL, client)
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

func crawlSenate(conn *sql.DB, client *http.Client, delay time.Duration, indexURL string) error {
	divs, err := scraper.CrawlSenateVotesIndex(indexURL, scraper.CurrentParliament, scraper.CurrentSession, client)
	if err != nil {
		return err
	}
	for _, div := range divs {
		isNew, err := db.DivisionExists(conn, div.ID)
		if err != nil {
			log.Printf("[senate] exists check %s: %v", div.ID, err)
		}
		isNew = !isNew

		if err := db.UpsertDivision(conn, db.Division{
			ID:          div.ID,
			Parliament:  div.Parliament,
			Session:     div.Session,
			Number:      div.Number,
			Date:        div.Date,
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

		if isNew && div.DetailURL != "" {
			votes, err := scraper.CrawlSenateDivisionDetail(div.ID, div.DetailURL, client)
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

// ── scheduled helpers ─────────────────────────────────────────────────────────

func runAll(conn *sql.DB, client *http.Client, delay time.Duration) error {
	crawlCalendar(conn, client, delay, "")
	crawlBills(conn, client, delay, "")
	crawlMembers(conn, client, delay, "", "")
	crawlVotes(conn, client, delay, "")
	crawlSenate(conn, client, delay, "")
	return nil
}

func runFrequentVoteCheck(conn *sql.DB, client *http.Client, delay time.Duration, votesURL string) error {
	dates, err := db.SittingDates(conn, scraper.CurrentParliament, scraper.CurrentSession)
	if err != nil {
		return err
	}
	if !scraper.ParliamentIsSitting(dates, "") {
		log.Println("[scheduler] parliament not sitting today — skipping frequent vote check")
		return nil
	}
	return crawlVotes(conn, client, delay, votesURL)
}
