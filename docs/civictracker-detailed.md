# CivicTracker — Detailed Implementation Plan

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

**Stack:** Python 3.11 · feedparser · BeautifulSoup4 · APScheduler · SQLite → Postgres · requests-cache

Everything you need exists in public government HTML and RSS. This phase is about writing reliable, scheduled scrapers, defining the data schema, and building a local database you control. No auth, no users, no frontend yet.

---

### 1.1 Data Sources & What Each Gives You

| Source | URL Pattern | What You Get | Method |
|--------|-------------|--------------|--------|
| LEGISinfo RSS | `parl.ca/legisinfo/en/bills/rss` | All active bills: title, sponsor, chamber, stage, last activity date. Updates within hours of a change. | feedparser |
| Bill Detail Page | `parl.ca/legisinfo/en/bill/{parl}-{session}/{id}` | Full stage timeline (1st/2nd/3rd reading, committee, Royal Assent), Hansard debate links, committee meeting dates, amendments | requests + BS4 |
| Bill Full Text | `parl.ca/DocumentViewer/en/{parl}-{session}/bill/{id}/first-reading` | Actual legislative text as HTML. Feed to AI summarizer. | requests + BS4 |
| LoP Legislative Summaries | `lop.parl.ca/sites/PublicWebsite/.../LegislativeSummaries` | Professional researcher plain-English summaries for ~300 major bills/year. Use these instead of AI where available. | requests + BS4 |
| MP Votes Index | `ourcommons.ca/Members/en/votes` | Table: vote #, date, bill, description, Yeas count, Nays count, result. One row per recorded division. | requests + BS4 |
| Individual Vote Detail | `ourcommons.ca/Members/en/votes/{vote_id}` | How every single MP voted (Yea/Nay/Paired/Abstain) on that division. | requests + BS4 |
| MP Profile | `ourcommons.ca/Members/en/{id}` | Photo URL, name, party, riding, province, role, contact email, website. | requests + BS4 |
| MP Vote History (Work tab) | `ourcommons.ca/Members/en/{id}?tab=votes` | Full voting record for that MP — all divisions they participated in. | requests + BS4 (JS-rendered, may need Playwright) |
| Sitting Calendar | `ourcommons.ca/en/sitting-calendar` | Scheduled sitting dates for current session. Used to determine if parliament is in session. | requests + BS4 |
| Senate Votes | `sencanada.ca/en/in-the-chamber/votes` | Senate division records. Same structure as Commons votes. | requests + BS4 |

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
  id            SERIAL PRIMARY KEY,
  bill_id       TEXT REFERENCES bills(id),
  stage         TEXT,
  chamber       TEXT,
  date          DATE,
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

```python
# crawler/main.py  — entry point
# Run with: python -m crawler.main
# Scheduled with APScheduler (see scheduler.py)

import feedparser
import requests
from bs4 import BeautifulSoup
from datetime import datetime
import sqlite3
import time

HEADERS = {"User-Agent": "CivicTracker/1.0 (civic-app.ca; contact@civic-app.ca)"}
# ↑ Always identify yourself to government scrapers. Be polite.

RSS_URL = "https://www.parl.ca/legisinfo/en/bills/rss"

def crawl_bills_from_rss():
    """Step 1: Get all active bills from RSS feed."""
    feed = feedparser.parse(RSS_URL)
    bills = []
    for entry in feed.entries:
        bills.append({
            "id": extract_bill_id(entry.link),   # parse from URL
            "title": entry.title,
            "link": entry.link,
            "updated": entry.get("updated", None),
        })
    return bills

def crawl_bill_detail(bill_id: str, url: str) -> dict:
    """Step 2: Scrape full detail page for a single bill."""
    resp = requests.get(url, headers=HEADERS, timeout=10)
    soup = BeautifulSoup(resp.text, "html.parser")

    # Current status
    status_el = soup.select_one(".bill-latest-activity")
    status = status_el.get_text(strip=True) if status_el else None

    # Stage timeline — LEGISinfo renders each stage as a section with an anchor
    stages = []
    for stage_el in soup.select("[id*='reading'], [id*='committee'], [id*='royal']"):
        stages.append({
            "stage": stage_el.get("id"),
            "date": extract_date_from_sibling(stage_el),
        })

    # Sponsor
    sponsor_el = soup.select_one(".bill-profile-sponsor a")
    sponsor_url = sponsor_el["href"] if sponsor_el else None
    sponsor_id = extract_member_id(sponsor_url) if sponsor_url else None

    return {"status": status, "stages": stages, "sponsor_id": sponsor_id}

def crawl_votes_index():
    """Step 3: Scrape the votes index page."""
    url = "https://www.ourcommons.ca/Members/en/votes"
    resp = requests.get(url, headers=HEADERS, timeout=10)
    soup = BeautifulSoup(resp.text, "html.parser")

    divisions = []
    for row in soup.select("table.table tbody tr"):
        cols = row.select("td")
        if len(cols) < 5: continue
        divisions.append({
            "number": cols[0].get_text(strip=True),
            "date": cols[1].get_text(strip=True),
            "description": cols[2].get_text(strip=True),
            "yeas": int(cols[3].get_text(strip=True) or 0),
            "nays": int(cols[4].get_text(strip=True) or 0),
            "result": cols[5].get_text(strip=True) if len(cols) > 5 else None,
            "detail_url": row.select_one("a")["href"] if row.select_one("a") else None,
        })
    return divisions

def crawl_division_detail(division_url: str) -> list[dict]:
    """Step 4: How each MP voted on a specific division."""
    resp = requests.get(division_url, headers=HEADERS, timeout=10)
    soup = BeautifulSoup(resp.text, "html.parser")

    votes = []
    for mp_el in soup.select(".vote-yea .member-name a"):
        votes.append({"member_id": extract_member_id(mp_el["href"]), "vote": "Yea"})
    for mp_el in soup.select(".vote-nay .member-name a"):
        votes.append({"member_id": extract_member_id(mp_el["href"]), "vote": "Nay"})
    for mp_el in soup.select(".vote-paired .member-name a"):
        votes.append({"member_id": extract_member_id(mp_el["href"]), "vote": "Paired"})
    return votes


# crawler/scheduler.py

from apscheduler.schedulers.blocking import BlockingScheduler

scheduler = BlockingScheduler()

@scheduler.scheduled_job("cron", hour=2, minute=0)   # 2am nightly
def nightly_full_crawl():
    print(f"[{datetime.now()}] Starting nightly crawl...")
    bills = crawl_bills_from_rss()
    for bill in bills:
        detail = crawl_bill_detail(bill["id"], bill["link"])
        upsert_bill(bill, detail)
        time.sleep(0.5)   # Be polite — 0.5s between requests

@scheduler.scheduled_job("cron", hour="*/4")          # Every 4h during sitting days
def frequent_vote_check():
    """Check for new divisions more frequently on sitting days."""
    if parliament_is_sitting():
        new_divisions = crawl_votes_index()
        for div in new_divisions:
            if not division_exists(div["number"]):
                detail = crawl_division_detail(div["detail_url"])
                upsert_division(div, detail)
                time.sleep(0.5)

scheduler.start()
```

---

### 1.4 Rate Limiting & Politeness

- ✅ Set a descriptive User-Agent header identifying your app and contact email. Government IT teams notice anonymous scrapers and may block them.
- ✅ Add 0.5–1 second sleep between requests. Government servers are not CDN-backed; be a good citizen.
- ✅ Use `requests-cache` with a 6-hour TTL for detail pages. Bills don't change hourly.
- ✅ Check `robots.txt` for both `ourcommons.ca` and `parl.ca` before scraping — both are permissive but verify.
- ○ Consider emailing the House of Commons IT team (they have a public address) to let them know about the project. They've been known to provide unofficial data feeds to civic tech projects.
- ○ For the MP vote history 'Work' tab — test if it requires JS rendering. If so, add Playwright as a fallback for that specific endpoint only.

---

## Phase 2 — Read-Only Frontend

**The no-login MVP — clean UI over public data · 2–3 weeks**

**Stack:** Next.js 14 (App Router) · Tailwind CSS · shadcn/ui · Vercel · React Server Components

Build the public-facing UI. No auth, no user accounts, no upvotes yet. Just a beautiful, fast, searchable window into the government data you collected in Phase 1. This is already a publishable, useful product.

---

### 2.1 Page Structure & Routes

| Route | Page Name | Key Components | Data Source |
|-------|-----------|----------------|-------------|
| `/` | Home / Dashboard | Parliament status banner, recent bill activity feed, recent divisions feed, postcode MP lookup widget | DB: bills + divisions + sitting calendar |
| `/bills` | Bills Feed | Category filter tabs, stage filter, search bar, bill cards with progress indicators | DB: bills (paginated, filtered) |
| `/bills/[id]` | Bill Detail | Stage timeline, AI summary, full bill link, vote breakdown table, constituent sentiment (Phase 4) | DB: bill + divisions + member_votes |
| `/votes` | Votes / Divisions | Table of all recorded divisions, sortable by date/result/bill, filterable by parliament | DB: divisions |
| `/members` | MPs Directory | Search by name/riding/province/party, grid or list view | DB: members |
| `/members/[id]` | MP Profile | Photo, bio, contact info, full vote history table, party-line analysis, category breakdown | DB: member + member_votes + divisions |
| `/compare` | Compare MPs | Select 2 MPs side-by-side, voting overlap %, divergence on specific bills | DB: member_votes |
| `/riding/[postal]` | Your Representatives | Enter postcode → see federal + provincial reps, their recent votes, contact links | DB: members + Elections Canada riding lookup |

---

### 2.2 Bill Card & Status Pipeline UI

```jsx
// components/BillCard.jsx
// The core reusable card used everywhere bills appear

const STAGES = [
  { key: "1st_reading",  label: "1st Reading"  },
  { key: "2nd_reading",  label: "2nd Reading"  },
  { key: "committee",    label: "Committee"    },
  { key: "3rd_reading",  label: "3rd Reading"  },
  { key: "royal_assent", label: "Royal Assent" },
];

const CATEGORY_COLORS = {
  Housing:      "#F59E0B",
  Health:       "#EF4444",
  Environment:  "#22C55E",
  Defence:      "#3B82F6",
  Indigenous:   "#8B5CF6",
  Finance:      "#0EA5E9",
  Justice:      "#F97316",
  "Other":      "#6B7280",
};

export function BillCard({ bill }) {
  const stageIndex = STAGES.findIndex(s => s.key === bill.current_stage);
  const progress = ((stageIndex + 1) / STAGES.length) * 100;
  const summary = bill.summary_lop || bill.summary_ai;

  return (
    <article className="bill-card">
      {/* Header row */}
      <div className="bill-card-header">
        <span className="bill-number">{bill.number}</span>
        <span
          className="bill-category"
          style={{ background: CATEGORY_COLORS[bill.category] + "22",
                   color: CATEGORY_COLORS[bill.category],
                   border: `1px solid ${CATEGORY_COLORS[bill.category]}44` }}
        >
          {bill.category}
        </span>
        <span className="bill-type">{bill.bill_type}</span>
      </div>

      {/* Title */}
      <h3 className="bill-title">
        <a href={`/bills/${bill.id}`}>{bill.short_title || bill.title}</a>
      </h3>

      {/* AI summary — 2 lines max, truncated */}
      {summary && (
        <p className="bill-summary">{summary}</p>
      )}

      {/* Stage pipeline */}
      <div className="bill-stages">
        {STAGES.map((stage, i) => (
          <div
            key={stage.key}
            className={`stage-dot ${
              i < stageIndex  ? "stage-done" :
              i === stageIndex ? "stage-current" : "stage-future"
            }`}
            title={stage.label}
          >
            <div className="stage-pip" />
            <span className="stage-label">{stage.label}</span>
          </div>
        ))}
        <div
          className="stage-progress-bar"
          style={{ width: `${progress}%` }}
        />
      </div>

      {/* Footer */}
      <div className="bill-card-footer">
        <span>Sponsored by {bill.sponsor_name}</span>
        <span>Last activity: {formatDate(bill.last_activity_date)}</span>
      </div>
    </article>
  );
}
```

---

### 2.3 Parliament Status Banner

```javascript
// lib/parliament-status.js
// Determines if parliament is currently sitting based on the sitting calendar.
// Shown prominently at the top of every page (the orange/blue sketch detail).

export async function getParliamentStatus() {
  const sittingDates = await db.query(
    "SELECT date FROM sitting_calendar WHERE parliament = 45 AND session = 1 ORDER BY date"
  );

  const today = new Date().toISOString().split("T")[0];
  const todayIsSitting = sittingDates.some(r => r.date === today);
  const nextSitting = sittingDates.find(r => r.date > today);

  return {
    status: todayIsSitting ? "in_session" : "on_break",
    label: todayIsSitting ? "In Session" : "On Break",
    detail: todayIsSitting
      ? "The House is sitting today"
      : nextSitting
        ? `Next sitting: ${formatDate(nextSitting.date)}`
        : "No sitting dates scheduled",
    parliament: 45,
    session: 1,
  };
}

// In layout.jsx — the banner shown on every page
// Matches the orange (provincial) / blue (federal) split from the sketch
export function ParliamentBanner({ federal, provincial }) {
  return (
    <div className="parliament-banner">
      <div className="banner-half banner-provincial">
        <span className="banner-label">Your Provincial Legislature</span>
        <span className={`banner-status ${provincial.status}`}>
          {provincial.label}
        </span>
        <span className="banner-detail">{provincial.detail}</span>
      </div>
      <div className="banner-half banner-federal">
        <span className="banner-label">Parliament of Canada</span>
        <span className={`banner-status ${federal.status}`}>
          {federal.label}
        </span>
        <span className="banner-detail">{federal.detail}</span>
      </div>
    </div>
  );
}
```

---

### 2.4 MP Profile — Vote History Table

```jsx
// app/members/[id]/page.jsx

export default async function MPProfile({ params }) {
  const member = await getMember(params.id);
  const votes  = await getMemberVotes(params.id, { limit: 50 });
  const stats  = await getMemberStats(params.id);

  return (
    <div>
      <MemberHero member={member} />

      <StatsRow stats={[
        { label: "Votes cast this session", value: stats.total_votes },
        { label: "With party",  value: `${stats.party_line_pct}%` },
        { label: "Against party", value: `${stats.rebel_pct}%` },
        { label: "Missed votes", value: `${stats.missed_pct}%` },
      ]} />

      <VoteHistoryTable votes={votes} />
      <CategoryBreakdown memberId={params.id} />
      <ContactSection member={member} />
    </div>
  );
}

function VoteHistoryTable({ votes }) {
  return (
    <table>
      <thead>
        <tr>
          <th>Date</th>
          <th>Bill</th>
          <th>Division Description</th>
          <th>Vote</th>        {/* Yea | Nay | Paired | Abstain */}
          <th>Result</th>      {/* Agreed to | Negatived */}
          <th>Party Line?</th> {/* Did they vote with their party? */}
        </tr>
      </thead>
      <tbody>
        {votes.map(v => (
          <tr key={v.division_id} className={`vote-row ${v.vote.toLowerCase()}`}>
            <td>{formatDate(v.date)}</td>
            <td><a href={`/bills/${v.bill_id}`}>{v.bill_number}</a></td>
            <td>{v.description}</td>
            <td><VoteBadge vote={v.vote} /></td>
            <td>{v.result}</td>
            <td>{v.voted_with_party ? "✓" : <span className="rebel">✗ Broke ranks</span>}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
```

---

### 2.5 Postcode → Riding Lookup

- ✅ Elections Canada publishes a full postcode-to-riding dataset as a free CSV download (`elections.ca`). Import it into your DB as a `postal_ridings` table.
- ✅ Table structure: `postal_code` (TEXT), `federal_riding_id`, `federal_riding_name`, `provincial_riding_id` (if you add provinces later).
- ✅ The lookup is a single DB query: `SELECT * FROM members WHERE riding_id = (SELECT federal_riding_id FROM postal_ridings WHERE postal_code = $1)`. Sub-20ms.
- ✅ For the MVP, just ask the user to type their first 3 postal code characters (FSA) — that narrows to a ~10km radius, sufficient to identify a riding.
- ○ v2: full postcode match for users near riding boundaries.

---

## Phase 3 — AI Summarization

**Make every bill readable in 30 seconds · 1 week**

**Stack:** Claude API (claude-sonnet-4) · Python · tiktoken · lop.parl.ca scraper

Bills are written in dense legalese. Most Canadians will never read one. This phase adds a background worker that fetches bill text, runs it through an LLM, and stores a plain-English summary. The Library of Parliament already does this for major bills — use their summaries first, AI only as fallback.

---

### 3.1 Summary Priority Ladder

- ✅ **TIER 1 — Library of Parliament** (`lop.parl.ca/LegislativeSummaries`): Written by Parliamentary researchers. Authoritative, non-partisan, already plain English. ~300 major bills per parliament get these. Scrape and store as `summary_lop`. Always display this if it exists.
- ✅ **TIER 2 — LEGISinfo bill description**: The short description on the bill's LEGISinfo page is usually 1–3 sentences written by the House of Commons. Good for minor bills that don't get a LoP summary.
- ✅ **TIER 3 — AI-generated summary**: For private member's bills, Senate public bills, and anything without LoP coverage. Generate from bill text using Claude API. Store as `summary_ai`.
- ○ **TIER 4 (future)**: Crowdsourced corrections — let logged-in users flag inaccurate AI summaries and suggest improvements.

---

### 3.2 AI Summarization Pipeline

```python
# summarizer/pipeline.py
import anthropic
import tiktoken

client = anthropic.Anthropic()  # reads ANTHROPIC_API_KEY from env

SYSTEM_PROMPT = """You are a non-partisan Canadian civic education assistant.
Your job is to summarize bills from the Parliament of Canada in plain English.
You must be accurate, neutral, and clear. Never editorialize or express opinions.
Always write for a Canadian high school student — no legal jargon."""

def summarize_bill(bill_id: str, bill_text: str, bill_title: str) -> dict:
    """
    Generate a structured summary for a Canadian parliamentary bill.
    Returns a dict with multiple summary fields for different UI contexts.
    """

    # Truncate if bill text is very long (some bills are 200+ pages)
    enc = tiktoken.get_encoding("cl100k_base")
    tokens = enc.encode(bill_text)
    if len(tokens) > 80_000:
        # Take first 40k + last 10k tokens (preamble + operative clauses)
        bill_text = enc.decode(tokens[:40000]) + "\n\n[...truncated...]\n\n" + enc.decode(tokens[-10000:])

    prompt = f"""
Bill title: {bill_title}

Full text:
{bill_text}

Please provide the following in JSON format (no markdown, raw JSON only):

{{
  "one_sentence": "One sentence (max 25 words) describing what this bill does.",
  "plain_summary": "2–3 paragraph plain-English explanation. What does it do? Who does it affect? Why was it introduced?",
  "key_changes": ["List of 3–6 specific things this bill would change or create"],
  "who_is_affected": ["List of groups, industries, or people most affected"],
  "estimated_cost": "Fiscal impact if mentioned in the bill, or 'Not specified'",
  "category": "One of: Housing, Health, Environment, Defence, Indigenous, Finance, Justice, Agriculture, Transport, Labour, Education, Foreign Affairs, Digital/Tech, Other"
}}
"""

    response = client.messages.create(
        model="claude-sonnet-4-20250514",
        max_tokens=1500,
        system=SYSTEM_PROMPT,
        messages=[{"role": "user", "content": prompt}]
    )

    import json
    result = json.loads(response.content[0].text)
    result["bill_id"] = bill_id
    result["generated_at"] = datetime.utcnow().isoformat()
    result["model"] = "claude-sonnet-4-20250514"
    return result


def summarize_new_bills():
    """Summarize all bills that don't have a summary yet."""
    bills_needing_summary = db.query(
        "SELECT id, title, full_text_url FROM bills "
        "WHERE summary_ai IS NULL AND summary_lop IS NULL "
        "AND full_text_url IS NOT NULL"
    )
    for bill in bills_needing_summary:
        # 1. Check LoP first
        lop = scrape_lop_summary(bill.id)
        if lop:
            db.execute("UPDATE bills SET summary_lop = ? WHERE id = ?", [lop, bill.id])
            continue

        # 2. Fetch bill text
        bill_text = fetch_bill_text(bill.full_text_url)
        if not bill_text:
            continue

        # 3. Generate AI summary
        try:
            summary = summarize_bill(bill.id, bill_text, bill.title)
            db.execute("""
                UPDATE bills
                SET summary_ai = ?, category = ?
                WHERE id = ?
            """, [summary["plain_summary"], summary["category"], bill.id])
        except Exception as e:
            print(f"Summary failed for {bill.id}: {e}")

        time.sleep(1)   # Rate limit
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

**Stack:** Clerk.dev (auth) · Resend (email) · Postgres · Next.js API routes · Elections Canada riding data

This phase adds the engagement layer — accounts, following MPs, upvoting on bills, and the policy idea submission flow. The key design principle: minimize friction. The feedback loop (policy idea → mailto → MP) requires zero backend infrastructure beyond the `mailto:` link itself.

---

### 4.1 Authentication Strategy

| Option | Pros | Cons | Verdict |
|--------|------|------|---------|
| Clerk.dev | Drop-in React components, email/Google/GitHub, free to 10k users, handles sessions + JWTs automatically | Vendor lock-in, paid beyond 10k MAU | ✅ Recommended for MVP |
| Magic.link (email only) | Zero-password UX, simple, privacy-respecting | Slower sign-in flow (check email), no social login | Good minimalist choice |
| NextAuth.js (self-hosted) | Full control, no vendor dependency | More setup, handle sessions yourself | Better for Phase 2+ if you outgrow Clerk |
| localStorage only (no accounts) | Zero friction, works immediately | No cross-device sync, no email notifications | OK for follow/upvote if you skip email notifications |

---

### 4.2 Follow an MP — Data Model & Flow

```sql
-- User tables (add to existing schema)

CREATE TABLE users (
  id            TEXT PRIMARY KEY,   -- Clerk user ID
  email         TEXT UNIQUE,
  postal_code   TEXT,               -- For auto-suggesting reps
  federal_riding_id   TEXT,
  provincial_riding_id TEXT,
  created_at    TIMESTAMP DEFAULT NOW(),
  email_digest  TEXT DEFAULT 'weekly'  -- 'daily' | 'weekly' | 'never'
);

CREATE TABLE user_follows (
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  member_id   TEXT REFERENCES members(id) ON DELETE CASCADE,
  created_at  TIMESTAMP DEFAULT NOW(),
  PRIMARY KEY (user_id, member_id)
);

CREATE TABLE bill_reactions (
  user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
  bill_id     TEXT REFERENCES bills(id) ON DELETE CASCADE,
  reaction    TEXT CHECK (reaction IN ('support', 'oppose', 'neutral')),
  note        TEXT,                 -- Optional short comment (500 char max)
  created_at  TIMESTAMP DEFAULT NOW(),
  PRIMARY KEY (user_id, bill_id)
);

CREATE TABLE policy_submissions (
  id          SERIAL PRIMARY KEY,
  user_id     TEXT REFERENCES users(id),
  member_id   TEXT REFERENCES members(id),
  subject     TEXT,
  body        TEXT,
  category    TEXT,
  submitted_at TIMESTAMP DEFAULT NOW()
  -- NOTE: This table just logs what was sent.
  -- Actual delivery is via mailto: link — no SMTP infra needed for V1.
);

-- Aggregated reaction counts (materialized for fast reads)
CREATE MATERIALIZED VIEW bill_reaction_counts AS
SELECT
  bill_id,
  COUNT(*) FILTER (WHERE reaction = 'support') AS support_count,
  COUNT(*) FILTER (WHERE reaction = 'oppose')  AS oppose_count,
  COUNT(*) FILTER (WHERE reaction = 'neutral') AS neutral_count,
  COUNT(*)                                      AS total_reactions
FROM bill_reactions
GROUP BY bill_id;

-- Refresh this view nightly (or on-demand with triggers)
```

---

### 4.3 Weekly Digest Email

```javascript
// emails/digest.js — weekly digest for followed MPs
// Sent every Sunday at 8am via Resend.com

import { Resend } from "resend";
const resend = new Resend(process.env.RESEND_API_KEY);

async function sendWeeklyDigest(userId) {
  const user = await getUser(userId);
  const followedMPs = await getUserFollows(userId);

  const recentVotes = await db.query(`
    SELECT
      m.name AS mp_name, m.id AS mp_id, m.party,
      d.date, d.description, d.result,
      mv.vote,
      b.number AS bill_number, b.short_title,
      b.id AS bill_id
    FROM member_votes mv
    JOIN divisions d    ON d.id = mv.division_id
    JOIN members m      ON m.id = mv.member_id
    LEFT JOIN bills b   ON b.id = d.bill_id
    WHERE mv.member_id = ANY($1)
      AND d.date >= NOW() - INTERVAL '7 days'
    ORDER BY m.name, d.date DESC
  `, [followedMPs.map(m => m.member_id)]);

  if (recentVotes.length === 0) return;  // Skip if no activity

  const byMP = groupBy(recentVotes, "mp_id");
  const emailHtml = renderDigestEmail({ user, byMP, weekOf: new Date() });

  await resend.emails.send({
    from: "CivicTracker <digest@civic-app.ca>",
    to: user.email,
    subject: `Your MPs voted on ${recentVotes.length} bills this week`,
    html: emailHtml,
  });
}

// Cron: every Sunday 8am
export async function sendAllDigests() {
  const users = await db.query(
    "SELECT id FROM users WHERE email_digest != 'never'"
  );
  for (const user of users) {
    await sendWeeklyDigest(user.id);
    await sleep(100);  // Don't hammer Resend
  }
}
```

---

### 4.4 Policy Idea Submission — The Mailto Approach

```jsx
// components/PolicySubmitForm.jsx
// Key insight: no SMTP server, no email infra, no spam risk.
// We generate a mailto: link that opens the user's own email client.
// This means the email comes FROM the constituent (which is the point).

export function PolicySubmitForm({ member }) {
  const [form, setForm] = useState({
    subject: "",
    category: "Housing",
    body: "",
    constituencyNote: true,
  });

  function buildMailtoLink() {
    const subject = encodeURIComponent(
      `Constituent Feedback: ${form.subject}`
    );
    const body = encodeURIComponent(
      `Dear ${member.name},\n\n` +
      form.body +
      `\n\n---\n` +
      (form.constituencyNote
        ? `I am a constituent in ${member.riding}.\n`
        : "") +
      `Sent via CivicTracker (civic-app.ca)`
    );
    return `mailto:${member.email}?subject=${subject}&body=${body}`;
  }

  return (
    <form onSubmit={e => e.preventDefault()}>
      <h3>Submit a Policy Idea to {member.name}</h3>

      <select value={form.category} onChange={e => setForm({...form, category: e.target.value})}>
        {CATEGORIES.map(c => <option key={c}>{c}</option>)}
      </select>

      <input
        placeholder="Subject (e.g. Support for affordable housing bill C-47)"
        value={form.subject}
        onChange={e => setForm({...form, subject: e.target.value})}
        maxLength={120}
      />

      <textarea
        placeholder="Describe your policy idea or position in your own words..."
        value={form.body}
        onChange={e => setForm({...form, body: e.target.value})}
        maxLength={2000}
        rows={8}
      />

      <label>
        <input
          type="checkbox"
          checked={form.constituencyNote}
          onChange={e => setForm({...form, constituencyNote: e.target.checked})}
        />
        Mention I am a constituent in {member.riding}
      </label>

      {/* Opens user's email client with pre-filled draft */}
      <a
        href={buildMailtoLink()}
        className="btn-primary"
        onClick={() => logSubmission(member.id, form)}  // Log to DB for stats
      >
        Open in my email app →
      </a>

      <p className="hint">
        This will open your email client with a pre-drafted message.
        You can edit it before sending. Your email goes directly to {member.name} — we never see it.
      </p>
    </form>
  );
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

**Stack:** Postgres analytics queries · Recharts / D3.js · Next.js · Scheduled analysis jobs

The highest-value features for civic transparency — not just "how did they vote" but "do they actually represent their constituents, and do they keep their word?" This phase turns raw vote data into meaningful accountability metrics.

---

### 5.1 Party-Line Analysis

```sql
-- For each MP, calculate how often they vote with vs against their party

WITH party_votes AS (
  -- For each division, find the majority vote of each party
  SELECT
    d.id AS division_id,
    m.party,
    MODE() WITHIN GROUP (ORDER BY mv.vote) AS party_majority_vote
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
    COUNT(*) FILTER (
      WHERE mv.vote = pv.party_majority_vote
    ) AS with_party_votes,
    COUNT(*) FILTER (
      WHERE mv.vote != pv.party_majority_vote
      AND mv.vote IN ('Yea', 'Nay')
    ) AS rebel_votes
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
  ROUND(ma.with_party_votes::numeric / ma.total_votes * 100, 1) AS party_line_pct,
  ROUND(ma.rebel_votes::numeric / ma.total_votes * 100, 1)      AS rebel_pct
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

SELECT
  mv.member_id,
  b.category,
  COUNT(*) AS votes_on_category,
  COUNT(*) FILTER (WHERE mv.vote = 'Yea') AS yea_count,
  COUNT(*) FILTER (WHERE mv.vote = 'Nay') AS nay_count,
  ROUND(
    COUNT(*) FILTER (WHERE mv.vote = 'Yea')::numeric
    / NULLIF(COUNT(*), 0) * 100, 1
  ) AS yea_pct
FROM member_votes mv
JOIN divisions d ON d.id = mv.division_id
JOIN bills b     ON b.id = d.bill_id
WHERE mv.member_id = $1           -- parameter: specific MP
  AND b.category IS NOT NULL
  AND mv.vote IN ('Yea', 'Nay')
GROUP BY mv.member_id, b.category
ORDER BY votes_on_category DESC;

-- Displayed as a radar chart / category grid on the MP profile page
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

```jsx
// components/RepComparison.jsx
// The core UX from the original sketch — orange (provincial) vs blue (federal)
// Shows when you enter your postal code on the home page

export function RepComparison({ federalRep, provincialRep }) {
  return (
    <div className="rep-comparison">

      {/* Provincial — orange column */}
      <div className="rep-column provincial">
        <div className="rep-column-header">
          <span className="rep-level">Provincial</span>
          <ParliamentStatusPill status={provincialRep.parliament_status} />
        </div>
        <RepCard member={provincialRep} />
        <RecentVotesPreview
          votes={provincialRep.recent_votes}
          maxItems={5}
        />
        <a href={`/members/${provincialRep.id}`}>
          Full voting record →
        </a>
      </div>

      {/* Divider with overlap score */}
      <div className="rep-divider">
        <div className="overlap-badge">
          <span className="overlap-number">
            {calculateOverlap(federalRep, provincialRep)}%
          </span>
          <span className="overlap-label">vote alignment on shared issues</span>
        </div>
      </div>

      {/* Federal — blue column */}
      <div className="rep-column federal">
        <div className="rep-column-header">
          <span className="rep-level">Federal</span>
          <ParliamentStatusPill status={federalRep.parliament_status} />
        </div>
        <RepCard member={federalRep} />
        <RecentVotesPreview
          votes={federalRep.recent_votes}
          maxItems={5}
        />
        <a href={`/members/${federalRep.id}`}>
          Full voting record →
        </a>
      </div>

    </div>
  );
}

// Note: The "overlap score" only makes sense when the same issue
// appears at both levels. Use shared bill categories as the bridge.
// e.g. both voted on housing-related legislation — compare positions.
```
