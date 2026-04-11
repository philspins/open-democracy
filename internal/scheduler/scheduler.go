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

// CronSpec holds the cron schedule expressions used by the scheduler.
// Exported so they can be asserted in tests.
const (
	NightlyCronSpec       = "0 2 * * *"
	FrequentVoteCronSpec  = "0 */4 * * *"
)

// New creates and registers all cron jobs from cfg, returning the configured
// *cron.Cron without starting it or blocking. Call c.Start() then keep the
// process alive (e.g. with select{}) to activate the schedule.
func New(cfg Config) *cron.Cron {
	c := cron.New(cron.WithLocation(time.UTC))

	// Nightly full crawl at 02:00 UTC
	c.AddFunc(NightlyCronSpec, func() {
		log.Printf("[scheduler] nightly_full_crawl starting at %s", time.Now().UTC().Format(time.RFC3339))
		if err := cfg.FullCrawlFn(cfg.DB); err != nil {
			log.Printf("[scheduler] nightly_full_crawl error: %v", err)
		} else {
			log.Printf("[scheduler] nightly_full_crawl complete")
		}
	})

	// Frequent vote check every 4 hours
	c.AddFunc(FrequentVoteCronSpec, func() {
		log.Printf("[scheduler] frequent_vote_check starting at %s", time.Now().UTC().Format(time.RFC3339))
		if err := cfg.FrequentVoteCheck(cfg.DB); err != nil {
			log.Printf("[scheduler] frequent_vote_check error: %v", err)
		} else {
			log.Printf("[scheduler] frequent_vote_check complete")
		}
	})

	return c
}

// Start initialises and runs the scheduler. This function blocks until the
// process is killed (send SIGINT/SIGTERM to stop).
func Start(cfg Config) {
	log.Println("[scheduler] starting (UTC)")
	log.Println("[scheduler]   nightly_full_crawl  : daily at 02:00 UTC")
	log.Println("[scheduler]   frequent_vote_check : every 4 hours")

	c := New(cfg)
	c.Start()

	// Block forever
	select {}
}
