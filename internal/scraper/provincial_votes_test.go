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
