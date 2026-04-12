# Open Democracy — Detailed Implementation Plan

> 5 phases · 10–13 weeks · 0 paid APIs for MVP

---

## Table of Contents

- [Phase 1 — Data Foundation](#phase-1--data-foundation)
- [Phase 2 — Read-Only Frontend](#phase-2--read-only-frontend)
- [Phase 3 — AI Summarization](#phase-3--ai-summarization)
- [Phase 4 — User Features](#phase-4--user-features)
- [Phase 5 — Accountability Layer](#phase-5--accountability-layer)

---

## Phase 1 — Data Foundation

**Crawl & store public government data · 2–3 weeks**

**Stack:** Go · gofeed · goquery · robfig/cron · SQLite

Everything you need exists in public government HTML and RSS. This phase is about writing reliable, scheduled scrapers, defining the data schema, and building a local database you control. No auth, no users, no frontend yet.

---

### 1.1 Data Sources & What Each Gives You

| Source | URL Pattern | What You Get | Method |
|--------|-------------|--------------|--------|
| LEGISinfo RSS | `parl.ca/legisinfo/en/bills/rss` | All active bills: title, sponsor, chamber, stage, last activity date. Updates within hours of a change. | gofeed |
| Bill Detail Page | `parl.ca/legisinfo/en/bill/{parl}-{session}/{id}` | Full stage timeline (1st/2nd/3rd reading, committee, Royal Assent), Hansard debate links, committee meeting dates, amendments | net/http + goquery |
| Bill Full Text | `parl.ca/DocumentViewer/en/{parl}-{session}/bill/{id}/first-reading` | Actual legislative text as HTML. Feed to AI summarizer. | net/http + goquery |
| LoP Legislative Summaries | `lop.parl.ca/sites/PublicWebsite/.../LegislativeSummaries` | Professional researcher plain-English summaries for ~300 major bills/year. Use these instead of AI where available. | net/http + goquery |
| MP Votes Index | `ourcommons.ca/Members/en/votes` | Table: vote #, date, bill, description, Yeas count, Nays count, result. One row per recorded division. | net/http + goquery |
| Individual Vote Detail | `ourcommons.ca/Members/en/votes/{vote_id}` | How every single MP voted (Yea/Nay/Paired/Abstain) on that division. | net/http + goquery |
| MP Profile | `ourcommons.ca/Members/en/{id}` | Photo URL, name, party, riding, province, role, contact email, website. | net/http + goquery |
| MP Vote History (Work tab) | `ourcommons.ca/Members/en/{id}?tab=votes` | Full voting record for that MP — all divisions they participated in. | net/http + goquery (JS-rendered pages may need chromedp as fallback) |
| Sitting Calendar | `ourcommons.ca/en/sitting-calendar` | Scheduled sitting dates for current session. Used to determine if parliament is in session. | net/http + goquery |
| Senate Votes | `sencanada.ca/en/in-the-chamber/votes` | Senate division records. Same structure as Commons votes. | net/http + goquery |

---

### 1.2 Database Schema

```sql
-- Core tables. Start with SQLite, swap to Postgres when you need concurrent writes.

CREATE TABLE members (
  id            TEXT PRIMARY KEY,      -- ourcommons member ID e.g. "123006"
  name          TEXT NOT NULL,
  party         TEXT,
  riding        TEXT,
  province      TEXT,
  role          TEXT,                  -- "Member of Parliament", "Minister of...", etc.
  photo_url     TEXT,
  email         TEXT,
  website       TEXT,
  chamber       TEXT DEFAULT 'commons', -- 'commons' | 'senate'
  active        BOOLEAN DEFAULT TRUE,
  last_scraped  TIMESTAMP
);

CREATE TABLE bills (
  id            TEXT PRIMARY KEY,      -- e.g. "45-1-C-47"  (parliament-session-billnumber)
  parliament    INTEGER,
  session       INTEGER,
  number        TEXT,                  -- "C-47", "S-209"
  title         TEXT,
  short_title   TEXT,
  bill_type     TEXT,                  -- "Government Bill", "Private Member's Bill", "Senate Public Bill"
  chamber       TEXT,                  -- 'commons' | 'senate'
  sponsor_id    TEXT REFERENCES members(id),
  current_stage TEXT,                  -- '1st_reading' | '2nd_reading' | 'committee' | '3rd_reading' | 'royal_assent'
  current_status TEXT,                 -- free text from LEGISinfo
  category      TEXT,                  -- AI-assigned: 'Housing', 'Health', 'Defence', etc.
  summary_ai    TEXT,                  -- AI-generated plain English summary
  summary_lop   TEXT,                  -- Library of Parliament summary (preferred if exists)
  full_text_url TEXT,
  legisinfo_url TEXT,
  introduced_date DATE,
  last_activity_date DATE,
  last_scraped  TIMESTAMP
);

CREATE TABLE divisions (
  id            TEXT PRIMARY KEY,      -- e.g. "45-1-892"
  parliament    INTEGER,
  session       INTEGER,
  number        INTEGER,
  date          DATE,
  bill_id       TEXT REFERENCES bills(id),
  description   TEXT,
  yeas          INTEGER,
  nays          INTEGER,
  paired        INTEGER DEFAULT 0,
  result        TEXT,                  -- 'Agreed to' | 'Negatived'
  chamber       TEXT DEFAULT 'commons',
  sitting_url   TEXT,
  last_scraped  TIMESTAMP
);

CREATE TABLE member_votes (
  division_id   TEXT REFERENCES divisions(id),
  member_id     TEXT REFERENCES members(id),
  vote          TEXT,                  -- 'Yea' | 'Nay' | 'Paired' | 'Abstain'
  PRIMARY KEY (division_id, member_id)
);

CREATE TABLE bill_stages (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  bill_id       TEXT REFERENCES bills(id),
  stage         TEXT,
  chamber       TEXT,
  date          TEXT,
  notes         TEXT                   -- e.g. "Referral to committee", "Report stage"
);

-- Indices for common query patterns
CREATE INDEX idx_divisions_bill ON divisions(bill_id);
CREATE INDEX idx_member_votes_member ON member_votes(member_id);
CREATE INDEX idx_bills_stage ON bills(current_stage);
CREATE INDEX idx_bills_category ON bills(category);
```

---

### 1.3 Crawler Architecture

```go
// cmd/crawler/main.go — entry point
// Run with: go run ./cmd/crawler --db civictracker.db
// Scheduled with robfig/cron (see internal/scheduler/scheduler.go)

package main

import (
    "database/sql"
    "log"
    "net/http"
    "time"

    "github.com/philspins/open-democracy/internal/db"
    "github.com/philspins/open-democracy/internal/scraper"
    "github.com/philspins/open-democracy/internal/utils"
)

const userAgent = "Open Democracy/1.0 (open-democracy.ca; contact@open-democracy.ca)"
// ↑ Always identify yourself to government scrapers. Be polite.

func crawlBillsFromRSS(database *sql.DB) error {
    client := utils.NewHTTPClient() // sets User-Agent, timeout, polite delays
    stubs, err := scraper.CrawlBillsRSS(scraper.RSSUrl, client)
    if err != nil {
        return err
    }
    log.Printf("[crawler] RSS returned %d bills", len(stubs))
    for _, stub := range stubs {
        detail, err := scraper.CrawlBillDetail(stub.LegisInfoURL, client)
        if err != nil {
            log.Printf("[crawler] detail fetch failed for %s: %v", stub.ID, err)
            continue
        }
        if err := db.UpsertBill(database, stub, detail); err != nil {
            log.Printf("[crawler] upsert failed for %s: %v", stub.ID, err)
        }
        time.Sleep(500 * time.Millisecond) // Be polite — 0.5s between requests
    }
    return nil
}

func crawlVotesIndex(database *sql.DB) error {
    client := utils.NewHTTPClient()
    divisions, err := scraper.CrawlVotesIndex(
        scraper.VotesIndexURL,
        scraper.CurrentParliament, scraper.CurrentSession,
        client,
    )
    if err != nil {
        return err
    }
    for _, div := range divisions {
        if db.DivisionExists(database, div.ID) {
            continue
        }
        memberVotes, err := scraper.CrawlDivisionDetail(div.DetailURL, div.ID, client)
        if err != nil {
            log.Printf("[crawler] division detail failed for %s: %v", div.ID, err)
            continue
        }
        db.UpsertDivision(database, div)
        db.UpsertMemberVotes(database, memberVotes)
        time.Sleep(500 * time.Millisecond)
    }
    return nil
}


// internal/scheduler/scheduler.go

package scheduler

import (
    "database/sql"
    "log"
    "time"

    "github.com/robfig/cron/v3"
)

func New(cfg Config) *cron.Cron {
    c := cron.New(cron.WithLocation(time.UTC))

    // Nightly full crawl at 02:00 UTC
    c.AddFunc("0 2 * * *", func() {
        log.Printf("[scheduler] nightly_full_crawl starting")
        if err := cfg.FullCrawlFn(cfg.DB); err != nil {
            log.Printf("[scheduler] nightly_full_crawl error: %v", err)
        }
    })

    // Frequent vote check every 4 hours
    c.AddFunc("0 */4 * * *", func() {
        log.Printf("[scheduler] frequent_vote_check starting")
        if err := cfg.FrequentVoteCheck(cfg.DB); err != nil {
            log.Printf("[scheduler] frequent_vote_check error: %v", err)
        }
    })

    return c
}
```

---

### 1.4 Rate Limiting & Politeness

- ✅ Set a descriptive User-Agent header identifying your app and contact email. Government IT teams notice anonymous scrapers and may block them.
- ✅ Add 0.5–1 second sleep between requests. Government servers are not CDN-backed; be a good citizen.
- ✅ Use a shared `*http.Client` with a 10-second timeout. Wrap with a simple in-memory TTL cache (or `httpcache`) with a 6-hour TTL for detail pages. Bills don't change hourly.
- ✅ Check `robots.txt` for both `ourcommons.ca` and `parl.ca` before scraping — both are permissive but verify.
- ○ Consider emailing the House of Commons IT team (they have a public address) to let them know about the project. They've been known to provide unofficial data feeds to civic tech projects.
- ○ For the MP vote history 'Work' tab — test if it requires JS rendering. If so, add `chromedp` as a fallback for that specific endpoint only.

---

## Phase 2 — Read-Only Frontend

**The no-login MVP — clean UI over public data · 2–3 weeks**

**Stack:** Go · Templ · Alpine.js · Tailwind CSS

Build the public-facing UI. No auth, no user accounts, no upvotes yet. Just a beautiful, fast, searchable window into the government data you collected in Phase 1. This is already a publishable, useful product.

---

### 2.1 Page Structure & Routes

| Route | Page Name | Key Components | Data Source |
|-------|-----------|----------------|-------------|
| `/` | Home / Dashboard | Parliament status banner, recent bill activity feed, recent divisions feed, postcode MP lookup widget | DB: bills + divisions + sitting calendar |
| `/bills` | Bills Feed | Category filter tabs, stage filter, search bar, bill cards with progress indicators | DB: bills (paginated, filtered) |
| `/bills/{id}` | Bill Detail | Stage timeline, AI summary, full bill link, vote breakdown table, constituent sentiment (Phase 4) | DB: bill + divisions + member_votes |
| `/votes` | Votes / Divisions | Table of all recorded divisions, sortable by date/result/bill, filterable by parliament | DB: divisions |
| `/members` | MPs Directory | Search by name/riding/province/party, grid or list view | DB: members |
| `/members/{id}` | MP Profile | Photo, bio, contact info, full vote history table, party-line analysis, category breakdown | DB: member + member_votes + divisions |
| `/compare` | Compare MPs | Select 2 MPs side-by-side, voting overlap %, divergence on specific bills | DB: member_votes |
| `/riding/{postal}` | Your Representatives | Enter postcode → see federal + provincial reps, their recent votes, contact links | DB: members + Elections Canada riding lookup |

---

### 2.2 Bill Card & Status Pipeline UI

```go
// internal/templates/bill_card.templ
// The core reusable component used everywhere bills appear

package templates

var stageOrder = []struct{ Key, Label string }{
    {"1st_reading",  "1st Reading"},
    {"2nd_reading",  "2nd Reading"},
    {"committee",    "Committee"},
    {"3rd_reading",  "3rd Reading"},
    {"royal_assent", "Royal Assent"},
}

var categoryColors = map[string]string{
    "Housing":     "#F59E0B",
    "Health":      "#EF4444",
    "Environment": "#22C55E",
    "Defence":     "#3B82F6",
    "Indigenous":  "#8B5CF6",
    "Finance":     "#0EA5E9",
    "Justice":     "#F97316",
    "Other":       "#6B7280",
}

templ BillCard(bill Bill) {
    @billCardInner(bill, stageIndexOf(bill.CurrentStage))
}

templ billCardInner(bill Bill, stageIdx int) {
    <article class="bill-card">
        <!-- Header row -->
        <div class="bill-card-header">
            <span class="bill-number">{ bill.Number }</span>
            <span
                class="bill-category"
                style={ categoryBadgeStyle(bill.Category) }
            >{ bill.Category }</span>
            <span class="bill-type">{ bill.BillType }</span>
        </div>

        <!-- Title -->
        <h3 class="bill-title">
            <a href={ templ.SafeURL("/bills/" + bill.ID) }>
                { shortOrFullTitle(bill) }
            </a>
        </h3>

        <!-- Summary — 2 lines max, truncated via CSS -->
        if summary := firstNonEmpty(bill.SummaryLoP, bill.SummaryAI); summary != "" {
            <p class="bill-summary">{ summary }</p>
        }

        <!-- Stage pipeline — Alpine.js drives the progress bar width -->
        <div class="bill-stages">
            for i, stage := range stageOrder {
                <div
                    class={ stageDotClass(i, stageIdx) }
                    title={ stage.Label }
                >
                    <div class="stage-pip"></div>
                    <span class="stage-label">{ stage.Label }</span>
                </div>
            }
            <div
                class="stage-progress-bar"
                style={ progressBarStyle(stageIdx) }
            ></div>
        </div>

        <!-- Footer -->
        <div class="bill-card-footer">
            <span>Sponsored by { bill.SponsorName }</span>
            <span>Last activity: { FormatDate(bill.LastActivityDate) }</span>
        </div>
    </article>
}
```

---

### 2.3 Parliament Status Banner

```go
// internal/server/handlers.go
// Determines if parliament is currently sitting based on the sitting calendar.
// Shown prominently at the top of every page (the orange/blue sketch detail).

package server

import (
    "database/sql"
    "time"
)

type ParliamentStatus struct {
    Status     string // "in_session" | "on_break"
    Label      string
    Detail     string
    Parliament int
    Session    int
}

func GetParliamentStatus(db *sql.DB) (ParliamentStatus, error) {
    today := time.Now().UTC().Format("2006-01-02")

    var isSitting bool
    db.QueryRow(
        "SELECT COUNT(*) > 0 FROM sitting_calendar WHERE parliament = 45 AND session = 1 AND date = ?",
        today,
    ).Scan(&isSitting)

    var nextSitting string
    db.QueryRow(
        "SELECT date FROM sitting_calendar WHERE parliament = 45 AND session = 1 AND date > ? ORDER BY date LIMIT 1",
        today,
    ).Scan(&nextSitting)

    if isSitting {
        return ParliamentStatus{
            Status: "in_session", Label: "In Session",
            Detail: "The House is sitting today", Parliament: 45, Session: 1,
        }, nil
    }
    detail := "No sitting dates scheduled"
    if nextSitting != "" {
        detail = "Next sitting: " + FormatDate(nextSitting)
    }
    return ParliamentStatus{
        Status: "on_break", Label: "On Break",
        Detail: detail, Parliament: 45, Session: 1,
    }, nil
}


// internal/templates/layout.templ
// The banner shown on every page — matches the orange (provincial) / blue (federal) split

templ ParliamentBanner(federal, provincial ParliamentStatus) {
    <div class="parliament-banner">
        <div class="banner-half banner-provincial">
            <span class="banner-label">Your Provincial Legislature</span>
            <span class={ "banner-status " + provincial.Status }>{ provincial.Label }</span>
            <span class="banner-detail">{ provincial.Detail }</span>
        </div>
        <div class="banner-half banner-federal">
            <span class="banner-label">Parliament of Canada</span>
            <span class={ "banner-status " + federal.Status }>{ federal.Label }</span>
            <span class="banner-detail">{ federal.Detail }</span>
        </div>
    </div>
}
```

---

### 2.4 MP Profile — Vote History Table

```go
// internal/server/handlers.go — handler for GET /members/{id}

func (s *Server) handleMemberProfile(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    member, err := s.store.GetMember(id)
    if err != nil {
        http.NotFound(w, r); return
    }
    votes, _  := s.store.GetMemberVotes(id, 50)
    stats, _  := s.store.GetMemberStats(id)

    templates.MemberProfile(member, votes, stats).Render(r.Context(), w)
}


// internal/templates/member_profile.templ

templ MemberProfile(member Member, votes []VoteRow, stats MemberStats) {
    @MemberHero(member)
    @StatsRow([]Stat{
        {Label: "Votes cast this session", Value: fmt.Sprint(stats.TotalVotes)},
        {Label: "With party",              Value: fmt.Sprintf("%d%%", stats.PartyLinePct)},
        {Label: "Against party",           Value: fmt.Sprintf("%d%%", stats.RebelPct)},
        {Label: "Missed votes",            Value: fmt.Sprintf("%d%%", stats.MissedPct)},
    })
    @VoteHistoryTable(votes)
    @CategoryBreakdown(member.ID)
    @ContactSection(member)
}

templ VoteHistoryTable(votes []VoteRow) {
    <table>
        <thead>
            <tr>
                <th>Date</th>
                <th>Bill</th>
                <th>Division Description</th>
                <th>Vote</th>
                <th>Result</th>
                <th>Party Line?</th>
            </tr>
        </thead>
        <tbody>
            for _, v := range votes {
                <tr class={ "vote-row " + strings.ToLower(v.Vote) }>
                    <td>{ FormatDate(v.Date) }</td>
                    <td><a href={ templ.SafeURL("/bills/" + v.BillID) }>{ v.BillNumber }</a></td>
                    <td>{ v.Description }</td>
                    <td>@VoteBadge(v.Vote)</td>
                    <td>{ v.Result }</td>
                    <td>
                        if v.VotedWithParty {
                            ✓
                        } else {
                            <span class="rebel">✗ Broke ranks</span>
                        }
                    </td>
                </tr>
            }
        </tbody>
    </table>
}
```

---

### 2.5 Postcode → Riding Lookup

- ✅ Elections Canada publishes a full postcode-to-riding dataset as a free CSV download (`elections.ca`). Import it into your DB as a `postal_ridings` table.
- ✅ Table structure: `postal_code` (TEXT), `federal_riding_id`, `federal_riding_name`, `provincial_riding_id` (if you add provinces later).
- ✅ The lookup is a single DB query: `SELECT * FROM members WHERE riding_id = (SELECT federal_riding_id FROM postal_ridings WHERE postal_code = ?)`. Sub-20ms. The Go handler writes the result directly into the Templ template — no client-side fetch needed.
- ✅ For the MVP, just ask the user to type their first 3 postal code characters (FSA) — that narrows to a ~10km radius, sufficient to identify a riding.
- ✅ The postcode input widget on the home page uses Alpine.js (`x-data`, `x-model`, `@submit.prevent`) to submit the form without a full page reload and swap in the rep cards via HTMX-style partial rendering or a simple `fetch` + `innerHTML`.
- ○ v2: full postcode match for users near riding boundaries.

---

## Phase 3 — AI Summarization

**Make every bill readable in 30 seconds · 1 week**

**Stack:** Claude API (claude-sonnet-4) · Go · lop.parl.ca scraper

Bills are written in dense legalese. Most Canadians will never read one. This phase adds a background worker that fetches bill text, runs it through an LLM, and stores a plain-English summary. The Library of Parliament already does this for major bills — use their summaries first, AI only as fallback.

---

### 3.1 Summary Priority Ladder

- ✅ **TIER 1 — Library of Parliament** (`lop.parl.ca/LegislativeSummaries`): Written by Parliamentary researchers. Authoritative, non-partisan, already plain English. ~300 major bills per parliament get these. Scrape and store as `summary_lop`. Always display this if it exists.
- ✅ **TIER 2 — LEGISinfo bill description**: The short description on the bill's LEGISinfo page is usually 1–3 sentences written by the House of Commons. Good for minor bills that don't get a LoP summary.
- ✅ **TIER 3 — AI-generated summary**: For private member's bills, Senate public bills, and anything without LoP coverage. Generate from bill text using Claude API. Store as `summary_ai`.
- ○ **TIER 4 (future)**: Crowdsourced corrections — let logged-in users flag inaccurate AI summaries and suggest improvements.

---

### 3.2 AI Summarization Pipeline

```go
// internal/summarizer/pipeline.go
package summarizer

import (
    "bytes"
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
    "unicode/utf8"
)

const systemPrompt = `You are a non-partisan Canadian civic education assistant.
Your job is to summarize bills from the Parliament of Canada in plain English.
You must be accurate, neutral, and clear. Never editorialize or express opinions.
Always write for a Canadian high school student — no legal jargon.`

// SummaryResult holds the structured fields returned by the LLM.
type SummaryResult struct {
    OneSentence   string   `json:"one_sentence"`
    PlainSummary  string   `json:"plain_summary"`
    KeyChanges    []string `json:"key_changes"`
    WhoIsAffected []string `json:"who_is_affected"`
    EstimatedCost string   `json:"estimated_cost"`
    Category      string   `json:"category"`
    BillID        string   `json:"bill_id"`
    GeneratedAt   string   `json:"generated_at"`
    Model         string   `json:"model"`
}

// SummarizeBill calls the Claude API and returns a structured summary.
func SummarizeBill(ctx context.Context, billID, billTitle, billText string) (*SummaryResult, error) {
    apiKey := mustEnv("ANTHROPIC_API_KEY")

    // Truncate very long bills — keep first ~120 KB + last 30 KB (rune-safe)
    const maxRunes = 150_000
    if utf8.RuneCountInString(billText) > maxRunes {
        runes := []rune(billText)
        billText = string(runes[:120_000]) + "\n\n[...truncated...]\n\n" + string(runes[len(runes)-30_000:])
    }

    prompt := fmt.Sprintf(`Bill title: %s

Full text:
%s

Please provide the following in JSON format (no markdown, raw JSON only):

{
  "one_sentence": "One sentence (max 25 words) describing what this bill does.",
  "plain_summary": "2–3 paragraph plain-English explanation. What does it do? Who does it affect? Why was it introduced?",
  "key_changes": ["List of 3–6 specific things this bill would change or create"],
  "who_is_affected": ["List of groups, industries, or people most affected"],
  "estimated_cost": "Fiscal impact if mentioned in the bill, or 'Not specified'",
  "category": "One of: Housing, Health, Environment, Defence, Indigenous, Finance, Justice, Agriculture, Transport, Labour, Education, Foreign Affairs, Digital/Tech, Other"
}`, billTitle, billText)

    body, _ := json.Marshal(map[string]any{
        "model":      "claude-sonnet-4-20250514",
        "max_tokens": 1500,
        "system":     systemPrompt,
        "messages":   []map[string]string{{"role": "user", "content": prompt}},
    })

    req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
    req.Header.Set("x-api-key", apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")
    req.Header.Set("content-type", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("claude request: %w", err)
    }
    defer resp.Body.Close()

    var out struct {
        Content []struct{ Text string `json:"text"` } `json:"content"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Content) == 0 {
        return nil, fmt.Errorf("decode claude response: %w", err)
    }

    var result SummaryResult
    if err := json.Unmarshal([]byte(out.Content[0].Text), &result); err != nil {
        return nil, fmt.Errorf("parse summary JSON: %w", err)
    }
    result.BillID      = billID
    result.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
    result.Model       = "claude-sonnet-4-20250514"
    return &result, nil
}

// SummarizeNewBills processes all bills that still lack a summary.
func SummarizeNewBills(ctx context.Context, db *sql.DB) error {
    rows, _ := db.QueryContext(ctx,
        `SELECT id, title, full_text_url FROM bills
         WHERE summary_ai IS NULL AND summary_lop IS NULL
           AND full_text_url IS NOT NULL`)
    defer rows.Close()

    type billRow struct{ ID, Title, FullTextURL string }
    var bills []billRow
    for rows.Next() {
        var b billRow
        rows.Scan(&b.ID, &b.Title, &b.FullTextURL)
        bills = append(bills, b)
    }
    rows.Close()

    for _, bill := range bills {
        // 1. Check LoP first
        if lop := scrapeLoPSummary(bill.ID); lop != "" {
            db.ExecContext(ctx, "UPDATE bills SET summary_lop = ? WHERE id = ?", lop, bill.ID)
            continue
        }

        // 2. Fetch bill text
        billText, err := fetchBillText(bill.FullTextURL)
        if err != nil || billText == "" {
            continue
        }

        // 3. Generate AI summary
        summary, err := SummarizeBill(ctx, bill.ID, bill.Title, billText)
        if err != nil {
            log.Printf("[summarizer] failed for %s: %v", bill.ID, err)
            continue
        }
        db.ExecContext(ctx,
            "UPDATE bills SET summary_ai = ?, category = ? WHERE id = ?",
            summary.PlainSummary, summary.Category, bill.ID)

        time.Sleep(time.Second) // Rate limit
    }
    return nil
}
```

---

### 3.3 Displaying Summaries — UI Guidelines

- ✅ Always show the source of the summary: "Summary by Library of Parliament" vs "AI-generated summary — may contain errors." Use distinct visual styling.
- ✅ Show `one_sentence` on bill cards in the feed. Show the full `plain_summary` on the bill detail page.
- ✅ Show `key_changes` as a bullet list under the summary. Show `who_is_affected` as tags/chips.
- ✅ Link directly to the full bill text (DocumentViewer URL) for users who want the source. Never hide the original.
- ○ Add a "Was this summary helpful?" thumbs up/down — store feedback to improve prompts and flag inaccurate AI summaries for review.

---

## Phase 4 — User Features

**Accounts, following, upvotes & constituent feedback · 3–4 weeks**

**Stack:** Go sessions (gorilla/sessions or built-in cookie store) · Resend (email) · SQLite · Go HTTP handlers · Elections Canada riding data

This phase adds the engagement layer — accounts, following MPs, upvoting on bills, and the policy idea submission flow. The key design principle: minimize friction. The feedback loop (policy idea → mailto → MP) requires zero backend infrastructure beyond the `mailto:` link itself.

---

### 4.1 Authentication Strategy

| Option | Pros | Cons | Verdict |
|--------|------|------|---------|
| Magic link (email only, Go handler) | Zero-password UX, simple, privacy-respecting, no vendor dependency — generate a signed token, email it, verify on click | Slower sign-in flow (check email) | ✅ Recommended for MVP — fits naturally with Go's `net/http` |
| Signed cookies / server sessions | Full control, no external service, standard Go middleware (gorilla/sessions) | Must manage session store in SQLite | Good choice once magic-link flow is in place |
| OAuth (Google/GitHub) via golang.org/x/oauth2 | Familiar social login, no password storage | Vendor dependency for each provider | Good v2 option for higher sign-up conversion |
| localStorage only (no accounts) | Zero friction, works immediately | No cross-device sync, no email notifications | OK for follow/upvote if you skip email notifications |

---

### 4.2 Follow an MP — Data Model & Flow

```sql
-- User tables (add to existing schema)

CREATE TABLE users (
  id            TEXT PRIMARY KEY,   -- internal user ID (e.g. UUID)
  email         TEXT UNIQUE,
  postal_code   TEXT,               -- For auto-suggesting reps
  federal_riding_id   TEXT,
  provincial_riding_id TEXT,
  created_at    TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  email_digest  TEXT DEFAULT 'weekly'  -- 'daily' | 'weekly' | 'never'
);

CREATE TABLE user_follows (
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  member_id   TEXT REFERENCES members(id) ON DELETE CASCADE,
  created_at  TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  PRIMARY KEY (user_id, member_id)
);

CREATE TABLE bill_reactions (
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  bill_id     TEXT REFERENCES bills(id) ON DELETE CASCADE,
  reaction    TEXT CHECK (reaction IN ('support', 'oppose', 'neutral')),
  note        TEXT,                 -- Optional short comment (500 char max)
  created_at  TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
  PRIMARY KEY (user_id, bill_id)
);

CREATE TABLE policy_submissions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     TEXT REFERENCES users(id),
  member_id   TEXT REFERENCES members(id),
  subject     TEXT,
  body        TEXT,
  category    TEXT,
  submitted_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
  -- NOTE: This table just logs what was sent.
  -- Actual delivery is via mailto: link — no SMTP infra needed for V1.
);

-- Aggregated reaction counts — refreshed by a nightly cron job
-- (SQLite does not support materialized views; use a plain table)
CREATE TABLE bill_reaction_counts (
  bill_id         TEXT PRIMARY KEY REFERENCES bills(id),
  support_count   INTEGER DEFAULT 0,
  oppose_count    INTEGER DEFAULT 0,
  neutral_count   INTEGER DEFAULT 0,
  total_reactions INTEGER DEFAULT 0,
  refreshed_at    TEXT
);

-- Refresh this table nightly via a robfig/cron job (or on-demand triggers)
```

---

### 4.3 Weekly Digest Email

```go
// internal/digest/digest.go — weekly digest for followed MPs
// Sent every Sunday at 8am UTC via Resend.com

package digest

import (
    "bytes"
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
)

const resendAPI = "https://api.resend.com/emails"

type DigestEmail struct {
    From    string `json:"from"`
    To      string `json:"to"`
    Subject string `json:"subject"`
    Html    string `json:"html"`
}

func SendWeeklyDigest(ctx context.Context, db *sql.DB, userID string) error {
    user, err := getUser(db, userID)
    if err != nil {
        return err
    }

    rows, _ := db.QueryContext(ctx, `
        SELECT
            m.name, m.id, m.party,
            d.date, d.description, d.result,
            mv.vote,
            b.number, b.short_title, b.id
        FROM member_votes mv
        JOIN divisions d  ON d.id = mv.division_id
        JOIN members m    ON m.id = mv.member_id
        LEFT JOIN bills b ON b.id = d.bill_id
        WHERE mv.member_id IN (
            SELECT member_id FROM user_follows WHERE user_id = ?
        )
          AND d.date >= date('now', '-7 days')
        ORDER BY m.name, d.date DESC`,
        userID)
    defer rows.Close()

    var recentVotes []VoteDigestRow
    for rows.Next() {
        var v VoteDigestRow
        rows.Scan(&v.MPName, &v.MPID, &v.Party, &v.Date, &v.Description,
            &v.Result, &v.Vote, &v.BillNumber, &v.BillTitle, &v.BillID)
        recentVotes = append(recentVotes, v)
    }

    if len(recentVotes) == 0 {
        return nil // No activity this week — skip
    }

    html := renderDigestHTML(user, groupByMP(recentVotes), time.Now())

    body, _ := json.Marshal(DigestEmail{
        From:    "Open Democracy <digest@open-democracy.ca>",
        To:      user.Email,
        Subject: fmt.Sprintf("Your MPs voted on %d bills this week", len(recentVotes)),
        Html:    html,
    })

    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, resendAPI, bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+mustEnv("RESEND_API_KEY"))
    req.Header.Set("Content-Type", "application/json")
    resp, err := http.DefaultClient.Do(req)
    if err != nil || resp.StatusCode >= 400 {
        return fmt.Errorf("resend error: status %d", resp.StatusCode)
    }
    return nil
}

// SendAllDigests is called by the robfig/cron Sunday 8am job.
func SendAllDigests(ctx context.Context, db *sql.DB) error {
    rows, _ := db.QueryContext(ctx,
        "SELECT id FROM users WHERE email_digest != 'never'")
    defer rows.Close()
    for rows.Next() {
        var id string
        rows.Scan(&id)
        if err := SendWeeklyDigest(ctx, db, id); err != nil {
            log.Printf("[digest] user %s: %v", id, err)
        }
        time.Sleep(100 * time.Millisecond) // Don't hammer Resend
    }
    return nil
}
```

---

### 4.4 Policy Idea Submission — The Mailto Approach

```go
// internal/templates/policy_submit.templ
// Key insight: no SMTP server, no email infra, no spam risk.
// We generate a mailto: link that opens the user's own email client.
// This means the email comes FROM the constituent (which is the point).
// Alpine.js manages the form state and builds the mailto: link in-browser.

templ PolicySubmitForm(member Member) {
    <div
        x-data={ policyFormData(member.Email, member.Name, member.Riding) }
        class="policy-submit-form"
    >
        <h3>Submit a Policy Idea to { member.Name }</h3>

        <select x-model="category">
            for _, cat := range Categories {
                <option value={ cat }>{ cat }</option>
            }
        </select>

        <input
            type="text"
            placeholder="Subject (e.g. Support for affordable housing bill C-47)"
            x-model="subject"
            maxlength="120"
        />

        <textarea
            placeholder="Describe your policy idea or position in your own words..."
            x-model="body"
            maxlength="2000"
            rows="8"
        ></textarea>

        <label>
            <input type="checkbox" x-model="constituencyNote"/>
            Mention I am a constituent in { member.Riding }
        </label>

        <!-- Opens user's email client with pre-filled draft -->
        <a
            :href="mailtoLink()"
            class="btn-primary"
            @click="logSubmission()"
        >
            Open in my email app →
        </a>

        <p class="hint">
            This will open your email client with a pre-drafted message.
            You can edit it before sending. Your email goes directly to { member.Name } — we never see it.
        </p>
    </div>
}
```

```javascript
// The Alpine.js component definition (inlined via x-data or registered globally)
function policyFormData(email, mpName, riding) {
    return {
        category: "Housing",
        subject: "",
        body: "",
        constituencyNote: true,
        mailtoLink() {
            const sub = encodeURIComponent("Constituent Feedback: " + this.subject);
            const txt = [
                `Dear ${mpName},\n\n`,
                this.body,
                `\n\n---\n`,
                this.constituencyNote ? `I am a constituent in ${riding}.\n` : "",
                "Sent via Open Democracy (open-democracy.ca)",
            ].join("");
            return `mailto:${email}?subject=${sub}&body=${encodeURIComponent(txt)}`;
        },
        logSubmission() {
            fetch("/api/log-submission", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify({member_id: mpName, category: this.category}),
            });
        },
    };
}
```

---

### 4.5 Bill Reaction Display — Constituent Sentiment vs Parliamentary Vote

- ✅ Show two bars side by side: "How Parliament voted" (actual Yeas/Nays from the division) and "How constituents feel" (your support/oppose tally). This is the "General concensious" section from the sketch.
- ✅ Require email verification before storing a reaction. One reaction per bill per verified email. Prevents trivial ballot-stuffing.
- ✅ Show total reaction count prominently. A bill with 3 reactions is not meaningful; one with 3,000 is. Show raw numbers, not just percentages.
- ✅ Filter constituent reactions by riding optionally — "How people in your riding feel" is more relevant to an MP than national sentiment.
- ○ Later: let MPs embed a "How should I vote?" widget on their own website that feeds into your DB. This is the original sketch vision — politicians using the platform directly.

---

## Phase 5 — Accountability Layer

**Patterns, scorecards & long-term tracking · 2 weeks**

**Stack:** SQLite analytics queries · Alpine.js + Chart.js · Go · robfig/cron

The highest-value features for civic transparency — not just "how did they vote" but "do they actually represent their constituents, and do they keep their word?" This phase turns raw vote data into meaningful accountability metrics.

---

### 5.1 Party-Line Analysis

```sql
-- For each MP, calculate how often they vote with vs against their party
-- Compatible with SQLite (no Postgres-specific syntax)

WITH party_votes AS (
  -- For each division, find the majority vote of each party
  SELECT
    d.id AS division_id,
    m.party,
    -- SQLite: use a subquery for modal vote instead of MODE() WITHIN GROUP
    (
      SELECT mv2.vote
      FROM member_votes mv2
      JOIN members m2 ON m2.id = mv2.member_id
      WHERE mv2.division_id = d.id
        AND m2.party = m.party
        AND mv2.vote IN ('Yea', 'Nay')
      GROUP BY mv2.vote
      ORDER BY COUNT(*) DESC
      LIMIT 1
    ) AS party_majority_vote
  FROM member_votes mv
  JOIN members m ON m.id = mv.member_id
  JOIN divisions d ON d.id = mv.division_id
  WHERE mv.vote IN ('Yea', 'Nay')
  GROUP BY d.id, m.party
),
mp_alignment AS (
  SELECT
    mv.member_id,
    COUNT(*) AS total_votes,
    SUM(CASE WHEN mv.vote = pv.party_majority_vote THEN 1 ELSE 0 END) AS with_party_votes,
    SUM(CASE WHEN mv.vote != pv.party_majority_vote
              AND mv.vote IN ('Yea', 'Nay') THEN 1 ELSE 0 END) AS rebel_votes
  FROM member_votes mv
  JOIN members m ON m.id = mv.member_id
  JOIN party_votes pv
    ON pv.division_id = mv.division_id
    AND pv.party = m.party
  GROUP BY mv.member_id
)
SELECT
  ma.member_id,
  m.name,
  m.party,
  ma.total_votes,
  ma.with_party_votes,
  ma.rebel_votes,
  ROUND(CAST(ma.with_party_votes AS REAL) / ma.total_votes * 100, 1) AS party_line_pct,
  ROUND(CAST(ma.rebel_votes      AS REAL) / ma.total_votes * 100, 1) AS rebel_pct
FROM mp_alignment ma
JOIN members m ON m.id = ma.member_id
ORDER BY rebel_pct DESC;

-- Use this to surface: "Most independent MPs", "MPs who broke ranks on housing bills", etc.
```

---

### 5.2 Category Scorecards

```sql
-- How did an MP vote across bills in a given category?
-- e.g. "On Housing bills, MP voted Yea 80% of the time"
-- SQLite-compatible (no Postgres FILTER syntax)

SELECT
  mv.member_id,
  b.category,
  COUNT(*) AS votes_on_category,
  SUM(CASE WHEN mv.vote = 'Yea' THEN 1 ELSE 0 END) AS yea_count,
  SUM(CASE WHEN mv.vote = 'Nay' THEN 1 ELSE 0 END) AS nay_count,
  ROUND(
    CAST(SUM(CASE WHEN mv.vote = 'Yea' THEN 1 ELSE 0 END) AS REAL)
    / NULLIF(COUNT(*), 0) * 100, 1
  ) AS yea_pct
FROM member_votes mv
JOIN divisions d ON d.id = mv.division_id
JOIN bills b     ON b.id = d.bill_id
WHERE mv.member_id = ?           -- parameter: specific MP
  AND b.category IS NOT NULL
  AND mv.vote IN ('Yea', 'Nay')
GROUP BY mv.member_id, b.category
ORDER BY votes_on_category DESC;

-- Displayed as a radar chart via Chart.js + Alpine.js on the MP profile page
-- e.g.:
-- Housing:     ████████░░ 82% Yea
-- Environment: █████░░░░░ 48% Yea
-- Defence:     ██████████ 100% Yea
```

---

### 5.3 Accountability Features Roadmap

| Feature | Description | Data Source | Complexity |
|---------|-------------|-------------|------------|
| Voting streak | X consecutive votes with party / against party — surface outliers | member_votes + party analysis query | Low |
| Attendance rate | % of recorded divisions an MP participated in vs. total divisions during their tenure | member_votes vs. divisions table | Low |
| Category drift | Did an MP's voting pattern on Housing change before vs. after an election? | member_votes grouped by date range | Medium |
| Constituency alignment | Compare MP's votes to their constituents' reactions on your platform — do they represent their riding? | bill_reactions (riding-filtered) vs. member_votes | Medium |
| Campaign promise tracker | Tag bills as related to party platform promises; track how MPs voted on them | Manual tagging + member_votes | High (manual curation) |
| Co-voting network | Which MPs vote together most? Visualize as a network graph — reveals cross-party alliances | member_votes co-occurrence matrix | Medium |
| Bill outcomes by sponsor | Does a given MP's bills tend to pass or die in committee? | bills.sponsor_id + bill_stages outcome | Low |
| Absence on controversial votes | MPs who were suspiciously absent on high-profile divisions | missing member_votes on high-attention divisions | Medium |

---

### 5.4 Federal vs. Provincial Side-by-Side

```go
// internal/templates/rep_comparison.templ
// The core UX from the original sketch — orange (provincial) vs blue (federal)
// Shows when you enter your postal code on the home page

templ RepComparison(federalRep, provincialRep Member, overlapPct int) {
    <div class="rep-comparison">

        <!-- Provincial — orange column -->
        <div class="rep-column provincial">
            <div class="rep-column-header">
                <span class="rep-level">Provincial</span>
                @ParliamentStatusPill(provincialRep.ParliamentStatus)
            </div>
            @RepCard(provincialRep)
            @RecentVotesPreview(provincialRep.RecentVotes, 5)
            <a href={ templ.SafeURL("/members/" + provincialRep.ID) }>
                Full voting record →
            </a>
        </div>

        <!-- Divider with overlap score -->
        <div class="rep-divider">
            <div class="overlap-badge">
                <span class="overlap-number">{ fmt.Sprint(overlapPct) }%</span>
                <span class="overlap-label">vote alignment on shared issues</span>
            </div>
        </div>

        <!-- Federal — blue column -->
        <div class="rep-column federal">
            <div class="rep-column-header">
                <span class="rep-level">Federal</span>
                @ParliamentStatusPill(federalRep.ParliamentStatus)
            </div>
            @RepCard(federalRep)
            @RecentVotesPreview(federalRep.RecentVotes, 5)
            <a href={ templ.SafeURL("/members/" + federalRep.ID) }>
                Full voting record →
            </a>
        </div>

    </div>
}

// Note: The "overlap score" only makes sense when the same issue
// appears at both levels. Use shared bill categories as the bridge.
// e.g. both voted on housing-related legislation — compare positions.
// The overlap percentage is computed server-side in the Go handler
// before rendering and passed directly to the template.
```
