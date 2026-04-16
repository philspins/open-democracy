# Claim-Focused Patentability Memo — Open Democracy / CivicTracker

**Prepared for:** Phil Craig / Legal Counsel  
**Prepared by:** GitHub Copilot (AI analysis — review with a registered Canadian patent agent before filing)  
**Date:** 16 April 2026  
**Applicable jurisdiction:** Canada (primary); USPTO and PCT noted where relevant  
**Governing law:** *Patent Act*, R.S.C. 1985, c. P-4, ss. 2, 27, 28.2, 28.3; CIPO Manual of Patent Office Practice (MOPOP) Chapter 17

---

## IMPORTANT PRELIMINARY NOTICE

This memo is an internal analysis only. It is **not legal advice**. Canadian law requires novelty at the time of filing (s. 28.2), and a 12-month grace period applies only to the applicant's own disclosures. Because this repository was briefly public, you must disclose that fact to your patent agent immediately so they can assess whether the grace period was triggered and whether any prior-art clock is running.

---

## Executive Summary

After reviewing all source files in the repository, **three feature clusters contain technically novel and potentially non-obvious elements** worth discussing with a patent agent. Two additional clusters are useful but appear to be combinations of well-known techniques with lower patentability prospects. The memo analyses each feature separately with draft claim language, claim type recommendations, and a specific prior-art search checklist.

---

## Part I — Canadian Patentability Framework (Applied)

Under s. 2 of the *Patent Act*, a patentable "invention" must be a **new and useful art, process, machine, manufacture, or composition of matter**. Software claims are eligible if they claim a practical, computer-implemented process that produces a technical result beyond abstract mental steps (*Amazon.com Inc. v. Canada (Attorney General)*, 2011 FCA 328; MOPOP § 17.02.04). The three mandatory tests are:

1. **Novelty** (s. 28.2): Subject-matter must not be publicly available before the claim date.
2. **Non-obviousness** (s. 28.3): Must not be obvious to a person skilled in the field.
3. **Utility** (s. 2 + s. 27(3)): Must be functional and operative as described.

*Excluded:* abstract algorithms, scientific principles, methods of doing business in the abstract, mental steps (s. 27(8)).

---

## Part II — Feature-by-Feature Analysis

---

### Feature A — Tiered Authoritative Summary Pipeline with Change-Triggered Re-Summarization

**Source files:** `internal/summarizer/pipeline.go`, `internal/summarizer/lop_scraper.go`, `cmd/crawler/main.go`

**Technical description:**

The system implements a three-tier legislative-text summarization pipeline:

1. **Tier 1 (LoP scrape):** Before any AI inference, the crawler fetches and stores pre-existing authoritative plain-English summaries from the Library of Parliament (LoP) website. Bills with LoP summaries never reach the AI.
2. **Tier 2 (AI generation):** For bills without a LoP summary, the system fetches the full legislative text, performs rune-safe truncation preserving the first 120,000 characters plus the last 30,000 characters, and submits to a large language model (LLM) with a structured civic-education system prompt demanding valid JSON output with specific fields.
3. **Tier 3 (Change-triggered re-summarization):** The function `shouldSummarizeBill` compares the bill's `last_activity_date` against the `generated_at` timestamp stored inside the previously generated JSON summary. Re-summarization is triggered only when the bill has been amended after the prior summary was generated — not on a fixed schedule. If a LoP summary appears after an AI summary was generated, the LoP summary takes precedence and AI re-summarization is permanently suppressed.

Additionally: a concurrent channel-based pipeline allows the crawl and summarization workers to operate in parallel — as bills are scraped they are emitted onto a `chan BillSummaryRequest` consumed by N worker goroutines; the crawler closes the channel at completion and waits for the summarizer drain.

**Potential claim types:**

- **Method claims:** A computer-implemented method for maintaining legislative bill summaries comprising: (a) querying an authoritative parliamentary research source for a pre-existing plain-language summary; (b) if no authoritative summary exists, generating an AI summary from the full legislative text with document-boundary-preserving truncation; (c) persisting a generation timestamp with the AI summary; (d) upon a subsequent detected change to the legislative record, comparing a last-activity date of the bill to the stored generation timestamp and triggering re-summarization only when the bill was modified after summary generation; and (e) suppressing AI re-summarization in favour of any subsequently acquired authoritative summary.
- **System claims:** A system for legislative bill summarization comprising a crawling pipeline, a tiered summary store, a priority comparator, and a re-summarization gate.

**Patentability assessment:** The change-triggered re-summarization gate and the automatic demotion of AI content in favour of newly detected authoritative content is technically specific and directed at a concrete computer-implemented process. This goes beyond a mere algorithm; it produces a specific data state change (summary source switching) with a practical civic-information result. **Moderate-to-good prospects** — most likely the strongest claim in the application.

---

### Feature B — Coordinated Concurrent Multi-Domain Government Data Crawl via Buffered-Channel Semaphore

**Source files:** `cmd/crawler/main.go`, `internal/scheduler/scheduler.go`

**Technical description:**

The crawler runs five independent domain scrapers (bills, votes, Senate, members, sitting calendar) in parallel, controlled by a buffered-channel semaphore (`runParallel`). Parallelism is tunable at runtime via a flag or environment variable. Each domain scraper uses an injected test-server URL to allow unit testing without network access. The scheduler component (`scheduler.Start`) coordinates two separate recurring jobs: a nightly full-crawl and a more frequent (every 4h) targeted vote-check — the nightly job also orchestrates LoP summary download before AI summarization begins, ensuring the priority logic is correct.

A concurrent producer/consumer channel links the bill-crawl goroutine to the summarizer worker pool — bills are emitted as they are scraped, consumed by the summarizer concurrently, and the pipeline waits for a clean drain before exiting.

**Patentability assessment:** The general pattern of parallel web crawling with a semaphore and channel-based pipelines is well-known in Go and in web-crawling literature broadly. The specific architectural combination of priority-ordered scheduled jobs, injectable test URLs, and producer/consumer channel linking two different processing domains is more specific, but would likely be considered an obvious adaptation of known patterns to a person skilled in this field. **Weak standalone** — potentially supportable only as a dependent claim under Feature A or Feature C.

---

### Feature C — Address-to-Dual-Jurisdiction Representative Identification with Local Database Reconciliation

**Source files:** `internal/riding/riding.go`, `internal/server/user_pages.go`, `internal/store/store.go`

**Technical description:**

The system geocodes a free-text Canadian address via Google Maps API to latitude/longitude coordinates, then queries the OpenNorth Represent API to obtain a list of representatives at that location. From the returned representative list, the system performs a secondary reconciliation: it matches each federal representative (MP) back to an internal database record by conducting a case-insensitive name comparison between the OpenNorth-returned name and locally stored member records indexed by riding name. The result is that the external representative object is enriched with an internal `LocalMemberID` — enabling deep-linking to the app's own vote-history and statistics pages.

A fallback path handles failures from the live geocoding/representative API by constructing a `LookupResult` from the user's persisted `federal_riding_id` and `provincial_riding_id` fields, querying the local members table directly, and synthesizing a representative object — so the UI degrades gracefully without exposing an error to the user.

**Potential claim types:**

- **Method claims:** A computer-implemented method for identifying a user's elected representatives comprising: (a) geocoding a free-text civic address to geographic coordinates; (b) querying a representative lookup service with the coordinates to retrieve a list of representatives; (c) for each representative at the federal level, querying a local legislative database using the riding name as a search key; (d) matching returned records to the external representative object by case-insensitive name comparison; and (e) augmenting the representative record with a local identifier from the database, thereby enabling cross-reference to locally stored legislative activity records associated with the identified representative.
- The fallback path (constructing synthetic representative objects from persisted riding IDs when live services are unavailable) could be claimed as a dependent claim.

**Patentability assessment:** The multi-source reconciliation between an external geocoding/representative API result and a local legislative database — particularly the name-based fuzzy join to produce an enriched cross-reference — is a specific, practical computer-implemented process. Address-to-representative lookup systems exist (GovTrack, WhoIsMyRepresentative, OpenNorth itself), but the specific reconciliation layer linking the external result to locally indexed legislative voting records may be novel. **Moderate prospects** — depends heavily on prior-art search results (see checklist below).

---

### Feature D — Civic Engagement with Constituent Reaction Aggregation and Real-Time Count Maintenance

**Source files:** `internal/store/store.go` (`ReactToBill`, `GetBillReactionCounts`), `internal/server/server.go`

**Technical description:**

Authenticated users can react to a bill (support / oppose / neutral) with an optional short note. The `ReactToBill` function executes a single database transaction that: (a) upserts the per-user reaction; and (b) in the same transaction, re-aggregates total reaction counts from the `bill_reactions` table and upserts the `bill_reaction_counts` summary row. This pattern avoids deferred batch aggregation and maintains counts atomically within the same write transaction. A separate `policy_submissions` table logs user-drafted policy messages (subject, body, category) associated with a specific member for audit purposes.

**Patentability assessment:** Transactional aggregation updates are a standard database pattern. The specific application to legislative reactions with real-time count maintenance is unlikely to be novel given widespread prior art in social/civic platforms (GovTrack, Change.org, IssueVoter, Represent.us, OpenParliament.ca). **Low patentability prospects as a standalone feature.**

---

### Feature E — Party-Line Voting Deviation Analysis with Batch-Fetch Optimization

**Source files:** `internal/store/store.go` (`GetMemberVotes`, `GetMemberStats`), documentation

**Technical description:**

For a given member, the system computes whether each vote was "with party" or "against party" by: (a) fetching all the member's votes; (b) issuing a single batched SQL query using `IN (...)` placeholders to compute the party-majority direction per division across all other party members simultaneously; and (c) comparing the member's individual vote to the computed party majority for each division. `GetMemberStats` extends this to produce aggregate statistics: total votes cast, party-line percentage, rebel percentage, and missed-vote percentage relative to total divisions in the current parliament/session.

A cross-member vote-agreement percentage (`CompareMemberVotes`) is implemented as a single SQL query joining `member_votes` twice on the same `division_id`.

**Patentability assessment:** Party-line vote deviation analysis is well-known in political science and prior legislative tracking platforms (GovTrack.us has published this feature since ~2004; OpenParliament.ca for Canada; VoteView for the US Congress). The specific batch-fetch SQL optimization pattern is a standard database programming technique. **Very low patentability prospects.**

---

## Part III — Recommended Claim Structure for Filing

If you proceed, we recommend a single patent application containing:

**Independent claims (strongest first):**
1. The tiered legislative summarization method (Feature A — change-triggered re-summarization gate + authoritative source demotion of AI content).
2. The address-to-dual-jurisdiction representative identification with local database reconciliation (Feature C).

**Dependent claims:**
3. The AI truncation method from Feature A (rune-safe boundary preservation with 120k/30k split).
4. The concurrent channel-based crawl-to-summarize pipeline from Feature B, dependent on the independent method of Feature A.
5. The graceful-degradation fallback representative construction from Feature C (dependent on independent claim 2).

**System/apparatus claims:** Mirror the method claims as system claims to capture both method and apparatus infringement.

---

## Part IV — Prior-Art Search Checklist

Your patent agent should search the following before filing. Results from these searches will determine whether to proceed, modify claim scope, or abandon.

### CIPO / Canadian Patent Database
- [ ] Search: `legislative bill summarization automatic` in CIPO Canadian Patents Database (https://www.ic.gc.ca/opic-cipo/cpd/)
- [ ] Search: `parliament bill summary AI classification` — limit to IPC codes G06F 40/30 (natural language summarisation) and G06F 16/903 (document indexing)
- [ ] Search: assignee "Open Parliament" and "OpenNorth" for any Canadian IP filings

### USPTO (PAIR / Patent Full-Text Database)
- [ ] CPC class G06F 40/30 (automatic text summarization) — search for "legislative" and "bill" in title/abstract
- [ ] CPC class G06Q 50/26 (political / government services) — broad review of filed applications
- [ ] Specific searches:
  - [ ] `"bill summarization" "parliament" "plain language"`
  - [ ] `"legislative text" "plain English" "artificial intelligence" "government"`
  - [ ] `"representative lookup" "geocoding" "riding" OR "district" "legislative record"`
  - [ ] `"address" "geocode" "representative" "voting record" "reconciliation"`
  - [ ] `"civic engagement" "bill reaction" "constituent" "voting history"`
  - [ ] GovTrack.us patents (assignee: "GovTrack")
  - [ ] LegiScan, Quorum, FiscalNote, Primer.ai, Legility — check for patents or published applications by these companies

### WIPO / PCT / Espacenet
- [ ] Search Espacenet (https://worldwide.espacenet.com) using IPC G06F40/30 + "parliament" + "summary"
- [ ] PCT published applications — search `"parliamentary" "bill" "summary" "natural language processing"`
- [ ] Canadian equivalents of any US applications found above

### Academic / Non-Patent Literature (for obviousness analysis)
- [ ] ACL Anthology (https://aclanthology.org): search "legislative summarization" — multiple academic papers on congressional/parliamentary NLP summarization exist (BiSumm, BillSum dataset from US Congress, 2019)
- [ ] BillSum dataset paper (Kornilova & Eidelman, 2019) — establishes prior art for AI bill summarization; assess whether your change-triggered re-summarization gate is still novel over this
- [ ] GovTrack blog and changelog — published descriptions of features
- [ ] OpenParliament.ca (Canadian) — public descriptions of features
- [ ] ParlAI (Facebook/Meta) — NLP tools for parliamentary data
- [ ] "Civic tech" literature: Knight Foundation, mySociety (TheyWorkForYou.com UK), Democracy Works

### Key dates to establish with your agent
- [ ] Exact date the repository was first made public (triggers grace period clock — Canada: 12 months from applicant's own disclosure under s. 28.2(1)(a))
- [ ] Date repository was made private
- [ ] Confirm no academic papers, blog posts, conference talks, or product announcements described these features before the public-repository period

---

## Part V — Strategic Recommendations

1. **File quickly.** Canada is a first-to-file jurisdiction. The public-repository exposure means someone else could have seen the code and filed first. Consult your agent about provisional protection.

2. **Feature A is your strongest claim.** Focus resources here. The change-triggered re-summarization gate combining authoritative-source priority with timestamp comparison against the bill's own legislative activity record is the most technically specific and practically novel element.

3. **Feature C is viable but dependent on prior-art results.** The name-reconciliation join across an external API result and a local legislative database is specific, but civic representative-lookup platforms are numerous. The claim needs careful scoping.

4. **Consider trade secrets as an alternative for Features D and E.** Given high prior-art density for those features, trade secret protection (keeping the specific query optimization and engagement design confidential) may be more commercially valuable than a weak patent.

5. **Keep the AI system prompt confidential.** The specific system prompt (`internal/summarizer/pipeline.go`) is a trade secret candidate separate from any patent filing. Do not include its full text in a patent application unless necessary.

6. **PCT filing strategy:** If commercial operations are planned in the US, UK, or EU, file a PCT application with a Canadian national phase. This preserves rights in 150+ countries with a single initial filing date.

---

*End of memo. All analysis is based solely on code and documentation reviewed in the private repository. This memo should be treated as attorney work-product and shared only with outside patent counsel.*
