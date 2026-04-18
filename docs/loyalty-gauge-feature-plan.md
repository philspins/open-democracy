# Open Democracy — Loyalty Gauge Feature
## Donor Intelligence, Graph Mapping & Accountability Scoring

> "Who does this politician actually work for?"

---

## Table of Contents

1. [Feature Overview](#1-feature-overview)
2. [The Legal Landscape — What the Data Actually Shows](#2-the-legal-landscape)
3. [Data Acquisition — Federal & Provincial Sources](#3-data-acquisition)
4. [Donor → Organization Entity Resolution](#4-entity-resolution)
5. [Graph Database Architecture](#5-graph-database)
6. [Stance Inference Engine](#6-stance-inference)
7. [The Loyalty Score Algorithm](#7-loyalty-score)
8. [The Gauge UI Component](#8-the-gauge-ui)
9. [Technical Stack & Phasing](#9-stack-and-phasing)
10. [Risks, Caveats & Ethics](#10-risks-and-ethics)

---

## 1. Feature Overview

A **radial gauge** displayed at the top of each MP/MLA profile page. The needle points to one of three options:

```
        PARTY
          ▲
          │
          │
DONORS ◄──┼──► PUBLIC

```
Public = low corruption = green, party = medium corruption = yellow, donors = high corruption = red

The position of the needle is computed from three independent scores:

- **Party alignment** — how often the politician votes with their party (from Phase 5 of the main plan)
- **Donor alignment** — how often their votes benefit their donors' inferred interests
- **Public alignment** — how often their votes match constituent reactions on Open Democracy

The gauge is the **output** of a substantial data pipeline described in this document. Building it requires four major components: donation data ingestion, entity resolution, stance inference, and scoring.

---

## 2. The Legal Landscape

Understanding what the donation data actually represents is critical before building anything on top of it.

### 2.1 Federal Rules (Post-2007)

The Federal Accountability Act (2006) made sweeping changes effective January 1, 2007:

- **Corporations, unions, trade associations, and all organizations are completely banned** from making contributions to federal political entities.
- Only **Canadian citizens and permanent residents** may donate.
- Annual limit: **~$1,725/year** per person to each registered party (indexed, increases $25/year since 2016).
- Elections Canada publishes a **bulk CSV download** of all contributions from 2004 to present, updated weekly.

**The crucial implication:** Federal donation records list *individuals*, not corporations. The analytical challenge — and the core of this feature — is reverse-engineering the corporate/organizational connections behind those individual names. This is exactly what researchers suspect happens: executives donate in their personal capacity to reflect corporate interests.

### 2.2 Provincial Rules — Significant Variation

| Province | Corporations Allowed? | Unions Allowed? | Individual Cap | Notes |
|----------|----------------------|-----------------|----------------|-------|
| Federal | ❌ Banned (2007) | ❌ Banned | ~$1,725/yr | Bulk CSV download available |
| BC | ❌ Banned (2017) | ❌ Banned | $1,250/yr | Web search interface; no bulk export found |
| Alberta | ❌ Banned | ❌ Banned | $4,000/yr | **Bulk CSV/Excel download available** — best provincial source |
| Saskatchewan | ✅ Allowed | ✅ Allowed | No limit | Corporations and out-of-province donors allowed |
| Manitoba | ❌ Banned | ❌ Banned | $3,000/yr | Search interface |
| Ontario | ❌ Banned (2017) | ❌ Banned | $1,650/yr | JS-rendered search app — may need Playwright |
| Quebec | ❌ Banned (1977) | ❌ Banned | **$100/yr** | Lowest cap in country; strictest rules |
| New Brunswick | ✅ Allowed (in-province only) | ✅ Allowed | No stated limit | Corporations must do business in province |
| Nova Scotia | Limited | Limited | $5,000/yr | Recent data gap (nothing newer than 2024 noted) |
| PEI | ✅ Allowed | ✅ Allowed | No limit | |
| Newfoundland | ✅ Allowed | ✅ Allowed | No limit | Recent data gap (nothing newer than 2021 noted) |

**Key insight for BC/Ontario/Federal:** Since corporations are banned, the data shows *people* — but a CEO donating $1,725 personally is functionally representing corporate interests. Entity resolution (Section 4) is how we surface that.

**Key insight for SK/NB/NL/PEI:** Corporate donors appear directly by name. These are the easiest provinces to analyze — the organizational affiliation is already in the record.

### 2.3 The "Straw Donor" Problem

In 2010–2011, it was discovered that corporations had been funnelling money to major provincial political parties by disguising the corporate funds as individual political contributions made by their employees, circumventing the political fundraising laws. This isn't a bug in our analysis — it's the entire point. The goal is to surface these patterns systematically.

---

## 3. Data Acquisition

### 3.1 Federal — Best Source

Elections Canada's Open Data page offers a bulk download of contributions to all political entities from January 2004 to present, updated weekly, in CSV format (compressed ZIP). Two versions: as submitted by entities, and as reviewed/amended by Elections Canada.

```go
// Federal bulk download — no scraping needed
const FederalCSVURL = "https://www.elections.ca/fin/oda/od_cntrbtn_audt_e.zip"

// Fields in the CSV (approximate — verify against data dictionary):
// contributor_first_name, contributor_last_name, contributor_type,
// contributor_province, contribution_amount, contribution_date,
// recipient_name, recipient_type, political_party, electoral_district
```

This is the cleanest, most machine-readable source. Start here.

### 3.2 Provincial Sources — Acquisition Strategy

| Province | Acquisition Method | Format | Complexity |
|----------|-------------------|--------|------------|
| **Federal** | Direct ZIP download (weekly updated) | CSV | ⭐ Trivial |
| **Alberta** | Direct download from efpublic.elections.ab.ca — select event + account type + CSV/Excel | CSV/Excel | ⭐ Trivial |
| **Saskatchewan** | Web form search at elections.sk.ca | HTML table | ⭐⭐ Easy scrape |
| **Manitoba** | electionsmanitoba.ca search | HTML | ⭐⭐ Easy scrape |
| **Quebec** | electionsquebec.qc.ca contributor search | HTML | ⭐⭐ Easy scrape |
| **Ontario** | JS-rendered web app | JSON API (intercept) | ⭐⭐⭐ Need DevTools |
| **BC** | contributions.electionsbc.gov.bc.ca — ASP.NET WebForms with `__doPostBack` | HTML + POST | ⭐⭐⭐ Tricky (session state) |
| **New Brunswick** | electionsnb.ca PDF reports | PDF extraction | ⭐⭐⭐⭐ Painful |
| **Nova Scotia** | electionsnovascotia.ca — data appears stale (pre-2024) | PDF | ⭐⭐⭐⭐ Painful + stale |
| **PEI** | electionspei.ca — JS app | HTML | ⭐⭐⭐ Medium |
| **Newfoundland** | elections.gov.nl.ca — data stale (pre-2021) | PDF | Skip for now |

**Recommended acquisition order:** Federal → Alberta → Saskatchewan → Manitoba → Quebec → Ontario → BC → NB/NS/PEI/NL (defer)

### 3.3 Federal Scraper

```go
// internal/scraper/contributions_federal.go

type ContributionRow struct {
	Source           string
	ContributorLast  string
	ContributorFirst string
	ContributorType  string
	Province         string
	Amount           float64
	Date             string
	Recipient        string
	RecipientType    string
	Party            string
	Riding           string
}

func IngestFederalContributions(ctx context.Context, client *http.Client) ([]ContributionRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FederalCSVURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Open Democracy/1.0 (open-democracy.ca; contact@open-democracy.ca)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	rows, err := parseFederalZip(zipBytes)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func UpsertContributions(ctx context.Context, db *sql.DB, rows []ContributionRow) error {
	// Batch inserts/updates in a transaction against SQLite.
	// Reuse existing store-layer upsert patterns for consistency.
	return nil
}
```

### 3.4 Unified Contributions Schema

```sql
-- Normalized across all federal + provincial sources (SQLite-friendly)

CREATE TABLE contributions (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  source              TEXT NOT NULL,          -- 'federal', 'alberta', 'bc', etc.
  contributor_last    TEXT,
  contributor_first   TEXT,
  contributor_middle  TEXT,
  contributor_type    TEXT,                   -- 'Individual', 'Corporation', 'Union'
  contributor_org     TEXT,                   -- For provinces that allow corporate donations
  city                TEXT,
  province            TEXT,
  postal_code         TEXT,
  amount              REAL,
  contribution_date   TEXT,                   -- ISO date
  recipient           TEXT,                   -- Candidate/party/association name
  recipient_type      TEXT,                   -- 'Candidate', 'Party', 'Association'
  party               TEXT,
  riding              TEXT,
  election_event      TEXT,
  raw_json            TEXT,                   -- Original row JSON for auditing

  -- Resolution outputs (populated later by entity resolution pipeline)
  donor_person_id     TEXT REFERENCES persons(id),
  donor_org_id        TEXT REFERENCES organizations(id),
  resolved_at         TEXT,                   -- RFC3339 timestamp
  resolution_confidence REAL                  -- 0.0–1.0
);

CREATE INDEX idx_contributions_name ON contributions (contributor_last, contributor_first);
CREATE INDEX idx_contributions_party ON contributions (party);
CREATE INDEX idx_contributions_recipient ON contributions (recipient);
CREATE INDEX idx_contributions_date ON contributions (contribution_date);
CREATE INDEX idx_contributions_province ON contributions (province);
```

---

## 4. Entity Resolution

This is the hardest and most important part of the pipeline. Federal donation records list a person's name and province — nothing else. We need to connect `"SMITH, JOHN, Ontario"` to `John Smith, CEO of Acme Corp`.

### 4.1 Resolution Data Sources

| Source | What It Provides | Access |
|--------|-----------------|--------|
| **Canada's Corporate Registry (Corporations Canada)** | Directors and officers of federal corporations by name | Free web search; bulk data via Open Government Portal |
| **Provincial corporate registries** (BC Registry, ONBIS, etc.) | Directors of provincially registered companies | Varies by province; many have free search |
| **LinkedIn** (via profile scraping / enrichment APIs) | Job titles, employers, career history | Rate-limited; use enrichment APIs like People Data Labs |
| **People Data Labs API** | Name + location → employer, title, LinkedIn URL | ~$0.10/lookup; ~1M lookups for full federal donor list |
| **FullContact / Clearbit** | Similar to PDL | Paid |
| **Lobbyist Registry** (lobbyist.gc.ca) | Who lobbies Parliament, on behalf of whom | Free, structured |
| **Board of Directors databases** | BoardEx, TheGlassHammer | Expensive; consider for Phase 2 |
| **News articles** (via Claude web search) | "John Smith, CEO of Acme, donated to..." | AI-assisted, case-by-case |

### 4.2 Resolution Pipeline

```go
// internal/scraper/entity_resolution.go
// Multi-stage entity resolution for individual donors

type ResolvedDonor struct {
	PersonID   string
	FullName   string
	Employer   *string
	EmployerID *string // links to organizations table
	JobTitle   *string
	Confidence float64 // 0.0 - 1.0
	Method     string  // how we resolved this
	Sources    []string
}

func ResolveDonor(ctx context.Context, last, first, province string, amount float64) ResolvedDonor {
	// Stage 1: Exact name match against known politicians, lobbyists, directors
	if result, ok := matchKnownEntities(ctx, last, first, province); ok && result.Confidence > 0.9 {
		return result
	}

	// Stage 2: Corporate registry director lookup
	if result, ok := matchCorporateDirectors(ctx, last, first, province); ok && result.Confidence > 0.85 {
		return result
	}

	// Stage 3: Lobbyist registry lookup
	if result, ok := matchLobbyistRegistry(ctx, last, first); ok && result.Confidence > 0.85 {
		return result
	}

	// Stage 4: People Data Labs enrichment (paid, use sparingly)
	if amount >= 500 {
		if result, ok := enrichViaPDL(ctx, last, first, province); ok && result.Confidence > 0.75 {
			return result
		}
	}

	// Stage 5: AI-assisted web search (for top donors only)
	if amount >= 1000 {
		if result, ok := aiWebSearchResolution(ctx, last, first, province); ok {
			return result
		}
	}

	// Stage 6: Unresolved — store as individual with no org link
	return ResolvedDonor{
		PersonID:   generateID(last, first, province),
		FullName:   strings.TrimSpace(first + " " + last),
		Confidence: 0.0,
		Method:     "unresolved",
		Sources:    nil,
	}
}

func aiWebSearchResolution(ctx context.Context, last, first, province string) (ResolvedDonor, bool) {
	// Call existing AI/web-search integration used elsewhere in the Go services.
	// Parse strict JSON output and require confidence >= 0.7.
	return ResolvedDonor{}, false
}
```

### 4.3 Entity Resolution Schema (Relational Side)

```sql
-- Persons — canonical deduplicated donor records
CREATE TABLE persons (
  id              TEXT PRIMARY KEY,       -- e.g. "person-john-smith-on-a3f2"
  last_name       TEXT NOT NULL,
  first_name      TEXT,
  province        TEXT,
  employer        TEXT,                   -- denormalized for fast display
  job_title       TEXT,
  org_id          TEXT REFERENCES organizations(id),
  linkedin_url    TEXT,
  resolution_method TEXT,
  resolution_confidence FLOAT,
  created_at      TIMESTAMP DEFAULT NOW(),
  updated_at      TIMESTAMP DEFAULT NOW()
);

-- Organizations — companies, unions, associations
CREATE TABLE organizations (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  name_variants   TEXT[],                -- alternate spellings, subsidiaries
  org_type        TEXT,                  -- 'corporation', 'union', 'association', 'non-profit'
  industry        TEXT,                  -- 'Oil & Gas', 'Finance', 'Real Estate', etc.
  industry_naics  TEXT,                  -- NAICS code
  hq_province     TEXT,
  website         TEXT,
  lobbyist_reg_id TEXT,                  -- link to lobbyist registry
  political_stance TEXT,                 -- see Section 6
  stance_confidence FLOAT,
  stance_sources  TEXT[],
  created_at      TIMESTAMP DEFAULT NOW()
);

-- Org membership — person to organization relationships
CREATE TABLE org_memberships (
  person_id   TEXT REFERENCES persons(id),
  org_id      TEXT REFERENCES organizations(id),
  role        TEXT,                      -- 'CEO', 'Director', 'Employee', 'Shareholder', etc.
  start_date  DATE,
  end_date    DATE,
  is_current  BOOLEAN DEFAULT TRUE,
  source      TEXT,
  PRIMARY KEY (person_id, org_id, role)
);
```

---

## 5. Graph Database Architecture

### 5.1 Why a Graph Database?

A relational DB can answer: *"Which donations did John Smith make?"*

A graph database answers: *"Show me every path between MP Jane Doe and the oil industry, through any combination of donations, board memberships, lobbyist contacts, and voting patterns."*

This is exactly the Panama Papers / TrumpWorld model — the ICIJ used Neo4j to surface the connection between an Icelandic PM and an undisclosed offshore account within hours of loading the data. The same pattern applies here.

### 5.2 Recommended Tool: Neo4j Community Edition

Neo4j Community is open source (GPL v3), free to self-host, and uses **Cypher** as its query language. It's the established standard for this kind of political network analysis.

**Alternative:** Memgraph — fully Cypher-compatible, in-memory, arguably faster for reads, also open source. Drop-in Neo4j replacement for this use case.

### 5.3 Node Types

```cypher
// ── NODE LABELS ──────────────────────────────────────────────────

(:Politician {
  id, name, party, riding, province, level,   // 'federal' | 'provincial'
  chamber, photo_url, ourcommons_id
})

(:Person {
  id, full_name, province,
  employer, job_title,                         // denormalized from resolution
  resolution_confidence
})

(:Organization {
  id, name, org_type, industry,
  political_stance, stance_confidence,
  hq_province, lobbyist_reg_id
})

(:Party {
  id, name, abbreviation, level,               // 'federal' | 'provincial'
  ideology                                      // 'left' | 'centre-left' | 'centre' | etc.
})

(:Bill {
  id, number, title, category, chamber,
  current_stage, parliament, session
})

(:Division {
  id, number, date, description,
  yeas, nays, result
})

(:Lobby {
  id, registrant, client_org_id,
  subject, bill_id,
  registration_date, communication_date
})
```

### 5.4 Relationship Types

```cypher
// ── RELATIONSHIP TYPES ───────────────────────────────────────────

// Donations
(:Person)-[:DONATED_TO {amount, date, source}]->(:Politician)
(:Person)-[:DONATED_TO {amount, date, source}]->(:Party)
(:Organization)-[:DONATED_TO {amount, date, source}]->(:Politician)  // SK, NB, NL, PEI

// Organizational ties
(:Person)-[:WORKS_FOR {role, is_current}]->(:Organization)
(:Person)-[:DIRECTOR_OF {since}]->(:Organization)
(:Organization)-[:SUBSIDIARY_OF]->(:Organization)
(:Organization)-[:LOBBIED {subject, date}]->(:Politician)
(:Organization)-[:LOBBIED_ON {date}]->(:Bill)

// Parliamentary activity
(:Politician)-[:MEMBER_OF {since}]->(:Party)
(:Politician)-[:VOTED {vote}]->(:Division)
(:Politician)-[:SPONSORED]->(:Bill)
(:Division)-[:ON_BILL]->(:Bill)

// Stances
(:Organization)-[:SUPPORTS]->(:Bill)    // inferred stance
(:Organization)-[:OPPOSES]->(:Bill)     // inferred stance
(:Party)-[:SUPPORTS]->(:Bill)
(:Party)-[:OPPOSES]->(:Bill)
```

### 5.5 Example Cypher Queries

```cypher
// ── Q1: All organizations connected to a politician's donors ──
MATCH (pol:Politician {id: "123006"})
      <-[:DONATED_TO]-(p:Person)
      -[:WORKS_FOR|DIRECTOR_OF]->(org:Organization)
RETURN org.name, org.industry, org.political_stance,
       COUNT(p) AS num_connected_donors,
       SUM(p.total_donated) AS total_via_this_org
ORDER BY total_via_this_org DESC

// ── Q2: Find industry concentration in a politician's donations ──
MATCH (pol:Politician {id: "123006"})
      <-[d:DONATED_TO]-(p:Person)
      -[:WORKS_FOR]->(org:Organization)
RETURN org.industry,
       COUNT(DISTINCT p) AS num_donors,
       SUM(d.amount) AS total_donated
ORDER BY total_donated DESC

// ── Q3: Voting alignment with donor industries ──
// Which bills did the politician vote Yea on,
// where their donors' orgs have a SUPPORTS relationship?
MATCH (pol:Politician {id: "123006"})
      -[:VOTED {vote: "Yea"}]->(div:Division)
      -[:ON_BILL]->(bill:Bill)
      <-[:SUPPORTS]-(org:Organization)
      <-[:WORKS_FOR]-(p:Person)
      -[:DONATED_TO]->(pol)
RETURN bill.number, bill.title, bill.category,
       COUNT(DISTINCT org) AS supporting_donor_orgs,
       SUM(p.total_donated) AS total_from_aligned_donors
ORDER BY total_from_aligned_donors DESC

// ── Q4: The full loyalty path — one query ──
// Given MP, find: party votes, donor-aligned votes, public-aligned votes
MATCH (pol:Politician {id: $mp_id})-[:VOTED]->(div:Division)-[:ON_BILL]->(bill:Bill)
WITH pol, div, bill
OPTIONAL MATCH (pol)-[:MEMBER_OF]->(party:Party)-[:SUPPORTS|OPPOSES]->(bill)
OPTIONAL MATCH (pol)<-[:DONATED_TO]-(p:Person)-[:WORKS_FOR]->(org:Organization)-[:SUPPORTS|OPPOSES]->(bill)
OPTIONAL MATCH (bill)<-[:REACTED_TO {reaction: "support"}]-(user:User)
RETURN pol.name,
       COUNT(DISTINCT div) AS total_votes,
       COUNT(DISTINCT CASE WHEN party IS NOT NULL THEN div END) AS party_aligned_votes,
       COUNT(DISTINCT CASE WHEN org IS NOT NULL THEN div END) AS donor_aligned_votes
```

### 5.6 SQLite + Neo4j Together

Keep SQLite as the **operational database** (bills, votes, users, session data). Use Neo4j as the **analytical layer** (influence mapping, loyalty scoring). Sync nightly from SQLite → Neo4j.

```
SQLite (operational)     →   ETL nightly   →   Neo4j (influence graph)
  bills, votes, members                          donors, orgs, stances,
  users, reactions                               loyalty scores
```

---

## 6. Stance Inference Engine

Once we have organizations linked to donors, we need to know: *what position would this organization likely take on a given bill?*

### 6.1 Industry → Bill Category Stances

The first layer is heuristic — industry predicts stance on many bills reliably:

```go
// internal/summarizer/industry_stances.go

type IndustryCategoryKey struct {
	Industry string
	Category string
}

var IndustryBillStances = map[IndustryCategoryKey]string{
	{Industry: "Oil & Gas", Category: "Environment"}:  "oppose",
	{Industry: "Oil & Gas", Category: "Energy"}:       "support_if_pro_industry",
	{Industry: "Real Estate", Category: "Housing"}:    "oppose_if_rent_control",
	{Industry: "Finance", Category: "Finance"}:        "oppose_if_regulation",
	{Industry: "Pharma", Category: "Health"}:          "support_if_patent_protection",
	{Industry: "Telecom", Category: "Digital/Tech"}:   "oppose_if_regulation",
	{Industry: "Defence", Category: "Defence"}:        "support_if_spending",
	{Industry: "Agriculture", Category: "Agriculture"}:"support_if_subsidy",
	{Industry: "Labour/Unions", Category: "Labour"}:   "support_if_pro_worker",
	{Industry: "Tech", Category: "Digital/Tech"}:      "oppose_if_privacy_regulation",
}
```

### 6.2 AI Stance Analysis for Specific Bills

For each bill + organization pair where we have enough context:

```go
// internal/summarizer/stance_inference.go

type InferredStance struct {
	Stance     string   `json:"stance"` // support|oppose|neutral|unknown
	Confidence float64  `json:"confidence"`
	Reasoning  string   `json:"reasoning"`
	Sources    []string `json:"sources"`
}

func InferOrgStanceOnBill(ctx context.Context, org Organization, bill Bill) InferredStance {
	// Call Claude through the existing Go summarization client and require strict JSON output.
	// Prompt should request support/oppose/neutral/unknown plus confidence and URLs.
	// If parsing fails or evidence is weak, return unknown with confidence 0.0.
	return InferredStance{Stance: "unknown", Confidence: 0.0}
}
```

### 6.3 Lobbyist Registry as Ground Truth

The federal Lobbyist Registry (`lobbyist.gc.ca`) is the most reliable source of organizational stances — organizations literally register what bills and policy areas they're trying to influence, and on whose behalf.

```go
// internal/scraper/lobbyist_registry.go

const LobbyistAPI = "https://lobbycanada.gc.ca/app/secure/ocl/lrs/do/vwRg"

func FetchLobbyingActivity(ctx context.Context, orgName string) ([]LobbyActivity, error) {
	// The lobbyist registry has a search API and downloadable datasets
	// at open.canada.ca/data — use bulk download ingestion in Go for efficiency.
	return nil, nil
}
```

---

## 7. The Loyalty Score Algorithm

### 7.1 Three Component Scores

```go
// internal/server/loyalty_scores.go

type LoyaltyScores struct {
	PoliticianID string     `json:"politician_id"`
	PartyScore   float64    `json:"party_score"`
	DonorScore   *float64   `json:"donor_score"`
	PublicScore  *float64   `json:"public_score"`
	DonorN       int        `json:"donor_n"`
	PublicN      int        `json:"public_n"`
	Dominant     string     `json:"dominant"`
	ComputedAt   time.Time  `json:"computed_at"`
}

func ComputeLoyaltyScores(ctx context.Context, politicianID string, lookbackDays int) (LoyaltyScores, error) {
	// PARTY SCORE: party line percentage / 100.
	// DONOR SCORE: compare vote directions to donor-org stance consensus.
	// PUBLIC SCORE: compare vote directions to constituent reaction consensus.
	// Scores remain independent (do not need to sum to 1.0).
	// Return nil pointers for donor/public scores when sample size is insufficient.
	return LoyaltyScores{
		PoliticianID: politicianID,
		ComputedAt:   time.Now().UTC(),
	}, nil
}
```

### 7.2 Score Interpretation

| Donor Score | Interpretation |
|-------------|----------------|
| > 0.75 | Strong donor alignment — votes frequently match donor interests |
| 0.55–0.75 | Moderate donor alignment |
| 0.45–0.55 | Ambiguous — no clear pattern |
| < 0.45 | Votes frequently *against* donor interests |
| `null` | Insufficient data (< 10 bills with resolved donor stances) |

**Important caveat to display in UI:** A high donor score does not prove corruption. It may reflect shared values between politician and donors, or donors supporting politicians whose pre-existing views align with industry interests. The score shows correlation, not causation.

---

## 8. The Gauge UI Component

### 8.1 Visual Design

```go
// internal/templates/loyalty_gauge.templ (Templ + Alpine.js approach)

templ LoyaltyGauge(scores LoyaltyScores, politicianID string) {
	<div class="loyalty-gauge-container"
	     x-data="{ expanded: false }">
		<h3 class="gauge-title">Loyalty Analysis</h3>
		<svg viewBox="0 0 200 110" class="gauge-svg">
			@GaugeArcs(scores)
			@GaugeNeedle(scores)
			<text x="28" y="95" class="gauge-label" fill="#F59E0B">DONORS</text>
			<text x="88" y="40" class="gauge-label" fill="#6366F1">PARTY</text>
			<text x="155" y="95" class="gauge-label" fill="#22C55E">PUBLIC</text>
		</svg>

		<div class="gauge-scores">
			@ScoreBar("Party alignment", scores.PartyScore, "#6366F1", 0)
			@ScoreBar("Donor alignment", ptrOrZero(scores.DonorScore), "#F59E0B", scores.DonorN)
			@ScoreBar("Public alignment", ptrOrZero(scores.PublicScore), "#22C55E", scores.PublicN)
		</div>

		if scores.DonorN < 10 || scores.PublicN < 10 {
			<p class="gauge-warning">⚠ Insufficient data for a reliable loyalty reading.</p>
		}

		<p class="gauge-caveat">
			Alignment scores show correlation only — not proof of improper influence.
			<a href="/methodology">Methodology →</a>
		</p>

		<details x-bind:open="expanded" x-on:toggle="expanded = $event.target.open">
			<summary>Top donor industries</summary>
			@DonorIndustryRows(politicianID)
		</details>
	</div>
}
```

### 8.2 Additional UI Elements on the Profile Page

Below the gauge, show a **Donor Network Panel**:

```
┌─ TOP DONOR INDUSTRIES ──────────────────────────────┐
│  Oil & Gas        ████████████  $42,300  (18 donors) │
│  Real Estate      ████████      $28,100  (11 donors) │
│  Finance          ██████        $21,500  (9 donors)  │
│  Agriculture      ████          $14,200  (6 donors)  │
│  Healthcare       ██            $8,900   (4 donors)  │
└─────────────────────────────────────────────────────┘

┌─ VOTES THAT ALIGNED WITH TOP DONORS ───────────────┐
│  C-47  Housing Act         Voted: YEA  Donors: OPPOSE │  ← good for public!
│  C-63  Online Harms Act    Voted: YEA  Donors: OPPOSE │  ← good for public!
│  C-91  Oil Subsidies       Voted: YEA  Donors: SUPPORT│  ← aligned with donors
│  C-12  Carbon Tax          Voted: NAY  Donors: OPPOSE │  ← aligned with donors
└─────────────────────────────────────────────────────┘
```

---

## 9. Technical Stack & Phasing

### Phase A — Data Ingestion (3–4 weeks)
- Federal CSV bulk download → SQLite `contributions` table
- Alberta, Saskatchewan, Manitoba CSV/scrape → same table
- Basic `persons` + `organizations` tables
- Rule-based entity resolution (corporate registry matching only)

### Phase B — Graph Setup (2 weeks)
- Install Neo4j Community (or Memgraph) alongside SQLite
- ETL: sync members, bills, votes, contributions → graph
- Basic Cypher queries for industry concentration per politician

### Phase C — Entity Resolution (4–6 weeks)
- Corporate registry integration (Corporations Canada bulk data)
- People Data Labs / enrichment API integration for top donors (>$500)
- AI-assisted resolution for top 1% of donors by total amount
- Target: resolve employer for ~40–60% of federal donors

### Phase D — Stance Inference (3–4 weeks)
- Industry heuristic stances
- Lobbyist registry integration
- AI stance analysis for top org/bill combinations
- Store stance confidence scores

### Phase E — Scoring + UI (2 weeks)
- Loyalty score computation jobs (nightly)
- Gauge component
- Donor industry breakdown panel
- Methodology page explaining the scoring

### Total: ~15–19 weeks after Phase 1–2 of main plan

### Stack Additions

| Layer | Technology | Notes |
|-------|-----------|-------|
| Graph DB | Neo4j Community (GPL) or Memgraph | Both use Cypher; Memgraph is faster in-memory |
| ETL (SQLite → Neo4j) | `neo4j-go-driver` + existing Go scheduler/cron jobs | Nightly sync |
| Entity resolution | People Data Labs API | ~$0.10/lookup; budget $5–10k for initial load |
| Corporate registry | Corporations Canada bulk data (Open Gov Portal) | Free |
| Lobbyist registry | lobbyist.gc.ca + open.canada.ca bulk download | Free |
| Graph visualization | Neo4j Bloom (dev) / D3.js force graph (prod UI) | |

---

## 10. Risks, Caveats & Ethics

### 10.1 What This Analysis Can and Cannot Show

| Can Show | Cannot Show |
|----------|-------------|
| Statistical correlation between donor industries and voting patterns | Quid pro quo arrangements or explicit agreements |
| Which industries are concentrated among a politician's donors | Whether a politician changed their vote because of donations |
| How often a politician votes in a way that benefits their donors | Causation (a politician may have always held these views before being donated to) |
| Whether voting aligns more with party, donors, or public | Whether correlation is meaningful or coincidental |

### 10.2 Legal Considerations

- **Defamation risk:** Never state that a politician was bribed or corrupted. Present scores as statistical observations with explicit methodology disclosure. The methodology page is not optional — it's legal protection.
- **Privacy:** Donor names are public record (published by Elections Canada). However, the *inference* of their employer is not always in the public record. Flag resolution confidence and method in the UI.
- **Data accuracy:** Contributions data can contain errors. Always link back to the source record. Provide a mechanism for individuals to flag incorrect entity resolutions.

### 10.3 The Straw Donor Problem

In provinces where corporate donations are banned, donations must be personal. But history shows corporations sometimes direct employees to donate. Our analysis can surface *patterns* (10 executives from the same company all maxing out their contributions to the same candidate) but cannot confirm individual intent. Surface these patterns clearly and let readers draw conclusions.

### 10.4 Data Gaps

- Nova Scotia: nothing newer than 2024 per your notes
- Newfoundland: nothing newer than 2021
- These are real gaps. Note them prominently in the UI — a politician who represents these provinces will show incomplete donor data.
- Provincial data is for *provincial* politicians only. Federal candidates are covered by Elections Canada regardless of which province they represent.

### 10.5 Minimum Viable Accountability

Even before the full graph pipeline is built, Phase A alone (ingesting federal contribution CSVs) allows you to display:

- **Total donations received by each federal politician**, sorted by amount
- **List of all individual donors** with their province
- **Donation totals by party** for context

This is genuinely useful civic information with zero entity resolution required, and it can ship within the first 2 weeks of work on this feature.
