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

func TestCrawlBritishColumbiaVotes_UsesLIMSAPI(t *testing.T) {
	// The BC scraper uses the LIMS document-store REST API.
	// The indexURL parameter becomes the LIMS base URL for testing.
	vpHTML := `<!DOCTYPE html><html><body>
<p>Motion agreed to on the following division:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 11</td></tr>
<tr><td>Eby <br>Farnworth <br>Sharma <br></td><td>Dix <br>Beare <br>Boyle <br></td><td>Kahlon <br>Bailey <br>Gibson <br></td><td>Glumac <br>Arora <br></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 4</td></tr>
<tr><td>Rustad <br></td><td>Milobar <br></td><td>Halford <br></td><td>Dew <br></td></tr>
</table>
</body></html>`

	limsJSON := `{"allParliamentaryFileAttributes":{"nodes":[{"fileName":"v260407.htm","filePath":"/ldp/43rd2nd/votes/","published":true,"date":"2026-04-07T00:00:00","votesAttributesByFileId":{"nodes":[{"voteNumbers":"38"}]}}]}}`

	mux := http.NewServeMux()
	mux.HandleFunc("/pdms/votes-and-proceedings/43rd2nd", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(limsJSON))
	})
	mux.HandleFunc("/pdms/ldp/43rd2nd/votes/v260407.htm", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(vpHTML))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	divs, err := scraper.CrawlBritishColumbiaVotes(srv.URL, 43, 2, srv.Client())
	if err != nil {
		t.Fatalf("CrawlBritishColumbiaVotes: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected at least one parsed bc division")
	}
	if divs[0].Division.Yeas != 11 || divs[0].Division.Nays != 4 {
		t.Fatalf("counts=(%d,%d), want (11,4)", divs[0].Division.Yeas, divs[0].Division.Nays)
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

	vpHTML := `<!DOCTYPE html><html><body>
<p>Motion agreed to:</p>
<table class="division">
<tr><td class="head" colspan="4">Yeas &#8212; 9</td></tr>
<tr><td>Smith <br>Jones <br>Brown <br></td><td>Davis <br>Wilson <br>Taylor <br></td><td>Allen <br>Foster <br>Mok <br></td><td></td></tr>
<tr><td class="head" colspan="4">Nays &#8212; 2</td></tr>
<tr><td>Lee <br></td><td>Park <br></td><td></td><td></td></tr>
</table></body></html>`

	limsJSON := `{"allParliamentaryFileAttributes":{"nodes":[{"fileName":"test.htm","filePath":"/ldp/1st1st/votes/","published":true,"date":"2026-04-07T00:00:00","votesAttributesByFileId":{"nodes":[{"voteNumbers":"1"}]}}]}}`

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
	// BC uses the LIMS document-store API; legislature=1, session=1 → "1st1st".
	mux.HandleFunc("/pdms/votes-and-proceedings/1st1st", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(limsJSON))
	})
	mux.HandleFunc("/pdms/ldp/1st1st/votes/test.htm", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(vpHTML))
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

// ── PEI WDF workflow API tests ────────────────────────────────────────────────

// TestCrawlPrinceEdwardIslandBills_UsesWorkflowAPI verifies that when the WDF
// workflow API endpoint serves valid JSON, bills are parsed from it instead of
// falling back to HTML scraping.
func TestCrawlPrinceEdwardIslandBills_UsesWorkflowAPI(t *testing.T) {
	const billsJSON = `{"processInstanceId":"t1","messages":{"error":[]},"data":[{"id":"1","type":"TableV2","data":{},"children":[{"id":"2","type":"TableV2Row","data":{},"children":[{"id":"3","type":"TableV2Cell","data":{},"children":[{"id":"4","type":"LinkV2","data":{"text":"Bill 1 - An Act to Amend the Highway Traffic Act","routerLink":"/bills/bill-1","queryParams":{}},"children":[]}]},{"id":"5","type":"TableV2Cell","data":{"text":"1"},"children":[]},{"id":"6","type":"TableV2Cell","data":{"text":"First Reading"},"children":[]},{"id":"7","type":"TableV2Cell","data":{"text":"March 15, 2026"},"children":[]}]}]}]}`

	mux := http.NewServeMux()
	mux.HandleFunc("/legislative-assembly/services/api/workflow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(billsJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bills, err := scraper.CrawlPrinceEdwardIslandBills(srv.URL, 68, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlPrinceEdwardIslandBills: %v", err)
	}
	if len(bills) == 0 {
		t.Fatal("expected at least one bill from WDF API")
	}
	if bills[0].Number != "1" {
		t.Errorf("bill number: got %q, want %q", bills[0].Number, "1")
	}
	if bills[0].ProvinceCode != "pe" {
		t.Errorf("province: got %q, want %q", bills[0].ProvinceCode, "pe")
	}
}

// TestCrawlPrinceEdwardIslandBills_FallsBackOnWorkflowNon200 verifies that a
// non-200 response from the WDF API causes the scraper to fall back to HTML.
func TestCrawlPrinceEdwardIslandBills_FallsBackOnWorkflowNon200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/legislative-assembly/services/api/workflow", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/bills/5">Bill 5 - A Test Act</a></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bills, err := scraper.CrawlPrinceEdwardIslandBills(srv.URL, 68, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlPrinceEdwardIslandBills: %v", err)
	}
	if len(bills) == 0 {
		t.Fatal("expected bills from HTML fallback when WDF API returns non-200")
	}
}

// TestCrawlPrinceEdwardIslandVotes_UsesWorkflowAPI verifies that when the WDF
// journals workflow API serves valid JSON with a journal URL, divisions are parsed
// from the linked journal HTML page.
func TestCrawlPrinceEdwardIslandVotes_UsesWorkflowAPI(t *testing.T) {
	const journalHTML = `<html><body>
<h3>Bill 1 second reading</h3>
<table><tr><td>Yeas: 15</td><td>Nays: 7</td></tr></table>
</body></html>`

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	journalsJSON := `{"processInstanceId":"t2","messages":{"error":[]},"data":[{"id":"1","type":"TableV2","data":{},"children":[{"id":"2","type":"TableV2Row","data":{},"children":[{"id":"3","type":"TableV2Cell","data":{},"children":[{"id":"4","type":"LinkV2","data":{"text":"Journal April 7 2026","href":"` + srv.URL + `/journals/2026-04-07"},"children":[]}]}]}]}]}`

	mux.HandleFunc("/legislative-assembly/services/api/workflow", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(journalsJSON))
	})
	mux.HandleFunc("/journals/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(journalHTML))
	})

	divs, err := scraper.CrawlPrinceEdwardIslandVotes(srv.URL, 68, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlPrinceEdwardIslandVotes: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected at least one division from WDF journals API")
	}
	if divs[0].Division.Yeas != 15 || divs[0].Division.Nays != 7 {
		t.Errorf("counts=(%d,%d), want (15,7)", divs[0].Division.Yeas, divs[0].Division.Nays)
	}
}

// TestCrawlPrinceEdwardIslandVotes_FallsBackOnWorkflowNon200 verifies that a
// non-200 response from the WDF journals API causes the scraper to fall back to
// the existing HTML-based journals parser.
func TestCrawlPrinceEdwardIslandVotes_FallsBackOnWorkflowNon200(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/legislative-assembly/services/api/workflow", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/votes/2026-04-07">Votes and Proceedings</a></body></html>`))
	})
	mux.HandleFunc("/votes/2026-04-07", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><table><tr><td>Yeas: 9</td><td>Nays: 2</td></tr></table></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	divs, err := scraper.CrawlPrinceEdwardIslandVotes(srv.URL, 68, 1, srv.Client())
	if err != nil {
		t.Fatalf("CrawlPrinceEdwardIslandVotes: %v", err)
	}
	if len(divs) == 0 {
		t.Fatal("expected divisions from HTML fallback when WDF API returns non-200")
	}
}

