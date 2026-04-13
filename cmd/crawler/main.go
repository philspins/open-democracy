// Command crawler is the Open Democracy data-crawling CLI.
//
// Usage:
//
//	crawler [flags]
//
// Flags:
//
//	--bills           Crawl bills only (LEGISinfo RSS + detail)
//	--votes           Crawl Commons votes only
//	--senate          Crawl Senate votes only
//	--members         Crawl MP profiles only
//	--calendar        Crawl sitting calendar only
//	--schedule        Run the background scheduler (blocks indefinitely)
//	--db PATH         Path to SQLite database file (default: open-democracy.db)
//	--delay MS        Milliseconds between HTTP requests (default: 500)
//	--parallelism N   Max domain crawlers to run concurrently (default: 5, env: CRAWLER_PARALLELISM)
//	-v                Verbose logging
//
// If no specific domain flag is provided, all crawlers run once.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/scheduler"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/summarizer"
	"github.com/philspins/open-democracy/internal/utils"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Printf("warning: could not load .env: %v", err)
	}

	billsFlag := flag.Bool("bills", false, "Crawl bills only")
	votesFlag := flag.Bool("votes", false, "Crawl Commons votes only")
	senateFlag := flag.Bool("senate", false, "Crawl Senate votes only")
	membersFlag := flag.Bool("members", false, "Crawl MP profiles only")
	calendarFlag := flag.Bool("calendar", false, "Crawl sitting calendar only")
	scheduleFlag := flag.Bool("schedule", false, "Run the background scheduler (blocks indefinitely)")
	dbPath := flag.String("db", db.DefaultPath, "Path to SQLite database file")
	delayMS := flag.Int("delay", 500, "Milliseconds between HTTP requests")
	parallelism := flag.Int("parallelism", defaultParallelism(), "Max domain crawlers to run concurrently (env: CRAWLER_PARALLELISM)")
	verbose := flag.Bool("v", false, "Verbose logging")
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
		p := *parallelism
		scheduler.Start(scheduler.Config{
			DB: conn,
			FullCrawlFn: func(sdb *sql.DB) error {
				return runAll(sdb, client, delay, p)
			},
			FrequentVoteCheck: func(sdb *sql.DB) error {
				return runFrequentVoteCheck(sdb, client, delay, "")
			},
			LoPSummaryFn: func(ctx context.Context, sdb *sql.DB) (int, error) {
				return summarizer.DownloadLoPSummaries(ctx, sdb, nil)
			},
			AISummarizationFn: func(ctx context.Context, sdb *sql.DB) (int, error) {
				return summarizer.SummarizeNewBills(ctx, sdb, true)
			},
		})
		return // never reached
	}

	// ── One-shot mode ────────────────────────────────────────────────────────
	shouldRunAll := !(*billsFlag || *votesFlag || *senateFlag || *membersFlag || *calendarFlag)

	// Build the set of crawl tasks the user selected (or all if none specified).
	type task struct {
		name string
		fn   func() error
	}
	var tasks []task
	var summaryRequests chan summarizer.BillSummaryRequest
	type summaryRunResult struct {
		processed int
		err       error
	}
	var summaryResultCh chan summaryRunResult

	if *billsFlag || shouldRunAll {
		summaryRequests = make(chan summarizer.BillSummaryRequest, 32)
		summaryResultCh = make(chan summaryRunResult, 1)
		go func() {
			n, err := summarizer.SummarizeBillsFromChannel(context.Background(), conn, summaryRequests)
			summaryResultCh <- summaryRunResult{processed: n, err: err}
		}()
	}

	if *calendarFlag || shouldRunAll {
		tasks = append(tasks, task{"calendar", func() error { return crawlCalendar(conn, client, delay, "") }})
	}
	if *billsFlag || shouldRunAll {
		tasks = append(tasks, task{"bills", func() error { return crawlBills(conn, client, delay, "", summaryRequests) }})
	}
	if *membersFlag || shouldRunAll {
		tasks = append(tasks, task{"members", func() error { return crawlMembers(conn, client, delay, "", "") }})
	}
	if *votesFlag || shouldRunAll {
		tasks = append(tasks, task{"votes", func() error { return crawlVotes(conn, client, delay, "") }})
	}
	if *senateFlag || shouldRunAll {
		tasks = append(tasks, task{"senate", func() error { return crawlSenate(conn, client, delay, "") }})
	}

	// Wrap each task so errors are logged with the domain name.
	fns := make([]func(), len(tasks))
	for i, t := range tasks {
		fns[i] = func() {
			if err := t.fn(); err != nil {
				log.Printf("[main] %s error: %v", t.name, err)
			}
		}
	}

	runParallel(*parallelism, fns)
	if summaryRequests != nil {
		close(summaryRequests)
		res := <-summaryResultCh
		if res.err != nil {
			log.Printf("[main] ai summarization channel error: %v", res.err)
		} else {
			log.Printf("[main] ai summaries generated: %d", res.processed)
		}
	}

	// In one-shot mode, run summarization after bill crawling so summaries are
	// persisted in the same execution path.
	if *billsFlag || shouldRunAll {
		ctx := context.Background()
		if n, err := summarizer.DownloadLoPSummaries(ctx, conn, nil); err != nil {
			log.Printf("[main] lop summary job error: %v", err)
		} else {
			log.Printf("[main] lop summaries updated: %d", n)
		}
	}

	log.Println("[main] done")
}

// ── parallelism helpers ───────────────────────────────────────────────────────

// defaultParallelism reads the CRAWLER_PARALLELISM environment variable and
// returns its integer value when set and valid. Otherwise it returns 5 (one
// goroutine per domain crawler).
func defaultParallelism() int {
	if v := os.Getenv("CRAWLER_PARALLELISM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 5
}

// loadDotEnv reads KEY=VALUE pairs from a .env-style file if present.
// Existing process environment variables are not overwritten.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// runParallel runs each function in fns in its own goroutine, allowing at most
// parallelism goroutines to execute concurrently (semaphore pattern). It waits
// for all goroutines to finish before returning. parallelism < 1 is treated as 1.
// Callers are responsible for handling errors (e.g. by logging) inside fns.
func runParallel(parallelism int, fns []func()) {
	if parallelism < 1 {
		parallelism = 1
	}
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}        // acquire a slot
			defer func() { <-sem }() // release on return
			fn()
		}()
	}
	wg.Wait()
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

func crawlBills(conn *sql.DB, client *http.Client, delay time.Duration, rssURL string, summaryRequests chan<- summarizer.BillSummaryRequest) error {
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

		if summaryRequests != nil && strings.TrimSpace(bill.FullTextURL) != "" {
			summaryRequests <- summarizer.BillSummaryRequest{
				BillID:           bill.ID,
				BillTitle:        bill.Title,
				FullTextURL:      bill.FullTextURL,
				LastActivityDate: bill.LastActivityDate,
			}
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

func runAll(conn *sql.DB, client *http.Client, delay time.Duration, parallelism int) error {
	type summaryRunResult struct {
		processed int
		err       error
	}
	summaryRequests := make(chan summarizer.BillSummaryRequest, 32)
	summaryResultCh := make(chan summaryRunResult, 1)
	go func() {
		n, err := summarizer.SummarizeBillsFromChannel(context.Background(), conn, summaryRequests)
		summaryResultCh <- summaryRunResult{processed: n, err: err}
	}()

	fns := []func(){
		func() { crawlCalendar(conn, client, delay, "") },
		func() { crawlBills(conn, client, delay, "", summaryRequests) },
		func() { crawlMembers(conn, client, delay, "", "") },
		func() { crawlVotes(conn, client, delay, "") },
		func() { crawlSenate(conn, client, delay, "") },
	}
	runParallel(parallelism, fns)
	close(summaryRequests)
	res := <-summaryResultCh
	if res.err != nil {
		return fmt.Errorf("summarization pipeline: %w", res.err)
	}
	log.Printf("[scheduler] ai summaries generated: %d", res.processed)
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
