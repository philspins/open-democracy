// Package scheduler runs the nightly and frequent-vote-check cron jobs.
package scheduler

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/robfig/cron/v3"
)

// CrawlFunc is the signature expected for a crawl function passed to the scheduler.
type CrawlFunc func(db *sql.DB) error

// SummarizeFunc is the signature for summarization functions.
type SummarizeFunc func(ctx context.Context, db *sql.DB) (int, error)

// Config holds the functions and DB connection used by the scheduler.
type Config struct {
	DB                *sql.DB
	FullCrawlFn       CrawlFunc     // run nightly at 02:00 UTC
	FrequentVoteCheck CrawlFunc     // run every 4 hours
	LoPSummaryFn      SummarizeFunc // run nightly at 04:00 UTC (after FullCrawlFn)
	AISummarizationFn SummarizeFunc // run nightly at 05:00 UTC (after LoPSummaryFn)
}

// CronSpec holds the cron schedule expressions used by the scheduler.
// Exported so they can be asserted in tests.
const (
	NightlyCronSpec      = "0 2 * * *"
	FrequentVoteCronSpec = "0 */4 * * *"
	LoPSummaryCronSpec   = "0 4 * * *" // 04:00 UTC
	AISummaryCronSpec    = "0 5 * * *" // 05:00 UTC
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

	// Library of Parliament summary scrape at 04:00 UTC (after FullCrawlFn completes)
	if cfg.LoPSummaryFn != nil {
		c.AddFunc(LoPSummaryCronSpec, func() {
			log.Printf("[scheduler] lop_summary_download starting at %s", time.Now().UTC().Format(time.RFC3339))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			count, err := cfg.LoPSummaryFn(ctx, cfg.DB)
			if err != nil {
				log.Printf("[scheduler] lop_summary_download error: %v", err)
			} else {
				log.Printf("[scheduler] lop_summary_download complete (%d downloaded)", count)
			}
		})
	}

	// AI summarization at 05:00 UTC (after LoP summary completes)
	if cfg.AISummarizationFn != nil {
		c.AddFunc(AISummaryCronSpec, func() {
			log.Printf("[scheduler] ai_summarization starting at %s", time.Now().UTC().Format(time.RFC3339))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			defer cancel()
			count, err := cfg.AISummarizationFn(ctx, cfg.DB)
			if err != nil {
				log.Printf("[scheduler] ai_summarization error: %v", err)
			} else {
				log.Printf("[scheduler] ai_summarization complete (%d summarized)", count)
			}
		})
	}

	return c
}

// Start initialises and runs the scheduler. This function blocks until the
// process is killed (send SIGINT/SIGTERM to stop).
func Start(cfg Config) {
	log.Println("[scheduler] starting (UTC)")
	log.Println("[scheduler]   nightly_full_crawl   : daily at 02:00 UTC")
	log.Println("[scheduler]   frequent_vote_check  : every 4 hours")
	if cfg.LoPSummaryFn != nil {
		log.Println("[scheduler]   lop_summary_download : daily at 04:00 UTC")
	}
	if cfg.AISummarizationFn != nil {
		log.Println("[scheduler]   ai_summarization     : daily at 05:00 UTC")
	}

	c := New(cfg)
	c.Start()

	// Block forever
	select {}
}
