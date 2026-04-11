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

## Build

```bash
# Download dependencies
go mod download

# Compile the crawler binary
go build -o civictracker-crawler ./cmd/crawler

# Verify the build
./civictracker-crawler --help
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

The `civictracker-crawler` binary fetches data from Canadian public government sources and writes it to a local SQLite database.

### One-shot crawl (all domains)

```bash
./civictracker-crawler --db civictracker.db
```

### Crawl specific domains

```bash
# Bills only (LEGISinfo RSS + detail + Library of Parliament summaries)
./civictracker-crawler --bills

# House of Commons votes only
./civictracker-crawler --votes

# Senate votes only
./civictracker-crawler --senate

# MP profiles only
./civictracker-crawler --members

# Sitting calendar only
./civictracker-crawler --calendar
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `--db PATH` | `civictracker.db` | Path to the SQLite database file |
| `--delay MS` | `500` | Milliseconds to sleep between HTTP requests |
| `--parallelism N` | `5` | Max domain crawlers running concurrently (env: `CRAWLER_PARALLELISM`) |
| `--schedule` | — | Run the background scheduler (blocks indefinitely) |
| `-v` | — | Verbose logging |

### Parallelism

By default all five domain crawlers (calendar, bills, members, votes, senate) run concurrently. Use `--parallelism` or the `CRAWLER_PARALLELISM` environment variable to cap concurrency. A value of `1` runs crawlers sequentially.

```bash
# Run at most 2 crawlers at a time
./civictracker-crawler --parallelism 2

# Using the environment variable
CRAWLER_PARALLELISM=2 ./civictracker-crawler

# Sequential (safe for low-resource environments)
./civictracker-crawler --parallelism 1
```

The semaphore pattern is used internally: a buffered channel of size N limits the number of goroutines that may execute concurrently. Each domain crawler acquires a slot on start and releases it when done.

### Scheduled mode

The scheduler runs two jobs:

| Job | Schedule |
|---|---|
| Full crawl (all domains) | Daily at 02:00 UTC |
| Frequent vote check | Every 4 hours (skipped when parliament is not sitting) |

```bash
./civictracker-crawler --schedule --db civictracker.db
```

---

## Database

The SQLite database contains six tables:

| Table | Contents |
|---|---|
| `members` | MP profiles (name, party, riding, province, photo, email) |
| `bills` | Bill metadata (number, title, stage, status, sponsor, full-text URL, LoP summary) |
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
  utils/            HTTP client, URL/ID helpers, date parser
```

