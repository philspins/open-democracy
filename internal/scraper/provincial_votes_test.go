package scraper_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestParsePEIJournalDivisions_YeasAndNays(t *testing.T) {
	// Minimal text modelled on the real April 10, 2026 journal (Nays first, then Yeas).
	text := `Hon. Premier moved the following Motion. Hon. Mr. Speaker put the Question. ` +
		`A Recorded Division being sought, the names were recorded by the Clerk as follows: ` +
		`Nays (2\ Leader of the Third Party Karla Bernard (Charlottetown - Victoria Park\ Gordon McNeilly (Charlottetown - West Royalty\ ` +
		`Yeas (3\ Hon. Darlene Compton (Land and Environment\ Hon. Premier Hon. Bloyce Thompson (Agriculture, Justice and Public Safety, Attorney General\ ` +
		`The Motion was CARRIED and resolved accordingly.`

	divs := scraper.ParsePEIJournalDivisionsForTest(text, "https://docs.assembly.pe.ca/test.pdf", 67, 3, 1, "2026-04-10")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	d := divs[0]
	if d.Division.Nays != 2 || d.Division.Yeas != 3 {
		t.Errorf("counts=(%d,%d), want (2,3)", d.Division.Nays, d.Division.Yeas)
	}
	if d.Division.Result != "Carried" {
		t.Errorf("result=%q, want Carried", d.Division.Result)
	}

	var yeas, nays []string
	for _, v := range d.Votes {
		if v.Vote == "Yea" {
			yeas = append(yeas, v.MemberName)
		} else {
			nays = append(nays, v.MemberName)
		}
	}
	if len(nays) != 2 {
		t.Errorf("nay voters=%v, want 2", nays)
	}
	if len(yeas) != 3 {
		t.Errorf("yea voters=%v, want 3", yeas)
	}
	// "Premier" should be extracted from "Hon. Premier" (no riding suffix).
	found := false
	for _, n := range yeas {
		if n == "Premier" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Premier' in yea voters, got %v", yeas)
	}
}

func TestParsePEIJournalDivisions_NaysFirst(t *testing.T) {
	text := `A Recorded Division being sought, the names were recorded by the Clerk as follows: ` +
		`Nays (12\ Hon. Darlene Compton (Land and Environment\ Hon. Jill Burridge (Finance and Affordability\ ` +
		`Hon. Bloyce Thompson (Agriculture, Justice and Public Safety, Attorney General\ Hon. Zack Bell (Workforce and Advanced Learning\ ` +
		`Hon. Ernie Hudson (Fisheries, Rural Development and Tourism\ Tyler DesRoches (Summerside - Wilmot\ ` +
		`Hon. Barb Ramsay (Social Development and Seniors\ Hon. Robin Croucher (Education and Early Years\ ` +
		`Hon. Jenn Redmond (Economic Development, Trade and Artificial Intelligence\ Hon. Kent Dollar (Housing and Communities\ ` +
		`Susie Dillon (Charlottetown - Belvedere\ Brendan Curran (Georgetown - Pownal\ ` +
		`Yeas (7\ Leader of the Third Party Karla Bernard (Charlottetown - Victoria Park\ Gordon McNeilly (Charlottetown - West Royalty\ ` +
		`Hon. Leader of the Opposition Peter Bevan - Baker (New Haven - Rocky Point\ ` +
		`Robert Henderson (O'Leary - Inverness\ Carolyn Simpson (Charlottetown - Hillsborough Park\ ` +
		`Motion resolved in the Negative.`

	divs := scraper.ParsePEIJournalDivisionsForTest(text, "https://docs.assembly.pe.ca/test.pdf", 67, 3, 1, "2026-04-07")
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d, want 1", len(divs))
	}
	d := divs[0]
	if d.Division.Nays != 12 || d.Division.Yeas != 7 {
		t.Errorf("counts=(%d,%d), want (12,7)", d.Division.Nays, d.Division.Yeas)
	}
	if d.Division.Result != "Negatived" {
		t.Errorf("result=%q, want Negatived", d.Division.Result)
	}
	if len(d.Votes) < 5 {
		t.Errorf("too few votes: %d", len(d.Votes))
	}
}

func TestCrawlPrinceEdwardIslandVotes_HandlesCaptcha(t *testing.T) {
	srv := newTestServer(`<html><body><link rel="stylesheet" href="https://captcha.perfdrive.com/challenge.css"></body></html>`)
	defer srv.Close()

	divs, err := scraper.CrawlPrinceEdwardIslandVotes(srv.URL, 68, 1, srv.Client())
	if err != nil {
		t.Fatalf("expected no error on CAPTCHA, got: %v", err)
	}
	if len(divs) != 0 {
		t.Fatalf("expected 0 divisions on CAPTCHA, got %d", len(divs))
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
		if v.Vote == "yea" {
			yeaCount++
		} else if v.Vote == "nay" {
			nayCount++
		}
	}
	if yeaCount == 0 || nayCount == 0 {
		t.Fatalf("yeaCount=%d nayCount=%d, want both >0", yeaCount, nayCount)
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

