# open-democracy

A Canadian civic transparency platform built almost entirely on existing open government data.

Built with the **GOAT Stack**: Go · Templ · Alpine.js · Tailwind CSS.

---

## Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | ≥ 1.24 | https://go.dev/dl/ |
| GCC / CGo | any | `apt install gcc` / `brew install gcc` (required by `go-sqlite3`) |

---

## Environment variables

| Variable | Required | Used by | Purpose |
|---|---|---|---|
| `CRAWLER_PARALLELISM` | No | crawler | Caps concurrent domain crawlers (same effect as `--parallelism`) |
| `ANTHROPIC_API_KEY` | Only for AI summaries | summarizer | Enables Claude API fallback summaries (`summary_ai`) |
| `PARTY_THEME_FILE` | No | frontend templates | Override path for party/province style config (default `config/party-theme.json`) |

Notes:

- The service runs without `ANTHROPIC_API_KEY`; only AI summarization is disabled.
- LoP summary scraping still runs without an API key.

---

## Build

```bash
# Download dependencies
go mod download

# Compile the crawler binary
go build -o open-democracy-crawler ./cmd/crawler

# Compile the web server binary
go build -o open-democracy-server ./cmd/server

# Regenerate templ-generated Go files (needed after editing *.templ files)
go run github.com/a-h/templ/cmd/templ@v0.3.1001 generate

# Verify the build
./open-democracy-crawler --help
```

---

## Run tests

```bash
# Run the full test suite
go test ./...

# Run with verbose output
go test ./... -v

# Run a single package
go test ./internal/scraper/... -v

# Run tests matching a pattern
go test ./... -run TestCrawlSenate
```

All 95 tests are offline — they use `httptest.Server` and temporary SQLite files; no network access is required.

---

## Using the crawler CLI

The `open-democracy-crawler` binary fetches data from Canadian public government sources and writes it to a local SQLite database.

### One-shot crawl (all domains)

```bash
./open-democracy-crawler --db open-democracy.db
```

### Crawl specific domains

```bash
# Bills only (LEGISinfo RSS + detail + Library of Parliament summaries)
./open-democracy-crawler --bills

# House of Commons votes only
./open-democracy-crawler --votes

# Senate votes only
./open-democracy-crawler --senate

# MP profiles only
./open-democracy-crawler --members

# Sitting calendar only
./open-democracy-crawler --calendar
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `--db PATH` | `open-democracy.db` | Path to the SQLite database file |
| `--delay MS` | `500` | Milliseconds to sleep between HTTP requests |
| `--parallelism N` | `5` | Max domain crawlers running concurrently (env: `CRAWLER_PARALLELISM`) |
| `--schedule` | — | Run the background scheduler (blocks indefinitely) |
| `-v` | — | Verbose logging |

### Parallelism

By default all five domain crawlers (calendar, bills, members, votes, senate) run concurrently. Use `--parallelism` or the `CRAWLER_PARALLELISM` environment variable to cap concurrency. A value of `1` runs crawlers sequentially.

```bash
# Run at most 2 crawlers at a time
./open-democracy-crawler --parallelism 2

# Using the environment variable
CRAWLER_PARALLELISM=2 ./open-democracy-crawler

# Sequential (safe for low-resource environments)
./open-democracy-crawler --parallelism 1
```

The semaphore pattern is used internally: a buffered channel of size N limits the number of goroutines that may execute concurrently. Each domain crawler acquires a slot on start and releases it when done.

### Scheduled mode

The scheduler runs four jobs:

| Job | Schedule |
|---|---|
| Full crawl (all domains) | Daily at 02:00 UTC |
| Frequent vote check | Every 4 hours (skipped when parliament is not sitting) |
| LoP summary download | Daily at 04:00 UTC |
| AI summarization fallback | Daily at 05:00 UTC |

```bash
./open-democracy-crawler --schedule --db open-democracy.db
```

If `ANTHROPIC_API_KEY` is not set, the AI summarization job will not be able to generate Claude summaries.

---

## Run the web frontend (Phase 2)

```bash
# Runs the read-only frontend on http://127.0.0.1:8080
go run ./cmd/server -db open-democracy.db -addr :8080
```

The server expects a populated SQLite database. Run the crawler first (one-shot or scheduler mode) to ingest data.

---

## Database

The SQLite database contains six tables:

| Table | Contents |
|---|---|
| `members` | MP profiles (name, party, riding, province, photo, email) |
| `bills` | Bill metadata (number, title, stage, status, sponsor, full-text URL, LoP summary, AI summary JSON, category) |
| `divisions` | House and Senate vote divisions |
| `member_votes` | Per-member votes (Yea / Nay / Paired / Abstain) |
| `bill_stages` | Individual legislative stages for each bill |
| `sitting_calendar` | Dates on which parliament is sitting |

WAL mode and `PRAGMA foreign_keys = ON` are enabled automatically on every connection.

---

## Project layout

```
cmd/
  crawler/          CLI entry point
internal/
  db/               SQLite schema and upsert helpers
  scraper/          Domain scrapers (bills, votes, members, senate)
  scheduler/        Cron scheduler (robfig/cron)
  server/           HTTP handlers and routes for the web frontend
  summarizer/       LoP + Claude summarization pipeline
  templates/        Templ UI components
  utils/            HTTP client, URL/ID helpers, date parser
```

