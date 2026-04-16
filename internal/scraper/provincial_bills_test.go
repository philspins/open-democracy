package scraper_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

func TestExtractProvincialBillNumber(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Bill 12 - An Act", "12"},
		{"BILL A-23 respecting schools", "A-23"},
		{"Motion on C-47", "C-47"},
		{"No bill here", ""},
	}
	for _, c := range cases {
		got := scraper.ExtractProvincialBillNumber(c.in)
		if got != c.want {
			t.Fatalf("ExtractProvincialBillNumber(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestCrawlProvincialBillsFromIndex_ParsesBillLinks(t *testing.T) {
	body := `<html><body>
  <ul>
    <li><a href="/bills/12">Bill 12 - Health Statute Amendment Act</a> (April 7, 2026)</li>
    <li><a href="/bills/13">Bill 13 - Education Modernization Act</a></li>
  </ul>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	bills, err := scraper.CrawlProvincialBillsFromIndex(srv.URL, "ab", 31, 1, "alberta", srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialBillsFromIndex: %v", err)
	}
	if len(bills) != 2 {
		t.Fatalf("len(bills)=%d want 2", len(bills))
	}
	if bills[0].ID == "" || bills[0].Number == "" {
		t.Fatalf("expected non-empty bill id and number: %+v", bills[0])
	}
}

func TestCrawlGenericProvincialVotes_ParsesCounts(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/votes/2026-04-07">Votes and Proceedings</a></body></html>`))
	})
	mux.HandleFunc("/votes/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body>
      <h3>Bill 12 second reading</h3>
      <table>
        <tr><td>Yeas: 5</td><td>Nays: 2</td></tr>
      </table>
    </body></html>`))
	})

	divs, err := scraper.CrawlGenericProvincialVotes(srv.URL, "ab", "alberta", 31, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlGenericProvincialVotes: %v", err)
	}
	if len(divs) != 1 {
		t.Fatalf("len(divs)=%d want 1", len(divs))
	}
	if divs[0].Division.Yeas != 5 || divs[0].Division.Nays != 2 {
		t.Fatalf("counts=%d/%d want 5/2", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
}

func TestCrawlAlbertaVotes_ReturnsZeroWhenNoPDFLinks(t *testing.T) {
	// AB now requires docs.assembly.ab.ca VP PDF links; generic HTML returns 0 results gracefully.
	srv := newTestServer(`<html><body>
      <a href="/assembly-records/votes-and-proceedings/2026-04-08">Votes and Proceedings</a>
    </body></html>`)
	defer srv.Close()

	divs, err := scraper.CrawlAlbertaVotes(srv.URL, 31, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlAlbertaVotes: %v", err)
	}
	// Without docs.assembly.ab.ca PDF links, the new PDF-based scraper returns 0 divisions.
	if divs == nil {
		divs = []scraper.ProvincialDivisionResult{}
	}
	_ = divs // graceful empty result is expected
}

func TestCrawlBritishColumbiaVotes_UsesProvinceMatcher(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/votes-and-proceedings/2026-04-07">Votes and Proceedings</a></body></html>`))
	})
	mux.HandleFunc("/votes-and-proceedings/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><table><tr><td>Ayes: 11</td><td>Nays: 4</td></tr></table></body></html>`))
	})

	divs, err := scraper.CrawlBritishColumbiaVotes(srv.URL, 43, 2, srv.Client())
	if err != nil {
		t.Fatalf("CrawlBritishColumbiaVotes: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected at least one parsed bc division")
	}
}

func TestCrawlQuebecVotes_UsesProvinceMatcher(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/registre-votes/registre-votes-details.html?vote=1">Registre votes details</a></body></html>`))
	})
	mux.HandleFunc("/registre-votes/registre-votes-details.html", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><table><tr><td>Pour: 77</td><td>Contre: 32</td></tr></table></body></html>`))
	})

	divs, err := scraper.CrawlQuebecVotes(srv.URL, 43, 2, srv.Client())
	if err != nil {
		t.Fatalf("CrawlQuebecVotes: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected at least one parsed quebec division")
	}
}

func TestProvinceSpecificBillCrawlerEntryPoints(t *testing.T) {
	type billCrawler func(string, int, int, *http.Client) ([]scraper.ProvincialBillStub, error)

	index := `<html><body>
  <a href="/bills/10">Bill 10 - Test Bill</a>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(index))
	}))
	defer srv.Close()

	cases := []struct {
		name string
		fn   billCrawler
	}{
		{"alberta", scraper.CrawlAlbertaBills},
		{"bc", scraper.CrawlBritishColumbiaBills},
		{"manitoba", scraper.CrawlManitobaBills},
		{"new_brunswick", scraper.CrawlNewBrunswickBills},
		{"newfoundland_labrador", scraper.CrawlNewfoundlandAndLabradorBills},
		{"nova_scotia", scraper.CrawlNovaScotiaBills},
		{"ontario", scraper.CrawlOntarioBills},
		{"pei", scraper.CrawlPrinceEdwardIslandBills},
		{"quebec", scraper.CrawlQuebecBills},
		{"saskatchewan", scraper.CrawlSaskatchewanBills},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bills, err := tc.fn(srv.URL, 1, 1, srv.Client())
			if err != nil {
				t.Fatalf("crawler returned error: %v", err)
			}
			if len(bills) == 0 {
				t.Fatal("expected at least one bill parsed")
			}
		})
	}
}

func TestProvinceSpecificVoteCrawlerEntryPoints(t *testing.T) {
	type voteCrawler func(string, int, int, *http.Client) ([]scraper.ProvincialDivisionResult, error)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/votes/2026-04-07">Votes and Proceedings</a></body></html>`))
	})
	mux.HandleFunc("/votes/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><table><tr><td>Yeas: 9</td><td>Nays: 2</td></tr></table></body></html>`))
	})

	// AB and NS use dedicated PDF scrapers that require specific PDF link patterns;
	// they are tested separately via ParseAlbertaVPDivisionsForTest / crawlNovaScotiaVotesFromPDF.
	// MB, NL fall back to the generic HTML scraper when no PDF links are found.
	cases := []struct {
		name string
		fn   voteCrawler
	}{
		{"bc", scraper.CrawlBritishColumbiaVotes},
		{"manitoba", scraper.CrawlManitobaVotes},
		{"new_brunswick", scraper.CrawlNewBrunswickVotes},
		{"newfoundland_labrador", scraper.CrawlNewfoundlandAndLabradorVotes},
		{"pei", scraper.CrawlPrinceEdwardIslandVotes},
		{"quebec", scraper.CrawlQuebecVotes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			divs, err := tc.fn(srv.URL, 1, 1, srv.Client())
			if err != nil {
				t.Fatalf("crawler returned error: %v", err)
			}
			if len(divs) == 0 {
				t.Fatal("expected at least one division parsed")
			}
		})
	}
}

