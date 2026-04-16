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

