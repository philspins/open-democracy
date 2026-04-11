# CivicTracker — Implementation Plan Overview

> A Canadian civic transparency platform built almost entirely on existing open government data.

**5 phases · 10–13 weeks total · 0 paid APIs required for MVP**

---

## Key Insight

The government has already done the hard work. `ourcommons.ca` and `parl.ca/legisinfo` expose structured HTML, RSS feeds, and deep-linkable bill/vote pages. A crawler + a clean UI + an AI summarizer is ~80% of the value — no proprietary data sources needed.

---

## Phase Summary

| # | Phase | Subtitle | Duration | Effort |
|---|-------|----------|----------|--------|
| 1 | Data Foundation | Crawl & Store Public Data | 2–3 weeks | Low–Medium |
| 2 | Read-Only Frontend | The No-Login MVP | 2–3 weeks | Medium |
| 3 | AI Summarization | Make Bills Human-Readable | 1 week | Low |
| 4 | User Features | Accounts, Subscriptions & Feedback | 3–4 weeks | High |
| 5 | Accountability Layer | Patterns, Scorecards & Long-Term Tracking | 2 weeks | Medium |

> **Minimum viable version:** Phase 1 + Phase 2 alone give you something genuinely useful — a clean, searchable view of every bill and every MP vote, auto-updated nightly from public sources. That's publishable in ~5 weeks with one developer.

---

## Phase 1 — Data Foundation
*Crawl & Store Public Data · 2–3 weeks · Low–Medium effort*

### What the government already gives you (free)

| Tag | Name | Detail |
|-----|------|--------|
| `RSS` | **LEGISinfo RSS feed** | `parl.ca/legisinfo/en/bills/rss` — every bill, its status, sponsor, chamber, readings, committee referrals. Zero scraping needed. |
| `HTML parse` | **ourcommons.ca Votes** | `ourcommons.ca/Members/en/votes` — structured HTML table, trivially parseable. Includes vote number, date, bill, Yeas/Nays counts. |
| `HTML parse` | **Member profile + voting history** | `ourcommons.ca/Members/en/123006` — the 'Work' tab has a full voting record per MP. Scrape once on member load, cache. |
| `HTML parse` | **Bill detail page** | `parl.ca/legisinfo/en/bill/45-1/s-209` — current status, stage (1st/2nd/3rd reading, committee), sponsor, summary, Hansard links. |
| `HTML parse` | **Senate equivalents** | `sencanada.ca` mirrors everything above for Senate bills. Same approach. |

### Storage (keep it simple)

| Tag | Name | Detail |
|-----|------|--------|
| `DB` | **SQLite or Postgres** | Tables: bills, votes, members, member_votes. Crawl nightly via cron job. No auth, no user data yet. |
| `Python` | **Crawler stack** | Python + feedparser (RSS) + BeautifulSoup (HTML) + APScheduler for cron. Runs nightly; delta updates only. |

---

## Phase 2 — Read-Only Frontend
*The No-Login MVP · 2–3 weeks · Medium effort*

### Pages to build

| Tag | Name | Detail |
|-----|------|--------|
| `UI` | **Bills feed** | Filterable by category (Housing, Health, Defence…), stage (1st/2nd/3rd reading, committee, Royal Assent), and parliament/session. |
| `UI` | **Bill detail** | AI-generated plain-English summary (see Phase 3), current stage progress bar, how each MP voted, links to Hansard debates. |
| `UI` | **MP profile** | Photo, riding, party, parliament status. Full vote history table sortable by bill/date/how they voted. |
| `UI` | **Vote comparison** | Side-by-side: how your provincial rep voted vs. your federal rep — mirrors the sketch's orange/blue split layout. |

### Nice-to-haves at this stage

| Tag | Name | Detail |
|-----|------|--------|
| `SEO` | **Shareable URLs** | Every bill and every MP vote record gets a clean deep-linkable URL. Essential for social sharing. |
| `Feature` | **Parliament status banner** | Auto-detect if parliament is in session or on break (compare sitting calendar RSS to today's date). Show prominently like in the sketch. |

---

## Phase 3 — AI Summarization
*Make Bills Human-Readable · 1 week · Low effort*

### Approach

| Tag | Name | Detail |
|-----|------|--------|
| `AI` | **Full bill text** | LEGISinfo links directly to the DocumentViewer PDF/HTML. Fetch the first-reading text, chunk it, summarize with Claude or GPT-4o. |
| `Prompt` | **Prompt template** | "Summarize this Canadian bill in 3 sentences a high school student could understand. Then list: who it affects, what it changes, estimated cost if mentioned." |
| `Perf` | **Cache aggressively** | Bill text rarely changes after introduction. Generate summary once on first crawl, store in DB. Re-generate only if bill is amended. |
| `Shortcut` | **Library of Parliament summaries** | For many major bills, `lop.parl.ca/LegislativeSummaries` already has professional researcher summaries. Use those first; fall back to AI. |

---

## Phase 4 — User Features
*Accounts, Subscriptions & Feedback · 3–4 weeks · High effort*

### Follow an MP (core feature)

| Tag | Name | Detail |
|-----|------|--------|
| `Auth` | **Auth** | Email/magic-link auth (no password). Or go fully local — store followed MPs in localStorage for a zero-backend option. |
| `Email` | **Notification digest** | Weekly email: "Your MPs voted on X bills this week. Here's how." One mailto per MP, or Sendgrid/Resend for proper email. |
| `Feature` | **Constituency auto-detect** | Postcode → riding lookup using Elections Canada's open data. Auto-suggest your federal + provincial reps on first visit. |

### Constituent feedback loop

| Tag | Name | Detail |
|-----|------|--------|
| `Feature` | **Upvote on bills** | Thumbs up/down per bill. Tally shown publicly. Requires minimal auth (email verify) to prevent spam. |
| `mailto` | **Submit a policy idea** | Form → generates a pre-filled `mailto:` link to the MP's email (all on `ourcommons.ca/Members/en/addresses`). No backend needed for V1. |
| `UI` | **General consensus display** | Per bill: bar showing % Yea vs Nay from the actual parliamentary vote + constituent sentiment side-by-side. Mirrors sketch's "How many voted / General concensious" section. |

---

## Phase 5 — Accountability Layer
*Patterns, Scorecards & Long-Term Tracking · 2 weeks · Medium effort*

### Voting accountability features

| Tag | Name | Detail |
|-----|------|--------|
| `Analytics` | **Party-line analysis** | For each MP, show: % of votes they voted with their party vs. independently. Rebels get highlighted. |
| `Advanced` | **Broken promises tracker** | If the MP campaigned on X but voted against Y — manual tagging initially, community-submitted eventually. |
| `Analytics` | **Category scorecards** | How did your MP vote across Housing bills? Environment? Indigenous rights? Aggregate by bill category tag. |
| `UI` | **Provincial vs. Federal comparison** | The orange/blue split from the original sketch — show both reps' status and recent votes side by side on the home dashboard. |

---

## Recommended Tech Stack

| Layer | Technology |
|-------|------------|
| Crawler | Python + feedparser + BeautifulSoup + APScheduler |
| Database | SQLite (dev) → Postgres (prod) |
| Backend API | FastAPI or Next.js API routes |
| Frontend | Next.js + Tailwind |
| AI Summary | Claude API (or LoP summaries where available) |
| Email | Resend.com (free tier generous) |
| Auth | Magic.link or Clerk (free tier) |
| Hosting | Vercel (frontend) + Fly.io (crawler) |
