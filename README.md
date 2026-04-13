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
| `SUMMARIZER_PARALLELISM` | No | summarizer | Number of concurrent AI summarization workers (default: `1`) |
| `ANTHROPIC_API_KEY` | Only for AI summaries | summarizer | Enables Claude API fallback summaries (`summary_ai`) |
| `ANTHROPIC_MODEL` | No | summarizer | Claude model ID/alias override (default first try: `claude-sonnet-4-6`) |
| `PARTY_THEME_FILE` | No | frontend templates | Override path for party/province style config (default `config/party-theme.json`) |
| `OAUTH_BASE_URL` | Recommended for auth/OAuth | server | Public app base URL used to build verification and OAuth callback URLs (e.g. `https://open-democracy.ca`) |
| `SES_FROM_EMAIL` | Yes for verification email delivery | server | Verified SES sender address used for outgoing verification emails (e.g. `contact@open-democracy.ca`) |
| `GOOGLE_CLIENT_ID` | Only for Google login | server | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | Only for Google login | server | Google OAuth client secret |
| `FACEBOOK_CLIENT_ID` | Only for Facebook login | server | Facebook OAuth app client ID |
| `FACEBOOK_CLIENT_SECRET` | Only for Facebook login | server | Facebook OAuth app client secret |
| `AWS_REGION` | Usually yes for SES | server | AWS region for SES client (for example `ca-central-1`) |
| `AWS_ACCESS_KEY_ID` | Optional (depends on credential source) | server | AWS credentials for SES if not using instance/profile credentials |
| `AWS_SECRET_ACCESS_KEY` | Optional (depends on credential source) | server | AWS credentials for SES if not using instance/profile credentials |
| `AWS_SESSION_TOKEN` | Optional | server | Temporary session token when using temporary AWS credentials |

Notes:

- The service runs without `ANTHROPIC_API_KEY`; only AI summarization is disabled.
- Set `SUMMARIZER_PARALLELISM` > 1 to summarize multiple bills concurrently.
- If `ANTHROPIC_MODEL` is unset, summarization first tries `claude-sonnet-4-6` and automatically falls back to compatible Sonnet/Haiku model IDs.
- LoP summary scraping still runs without an API key.
- If `SES_FROM_EMAIL` is unset, verification requests are accepted but emails are not sent.
- OAuth login routes require provider credentials to be set (`GOOGLE_*` and/or `FACEBOOK_*`).
- AWS credentials are loaded via the default AWS SDK chain (env vars, shared config/profile, or attached role).

Auth/OAuth examples:

```bash
# Base URL used in OAuth callbacks and verification links
OAUTH_BASE_URL=https://open-democracy.ca

# SES sender (must be verified in SES)
SES_FROM_EMAIL=contact@open-democracy.ca

# Optional explicit AWS credentials (if not using role/profile)
AWS_REGION=ca-central-1
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...

# Optional Anthropic model override
ANTHROPIC_MODEL=claude-sonnet-4-6

# Optional summarization worker count
SUMMARIZER_PARALLELISM=4

# Optional social providers
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
FACEBOOK_CLIENT_ID=...
FACEBOOK_CLIENT_SECRET=...
```

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

## Deploy to Netlify (SSR via Function)

This repository includes a Netlify SSR function entry point at `cmd/netlify/main.go` and routing/build config in `netlify.toml`.

### What Netlify runs

- Build command: `go build -o netlify/functions/ssr ./cmd/netlify`
- Function path: `netlify/functions/ssr`
- Redirect: all requests (`/*`) are routed to `/.netlify/functions/ssr`

### Deploy steps

1. Push this repo to GitHub.
2. In Netlify, create a new site from that repository.
3. Netlify detects `netlify.toml` and uses the configured build/function settings.
4. Add required environment variables in Netlify Site Settings.

### Recommended Netlify environment variables

- `OAUTH_BASE_URL=https://<your-site>.netlify.app` (or your custom domain)
- `OPEN_DEMOCRACY_DB_PATH=/tmp/open-democracy.db` (writable Lambda filesystem)
- All auth/email/provider variables you use in production (`SES_FROM_EMAIL`, `GOOGLE_*`, `FACEBOOK_*`, AWS credentials/region).

### SQLite note on serverless

- Lambda code package files are read-only.
- On cold start, the function copies bundled `open-democracy.db` to `/tmp/open-democracy.db` if needed, then opens SQLite there.
- `/tmp` is ephemeral and instance-local, so writes are not durable across cold starts/instances.

For persistent multi-instance writes, migrate to an external DB (for example Neon/Postgres).

### Netlify production checklist

- Confirm `GET /healthz` returns `200` after deploy.
- In Netlify Site Settings and any uptime monitoring integration, set the health check URL/path override to `/healthz` (for example `https://<your-domain>/healthz`) instead of `/`.
- Set `OAUTH_BASE_URL` to your Netlify/custom domain URL.
- Set `OPEN_DEMOCRACY_DB_PATH=/tmp/open-democracy.db`.
- Configure all auth variables you use (`GOOGLE_*`, `FACEBOOK_*`, `SES_FROM_EMAIL`, AWS region/credentials).
- If not using local SQLite-only mode, move persistent user write paths to an external database.
- Verify OAuth callback URLs in Google/Facebook dashboards match your Netlify domain.
- Test login, profile update, and any write endpoints after a fresh deploy (cold start).
- Monitor function duration/errors in Netlify logs; keep request handling under function timeout limits.
- Add a custom domain and enforce HTTPS before production launch.

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

