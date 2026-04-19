package scraper_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

func TestCrawlOntarioVPSittingDates_ParsesVotesProceedingsLinks(t *testing.T) {
	html := `<html><body>
		<a href="/en/legislative-business/house-documents/parliament-44/session-1/2025-04-16/votes-proceedings">Votes and Proceedings</a>
		<a href="/en/legislative-business/house-documents/parliament-44/session-1/2025-04-15/votes-proceedings">Votes and Proceedings</a>
	</body></html>`

	srv := newTestServer(html)
	defer srv.Close()

	dates, err := scraper.CrawlOntarioVPSittingDates(srv.URL, 44, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlOntarioVPSittingDates: %v", err)
	}
	if len(dates) != 2 {
		t.Fatalf("len(dates)=%d, want 2", len(dates))
	}
	if dates[0] != "2025-04-15" || dates[1] != "2025-04-16" {
		t.Fatalf("dates=%v, want [2025-04-15 2025-04-16]", dates)
	}
}

func TestCrawlOntarioVPSittingDates_ParsesHansardLinksAndIgnoresOrdersNotices(t *testing.T) {
	html := `<html><body>
		<a href="/en/legislative-business/house-documents/parliament-44/session-1/2025-04-16/orders-notices">Orders and Notices</a>
		<a href="/en/legislative-business/house-documents/parliament-44/session-1/2025-04-15/hansard">Hansard</a>
		<a href="/en/legislative-business/house-documents/parliament-44/session-1/2025-04-15/hansard">Hansard duplicate</a>
	</body></html>`

	srv := newTestServer(html)
	defer srv.Close()

	dates, err := scraper.CrawlOntarioVPSittingDates(srv.URL, 44, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlOntarioVPSittingDates: %v", err)
	}
	if len(dates) != 1 {
		t.Fatalf("len(dates)=%d, want 1", len(dates))
	}
	if dates[0] != "2025-04-15" {
		t.Fatalf("dates=%v, want [2025-04-15]", dates)
	}
}

func TestCrawlOntarioVPDay_ParsesCurrentMarkupAndSkipsNonDivisionDataWrappers(t *testing.T) {
	html := `<html><body>
	<table><tbody><tr><td colspan="2"><div class="datawrapper">Yesterday, the Leader of the Official Opposition filed a notice of motion.</div></td></tr></tbody></table>
	<table><tbody><tr>
		<td class="votesProceedingsDoc2col" lang="en"><p class="no-indent">Second Reading of <strong>Bill 23</strong>, An Act to amend the Residential Tenancies Act, 2006 and the Retirement Homes Act, 2010 respecting tenancies in care homes.</p></td>
		<td class="votesProceedingsDoc2col" lang="fr"><p class="no-indent">Deuxieme lecture du projet de loi 23.</p></td>
	</tr></tbody></table>
	<table><tbody><tr>
		<td class="votesProceedingsDoc2col" lang="en"><p class="no-indent">Lost on the following division:</p></td>
		<td class="votesProceedingsDoc2col" lang="fr"><p class="no-indent">Rejetee par le vote suivant :</p></td>
	</tr></tbody></table>
	<table><tbody><tr><td colspan="2">
		<div class="datawrapper">
			<h5 class="divisionHeader"><span lang="en">Ayes</span><span class="sl-hide">/</span><span lang="fr">pour</span> (2)</h5>
			<table class="votesList"><tbody><tr>
				<td><div lang="en">Armstrong</div><div class="docHide" lang="fr">Armstrong</div></td>
				<td><div lang="en">Jones</div><div class="docHide" lang="fr">Jones</div></td>
			</tr></tbody></table>
			<h5 class="divisionHeader"><span lang="en">Nays</span><span class="sl-hide">/</span><span lang="fr">contre</span> (3)</h5>
			<table class="votesList"><tbody><tr>
				<td><div lang="en">Brown</div><div class="docHide" lang="fr">Brown</div></td>
				<td><div lang="en">Taylor</div><div class="docHide" lang="fr">Taylor</div></td>
				<td><div lang="en">Wilson</div><div class="docHide" lang="fr">Wilson</div></td>
			</tr></tbody></table>
		</div>
	</td></tr></tbody></table>
	</body></html>`

	srv := newTestServer(html)
	defer srv.Close()

	divs, err := scraper.CrawlOntarioVPDay(srv.URL, 44, 1, "2026-04-16", srv.Client())
	if err != nil {
		t.Fatalf("CrawlOntarioVPDay: %v", err)
	}
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.ID != "on-44-1-2026-04-16-1" {
		t.Fatalf("division ID=%q, want on-44-1-2026-04-16-1", divs[0].Division.ID)
	}
	if divs[0].Division.Description != "Second Reading of Bill 23, An Act to amend the Residential Tenancies Act, 2006 and the Retirement Homes Act, 2010 respecting tenancies in care homes." {
		t.Fatalf("description=%q", divs[0].Division.Description)
	}
	if divs[0].Division.Yeas != 2 || divs[0].Division.Nays != 3 {
		t.Fatalf("counts=(%d,%d), want (2,3)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if divs[0].Division.Result != "Negatived" {
		t.Fatalf("result=%q, want Negatived", divs[0].Division.Result)
	}
	if len(divs[0].Votes) != 5 {
		t.Fatalf("len(votes)=%d, want 5", len(divs[0].Votes))
	}
}

func TestCrawlQuebecVotes_UsesJSONSearchAndParsesDetailVotes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
			<select class="sessionLegislature">
				<option value="-1">All sessions</option>
				<option value="1617" title="43rd Legislature, 2nd Session (September 30, 2025 - April 8, 2026)">Current</option>
			</select>
		</body></html>`)
	})
	mux.HandleFunc("/Gabarits/RegistreDesVotes.aspx/Rechercher", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"d":{"NumeroPage":0,"QuantiteParPage":25,"NombreTotalDonnees":1,"NomRequete":"mock-query","Donnees":[{"DateVote":"2026-04-02","Titre":"Budget motion","Numero":"171","VoteURL":"/vote/43-2-171"}]}}`)
	})
	mux.HandleFunc("/vote/43-2-171", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
			<input type="hidden" id="nbPour" value="53" />
			<input type="hidden" id="nbContre" value="20" />
			<div id="ctl00_ColCentre_ContenuColonneGauche_pnlPour" class="votes">
				<div class="depute"><span class="nom">Allaire</span></div>
			</div>
			<div id="ctl00_ColCentre_ContenuColonneGauche_pnlContre" class="votes">
				<div class="depute"><span class="nom">Tanguay</span></div>
			</div>
		</body></html>`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	divs, err := scraper.CrawlQuebecVotes(srv.URL+"/index", 43, 2, srv.Client())
	if err != nil {
		t.Fatalf("CrawlQuebecVotes: %v", err)
	}
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Yeas != 53 || divs[0].Division.Nays != 20 {
		t.Fatalf("counts=(%d,%d), want (53,20)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if len(divs[0].Votes) != 2 {
		t.Fatalf("len(votes)=%d, want 2", len(divs[0].Votes))
	}
}

func TestParseNewBrunswickPDFDivisions_ParsesMemberNamesFromVoteBlock(t *testing.T) {
	text := `RECORDED DIVISION YEAS - 14 Mr. Hogan Mr. Monahan Ms. S. Wilson Ms. M. Johnson Mr. Ames Mr. Cullins Mr. Savoie Mr. Weir Ms. Bockus Ms. Scott - Wallace Ms. Conroy Mr. Lee Mr. Austin Mr. Oliver NAYS - 25 Hon. Mr. Gauvin Hon. Mr. C. Chiasson Mr. J. LeBlanc Mr. M. LeBlanc Hon. Ms. Holt And the question being put`

	divs := scraper.ParseNewBrunswickPDFDivisionsForTest(text, "https://example.com/journal.pdf", 61, 1, 1, "2025-03-27")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Yeas != 14 || divs[0].Division.Nays != 25 {
		t.Fatalf("counts=(%d,%d), want (14,25)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if len(divs[0].Votes) < 18 {
		t.Fatalf("len(votes)=%d, want >=18", len(divs[0].Votes))
	}
}

func TestParseAlbertaVPDivisions_ForAgainstFormat(t *testing.T) {
	text := `VOTES AND PROCEEDINGS No. 7 DIVISION 1 On Bill 37 amendment For the amendment: 31 Al-Guneid Elmeligi Kayande Arcand-Paul Eremenko Against the amendment: 28 Amery Johnson Rowswell`
	divs := scraper.ParseAlbertaVPDivisionsForTest(text, "https://example.com/vp.pdf", 31, 2, 1, "2025-05-14")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Yeas != 31 || divs[0].Division.Nays != 28 {
		t.Fatalf("counts=(%d,%d), want (31,28)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if divs[0].Division.Result != "Carried" {
		t.Fatalf("result=%q, want Carried", divs[0].Division.Result)
	}
	if len(divs[0].Votes) < 5 {
		t.Fatalf("len(votes)=%d, want >=5", len(divs[0].Votes))
	}
}

func TestParseAlbertaVPDivisions_MultiDivision(t *testing.T) {
	text := `DIVISION 1 On the motion For the motion: 20 Smith Jones Brown Against the motion: 15 Davis Wilson DIVISION 2 On third reading For the bill: 35 Taylor Morgan Against the bill: 10 Allen Foster`
	divs := scraper.ParseAlbertaVPDivisionsForTest(text, "https://example.com/vp.pdf", 31, 2, 1, "2025-05-14")
	if len(divs) != 2 {
		t.Fatalf("len(divs)=%d, want 2", len(divs))
	}
	if divs[0].Division.Yeas != 20 || divs[0].Division.Nays != 15 {
		t.Fatalf("div1 counts=(%d,%d), want (20,15)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if divs[1].Division.Yeas != 35 || divs[1].Division.Nays != 10 {
		t.Fatalf("div2 counts=(%d,%d), want (35,10)", divs[1].Division.Yeas, divs[1].Division.Nays)
	}
}

func TestParseAlbertaVPDivisions_QuestionBlocksFormat(t *testing.T) {
	text := `Hon. Mr. Schow, Government House Leader, moved pursuant to Standing Order 27 that the Assembly proceed immediately to Orders of the Day. The question being put, the motion was agreed to on the voice vote. With Hon. Mr. McIver in the Chair, the names being called for were taken as follows: For the motion: 45 Amery Armstrong-Homeniuk Boitchenko Bouchard Cyr de Jonge Dreeshen Against the motion: 37 Al-Guneid Arcand-Paul Batten Boparai Brar ORDERS OF THE DAY Government Motions 2. Moved by Hon. Mr. Schow: Be it resolved that the Legislative Assembly resolve into Committee of the Whole, when called, to consider certain Bills on the Order Paper. The question being put, the motion was agreed to on the voice vote. With Hon. Mr. McIver in the Chair, the names being called for were taken as follows: For the motion: 44 Amery Armstrong-Homeniuk Boitchenko Bouchard Cyr de Jonge Dreeshen Against the motion: 37 Al-Guneid Arcand-Paul Batten Boparai Brar`
	divs := scraper.ParseAlbertaVPDivisionsForTest(text, "https://example.com/vp.pdf", 31, 2, 1, "2025-10-27")
	if len(divs) != 2 {
		t.Fatalf("len(divs)=%d, want 2", len(divs))
	}
	if divs[0].Division.Yeas != 45 || divs[0].Division.Nays != 37 {
		t.Fatalf("div1 counts=(%d,%d), want (45,37)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if divs[1].Division.Yeas != 44 || divs[1].Division.Nays != 37 {
		t.Fatalf("div2 counts=(%d,%d), want (44,37)", divs[1].Division.Yeas, divs[1].Division.Nays)
	}
	if !strings.Contains(divs[0].Division.Description, "Standing Order 27") {
		t.Fatalf("div1 description=%q", divs[0].Division.Description)
	}
	if !strings.Contains(divs[1].Division.Description, "Committee of the Whole") {
		t.Fatalf("div2 description=%q", divs[1].Division.Description)
	}
	if len(divs[0].Votes) < 5 || len(divs[1].Votes) < 5 {
		t.Fatalf("votes lens=(%d,%d), want >=5", len(divs[0].Votes), len(divs[1].Votes))
	}
}

func TestParseAlbertaVPDivisions_QuestionBlocksKeepBillDescription(t *testing.T) {
	text := `Second Reading On the motion that the following Bill be now read a Second time: Bill 27 Financial Statutes Amendment Act, 2026 -- Hon. Mr. Horner A debate followed. The question being put, the motion was agreed to on the voice vote. With Hon. Mr. McIver in the Chair, the names being called for were taken as follows: For the motion: 45 Amery Armstrong-Homeniuk Boitchenko Bouchard Cyr de Jonge Dreeshen Against the motion: 37 Al-Guneid Arcand-Paul Batten Boparai Brar`
	divs := scraper.ParseAlbertaVPDivisionsForTest(text, "https://example.com/vp.pdf", 31, 2, 1, "2026-04-15")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if !strings.Contains(divs[0].Division.Description, "Bill 27 Financial Statutes Amendment Act") {
		t.Fatalf("description=%q", divs[0].Division.Description)
	}
}

func TestParsePDFDivisionsYeasNays_ManitobaStyle(t *testing.T) {
	text := `VOTES AND PROCEEDINGS 43rd Legislature 3rd Session YEAS - 37 Balser Bailey Bereza Brar Bushie Clarke Cook NAYS - 18 Balcaen Byram Eichler Ewasko Goertzen`
	divs := scraper.ParsePDFDivisionsYeasNaysForTest(text, "https://example.com/votes_041.pdf", "mb", "manitoba", 43, 3, 1, "2024-02-20")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Yeas != 37 || divs[0].Division.Nays != 18 {
		t.Fatalf("counts=(%d,%d), want (37,18)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if divs[0].Division.Result != "Carried" {
		t.Fatalf("result=%q, want Carried", divs[0].Division.Result)
	}
	if len(divs[0].Votes) < 5 {
		t.Fatalf("len(votes)=%d, want >=5", len(divs[0].Votes))
	}
}

func TestParsePDFDivisionsYeasNays_ManitobaStyleUppercaseNames(t *testing.T) {
	// Real MB V&P PDFs commonly render surname lists in ALL CAPS. This compact
	// excerpt is sufficient because the parser only requires YEAS/NAYS headers,
	// counts, and tokenized name blocks.
	text := `VOTES AND PROCEEDINGS 43rd Legislature 3rd Session YEAS - 37 BALSER BAILEY BEREZA BRAR BUSHIE CLARKE COOK NAYS - 18 BALCAEN BYRAM EICHLER EWASKO GOERTZEN`
	divs := scraper.ParsePDFDivisionsYeasNaysForTest(text, "https://example.com/votes_041.pdf", "mb", "manitoba", 43, 3, 1, "2024-02-20")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if len(divs[0].Votes) < 5 {
		t.Fatalf("len(votes)=%d, want >=5", len(divs[0].Votes))
	}
	votesByName := map[string]string{}
	for _, v := range divs[0].Votes {
		votesByName[v.MemberName] = v.Vote
	}
	if got := votesByName["BALSER"]; got != "Yea" {
		t.Fatalf("vote[BALSER]=%q, want Yea", got)
	}
	if got := votesByName["EICHLER"]; got != "Nay" {
		t.Fatalf("vote[EICHLER]=%q, want Nay", got)
	}
}

func TestParseManitobaAyeNayDivisions_CurrentLayout(t *testing.T) {
	text := `Pursuant to sub-rule 24(7), the division on the proposed motion of MLA LAMOUREUX was deferred to take place today at 11:55 a.m. THAT Bill (No. 232) The Autism Strategy Act/Loi sur la strategie sur l'autisme, be now read a Second Time and be referred to a Committee of this House. And the Question being put. It was agreed to, on the following division: AYE BALCAEN BEREZA BLASHKO BRAR BUSHIE BYRAM CABLE COMPTON COOK CORBETT CROSS DELA CRUZ DEVGAN EWASKO GUENTER HIEBERT JOHNSON KENNEDY KHAN KING KOSTYSHYN LAMOUREUX MALOWAY MARCELINO MOROZ MOSES MOYES NARTH NESBITT OXENHAM PERCHOTTE REDHEAD ROBBINS SALA SANDHU SCHMIDT SCHOTT SCHULER SIMARD SMITH STONE WASYLIW WHARTON WIEBE WOWCHUK ..................................... 46 NAY ......................................................... 0 The Bill was accordingly read a Second Time and referred to a Committee of this House.`
	divs := scraper.ParseManitobaAyeNayDivisionsForTest(text, "https://example.com/votes_031.pdf", 43, 3, 1, "2026-03-19")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Yeas != 46 || divs[0].Division.Nays != 0 {
		t.Fatalf("counts=(%d,%d), want (46,0)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
	if len(divs[0].Votes) < 10 {
		t.Fatalf("len(votes)=%d, want >=10", len(divs[0].Votes))
	}
	if !strings.Contains(divs[0].Division.Description, "Bill (No. 232)") {
		t.Fatalf("description=%q", divs[0].Division.Description)
	}
	seen := map[string]string{}
	for _, vote := range divs[0].Votes {
		seen[vote.MemberName] = vote.Vote
	}
	if seen["BALCAEN"] != "Yea" || seen["WOWCHUK"] != "Yea" || seen["DELA CRUZ"] != "Yea" {
		t.Fatalf("unexpected parsed votes: BALCAEN=%q WOWCHUK=%q DELA CRUZ=%q", seen["BALCAEN"], seen["WOWCHUK"], seen["DELA CRUZ"])
	}
}

func TestParseManitobaAyeNayDivisions_LiveNoParensBillFormat(t *testing.T) {
	text := `Pursuant to sub-rule 24(7), the division on the proposed motion of MLA LAMOUREUX was deferred to take place today at 11:55 a.m. THAT Bill No. 232 The Autism Strategy Act/Loi sur la strategie sur l'autisme, be now read a Second Time and be referred to a Committee of this House. And the Question being put. It was agreed to, on the following division: AYE BALCAEN BEREZA BLASHKO BRAR BUSHIE BYRAM CABLE COMPTON COOK CORBETT CROSS DELA CRUZ DEVGAN EWASKO GUENTER HIEBERT JOHNSON KENNEDY KHAN KING KOSTYSHYN LAMOUREUX MALOWAY MARCELINO MOROZ MOSES MOYES NARTH NESBITT OXENHAM PERCHOTTE REDHEAD ROBBINS SALA SANDHU SCHMIDT SCHOTT SCHULER SIMARD SMITH STONE WASYLIW WHARTON WIEBE WOWCHUK ..................................... 46 NAY ......................................................... 0`
	divs := scraper.ParseManitobaAyeNayDivisionsForTest(text, "https://example.com/votes_031.pdf", 43, 3, 1, "2026-03-19")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if !strings.Contains(divs[0].Division.Description, "Bill No. 232") {
		t.Fatalf("description=%q", divs[0].Division.Description)
	}
	if scraper.ExtractProvincialBillNumber(divs[0].Division.Description) != "232" {
		t.Fatalf("bill number=%q", scraper.ExtractProvincialBillNumber(divs[0].Division.Description))
	}
}

func TestParseNLJournalDivisions_OutcomeOnly(t *testing.T) {
	text := `The house considered Bill 3. On the motion that the bill be read a third time, the question was put, and the motion was agreed to. On the amendment to the bill, the question was put, and the amendment was defeated.`
	divs := scraper.ParseNLJournalDivisionsForTest(text, "https://example.com/26-04-14.pdf", 51, 1, 1, "2026-04-14")
	if len(divs) == 0 {
		t.Fatal("expected at least one division")
	}
	for _, d := range divs {
		if d.Division.Result != "Carried" && d.Division.Result != "Negatived" {
			t.Fatalf("unexpected result: %q", d.Division.Result)
		}
		if len(d.Votes) != 0 {
			t.Fatalf("expected no member votes for NL outcome-only, got %d", len(d.Votes))
		}
	}
}

func TestCrawlPrinceEdwardIslandVotes_HandlesCaptcha(t *testing.T) {
	const peiLegislature = 68
	srv := newTestServer(`<html><body><link rel="stylesheet" href="https://captcha.perfdrive.com/challenge.css"></body></html>`)
	defer srv.Close()

	divs, err := scraper.CrawlPrinceEdwardIslandVotes(srv.URL, peiLegislature, 1, srv.Client())
	if err != nil {
		t.Fatalf("expected no error on CAPTCHA, got: %v", err)
	}
	if len(divs) != 0 {
		t.Fatalf("expected 0 divisions on CAPTCHA, got %d", len(divs))
	}
}

func TestCrawlPrinceEdwardIslandVotes_UsesWorkflowAPI(t *testing.T) {
	const peiLegislature = 68
	mux := http.NewServeMux()
	mux.HandleFunc("/services/api/workflow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"url":"/journals/2026-04-11"},{"detailUrl":"/journals/2026-04-12"}]}`)
	})
	mux.HandleFunc("/journals/2026-04-11", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body>
<p>Motion agreed to:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 3</td></tr>
<tr><td>Smith <br>Jones <br></td><td>Brown <br></td><td></td><td></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 1</td></tr>
<tr><td>Lee <br></td><td></td><td></td><td></td></tr>
</table></body></html>`)
	})
	mux.HandleFunc("/journals/2026-04-12", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body>
<p>Motion agreed to:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 2</td></tr>
<tr><td>Alpha <br></td><td>Beta <br></td><td></td><td></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 0</td></tr>
<tr><td></td><td></td><td></td><td></td></tr>
</table></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	divs, err := scraper.CrawlPrinceEdwardIslandVotes(srv.URL+"/services/api/workflow", peiLegislature, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlPrinceEdwardIslandVotes workflow: %v", err)
	}
	if len(divs) < 2 {
		t.Fatalf("len(divs)=%d, want at least 2", len(divs))
	}
}

func TestParseBCVotesDivisions_ParsesDivisionTableYeasNays(t *testing.T) {
	// HTML fixture modelled on a real BC VP document from the 43rd Parliament, 1st Session.
	// The <table class="division"> format lists Yeas first, then Nays, each spanning four
	// 25%-width columns with member surnames separated by <br> elements.
	html := `<html><body>
<p>Motion agreed to on the following division:</p>
<table width="600" cellpadding="0" cellspacing="0" class="division">
<tr>
<td valign="top" class="head" colspan="4">Yeas &#8212; 48</td>
</tr>
<tr>
<td valign="top" width="25%">Eby <br>Farnworth <br>Sharma <br></td>
<td valign="top" width="25%">Dix <br>Beare <br>Boyle <br></td>
<td valign="top" width="25%">Kahlon <br>Bailey <br>Gibson <br></td>
<td valign="top" width="25%">Glumac <br>Arora <br>Shah <br></td>
</tr>
<tr>
<td valign="top" class="head" colspan="4">Nays &#8212; 40</td>
</tr>
<tr>
<td valign="top" width="25%">Rustad <br>Milobar <br>Halford <br></td>
<td valign="top" width="25%">Dew <br>Clare <br>Rattee <br></td>
<td valign="top" width="25%">Bird <br>Stamer <br>Day <br></td>
<td valign="top" width="25%">Doerkson <br>Luck <br>Block <br></td>
</tr>
</table>
</body></html>`

	divs := scraper.ParseBCVotesDivisionsForTest(html, "https://example.com/v251201.htm", "2025-12-01", 43, 1, 1)
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	d := divs[0]
	if d.Division.Yeas != 48 || d.Division.Nays != 40 {
		t.Fatalf("counts=(%d,%d), want (48,40)", d.Division.Yeas, d.Division.Nays)
	}
	if d.Division.Result != "Carried" {
		t.Fatalf("result=%q, want Carried", d.Division.Result)
	}
	if len(d.Votes) < 24 {
		t.Fatalf("len(votes)=%d, want >=24", len(d.Votes))
	}
	// Verify at least one yea and one nay vote recorded.
	yeaCount, nayCount := 0, 0
	for _, v := range d.Votes {
		if v.Vote == "Yea" {
			yeaCount++
		} else if v.Vote == "Nay" {
			nayCount++
		}
	}
	if yeaCount == 0 || nayCount == 0 {
		t.Fatalf("yeaCount=%d nayCount=%d, want both >0", yeaCount, nayCount)
	}
}

func TestParseBCVotesDivisions_UsesPriorSubstantiveParagraphForDescription(t *testing.T) {
	html := `<html><body>
<p>On the motion of <em>Tara Armstrong</em> that Bill (No.&nbsp;M 201) intituled <em>Public Safety Statutes Amendment Act</em> be introduced and read a first time, the House divided.</p>
<p>Motion negatived on the following division:</p>
<table width="600" cellpadding="0" cellspacing="0" class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 3</td></tr>
<tr><td>Armstrong <br></td><td>Jones <br></td><td>Brown <br></td><td></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 6</td></tr>
<tr><td>Allen <br></td><td>Foster <br></td><td>Mok <br></td><td>Lee <br>Smith <br>Taylor <br></td></tr>
</table>
</body></html>`

	divs := scraper.ParseBCVotesDivisionsForTest(html, "https://example.com/v251202.htm", "2025-12-02", 43, 1, 1)
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Description != "On the motion of Tara Armstrong that Bill (No. M 201) intituled Public Safety Statutes Amendment Act be introduced and read a first time, the House divided." {
		t.Fatalf("description=%q", divs[0].Division.Description)
	}
	if billNumber := scraper.ExtractProvincialBillNumber(divs[0].Division.Description); billNumber != "M-201" {
		t.Fatalf("billNumber=%q, want M-201", billNumber)
	}
}

func TestParseBCVotesDivisions_NaysExceedYeadsIsNegatived(t *testing.T) {
	html := `<html><body>
<p>Amendment was defeated on the following division:</p>
<table width="600" cellpadding="0" cellspacing="0" class="division">
<tr><td valign="top" class="head" colspan="4">Nays &#8212; 6</td></tr>
<tr>
<td valign="top" width="25%">Smith <br>Jones <br></td>
<td valign="top" width="25%">Brown <br>Davis <br></td>
<td valign="top" width="25%">Wilson <br>Taylor <br></td>
<td valign="top" width="25%"></td>
</tr>
<tr><td valign="top" class="head" colspan="4">Yeas &#8212; 3</td></tr>
<tr>
<td valign="top" width="25%">Allen <br></td>
<td valign="top" width="25%">Foster <br></td>
<td valign="top" width="25%">Mok <br></td>
<td valign="top" width="25%"></td>
</tr>
</table>
</body></html>`

	divs := scraper.ParseBCVotesDivisionsForTest(html, "https://example.com/v251202.htm", "2025-12-02", 43, 1, 1)
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	if divs[0].Division.Result != "Negatived" {
		t.Fatalf("result=%q, want Negatived", divs[0].Division.Result)
	}
	if divs[0].Division.Yeas != 3 || divs[0].Division.Nays != 6 {
		t.Fatalf("counts=(%d,%d), want (3,6)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
}

func TestParliamentOrdinal(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1st"},
		{2, "2nd"},
		{3, "3rd"},
		{4, "4th"},
		{11, "11th"},
		{12, "12th"},
		{13, "13th"},
		{21, "21st"},
		{43, "43rd"},
	}
	for _, c := range cases {
		got := scraper.ParliamentOrdinalForTest(c.n)
		if got != c.want {
			t.Errorf("parliamentOrdinal(%d)=%q, want %q", c.n, got, c.want)
		}
	}
}
