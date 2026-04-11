// Package scheduler runs the nightly and frequent-vote-check cron jobs.
package scheduler

import (
	"database/sql"
	"log"
	"time"

	"github.com/robfig/cron/v3"
)

// CrawlFunc is the signature expected for a crawl function passed to the scheduler.
type CrawlFunc func(db *sql.DB) error

// Config holds the functions and DB connection used by the scheduler.
type Config struct {
	DB                *sql.DB
	FullCrawlFn       CrawlFunc // run nightly at 02:00 UTC
	FrequentVoteCheck CrawlFunc // run every 4 hours
}

// Start initialises and runs the APScheduler-equivalent cron scheduler.
// This function blocks until the process is killed.
func Start(cfg Config) {
	c := cron.New(cron.WithLocation(time.UTC))

	// Nightly full crawl at 02:00 UTC
	c.AddFunc("0 2 * * *", func() {
		log.Printf("[scheduler] nightly_full_crawl starting at %s", time.Now().UTC().Format(time.RFC3339))
		if err := cfg.FullCrawlFn(cfg.DB); err != nil {
			log.Printf("[scheduler] nightly_full_crawl error: %v", err)
		} else {
			log.Printf("[scheduler] nightly_full_crawl complete")
		}
	})

	// Frequent vote check every 4 hours
	c.AddFunc("0 */4 * * *", func() {
		log.Printf("[scheduler] frequent_vote_check starting at %s", time.Now().UTC().Format(time.RFC3339))
		if err := cfg.FrequentVoteCheck(cfg.DB); err != nil {
			log.Printf("[scheduler] frequent_vote_check error: %v", err)
		} else {
			log.Printf("[scheduler] frequent_vote_check complete")
		}
	})

	log.Println("[scheduler] starting (UTC)")
	log.Println("[scheduler]   nightly_full_crawl  : daily at 02:00 UTC")
	log.Println("[scheduler]   frequent_vote_check : every 4 hours")

	c.Start()

	// Block forever (the caller sends SIGINT/SIGTERM to stop)
	select {}
}
