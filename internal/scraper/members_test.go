package scraper_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

// newJSONTestServer starts a test server that always returns body as JSON.
func newJSONTestServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(body))
	}))
}

// ── NormaliseVote ─────────────────────────────────────────────────────────────

func TestNormaliseVote_YeaVariants(t *testing.T) {
	for _, raw := range []string{"Yea", "yea", "Yes", "yes", "Pour", "pour", "Oui", "oui"} {
		if got := scraper.NormaliseVote(raw); got != "Yea" {
			t.Errorf("NormaliseVote(%q) = %q, want Yea", raw, got)
		}
	}
}

func TestNormaliseVote_NayVariants(t *testing.T) {
	for _, raw := range []string{"Nay", "nay", "No", "no", "Contre", "contre", "Non", "non"} {
		if got := scraper.NormaliseVote(raw); got != "Nay" {
			t.Errorf("NormaliseVote(%q) = %q, want Nay", raw, got)
		}
	}
}

func TestNormaliseVote_Paired(t *testing.T) {
	for _, raw := range []string{"Paired", "paired"} {
		if got := scraper.NormaliseVote(raw); got != "Paired" {
			t.Errorf("NormaliseVote(%q) = %q, want Paired", raw, got)
		}
	}
}

func TestNormaliseVote_UnknownBecomesAbstain(t *testing.T) {
	for _, raw := range []string{"Absent", "", "unknown"} {
		if got := scraper.NormaliseVote(raw); got != "Abstain" {
			t.Errorf("NormaliseVote(%q) = %q, want Abstain", raw, got)
		}
	}
}

// ── CrawlMembersList ──────────────────────────────────────────────────────────

const sampleMembersHTML = `<html><body>
  <div class="ce-mip-mp-tile">
    <a href="/Members/en/111">
      <span class="ce-mip-mp-name">Jane Doe</span>
    </a>
    <span class="ce-mip-mp-party">Liberal</span>
    <span class="ce-mip-mp-constituency">Ottawa Centre</span>
    <span class="ce-mip-mp-province">Ontario</span>
  </div>
  <div class="ce-mip-mp-tile">
    <a href="/Members/en/222">
      <span class="ce-mip-mp-name">John Smith</span>
    </a>
    <span class="ce-mip-mp-party">Conservative</span>
    <span class="ce-mip-mp-constituency">Calgary East</span>
    <span class="ce-mip-mp-province">Alberta</span>
  </div>
</body></html>`

func TestCrawlMembersList_ParsesTiles(t *testing.T) {
	srv := newTestServer(sampleMembersHTML)
	defer srv.Close()

	stubs, err := scraper.CrawlMembersList(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlMembersList: %v", err)
	}
	if len(stubs) != 2 {
		t.Errorf("len=%d, want 2", len(stubs))
	}
}

func TestCrawlMembersList_ParsesFirstMember(t *testing.T) {
	srv := newTestServer(sampleMembersHTML)
	defer srv.Close()

	stubs, _ := scraper.CrawlMembersList(srv.URL, srv.Client())
	m := stubs[0]
	if m.ID != "111" {
		t.Errorf("ID=%q want 111", m.ID)
	}
	if m.Name != "Jane Doe" {
		t.Errorf("Name=%q want Jane Doe", m.Name)
	}
	if m.Party != "Liberal" {
		t.Errorf("Party=%q want Liberal", m.Party)
	}
	if m.Riding != "Ottawa Centre" {
		t.Errorf("Riding=%q want Ottawa Centre", m.Riding)
	}
	if m.Province != "Ontario" {
		t.Errorf("Province=%q want Ontario", m.Province)
	}
	if m.Chamber != "commons" {
		t.Errorf("Chamber=%q want commons", m.Chamber)
	}
}

func TestCrawlMembersList_ErrorOnBadServer(t *testing.T) {
	_, err := scraper.CrawlMembersList("http://localhost:0/no-server", nil)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestCrawlMembersList_EmptyWhenNoTiles(t *testing.T) {
	srv := newTestServer("<html><body><p>No members</p></body></html>")
	defer srv.Close()

	stubs, err := scraper.CrawlMembersList(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stubs) != 0 {
		t.Errorf("expected 0 stubs, got %d", len(stubs))
	}
}

// ── CrawlMemberProfile ────────────────────────────────────────────────────────

const sampleProfileHTML = `<html><body>
  <h1 class="ce-mip-mp-name">Jane Doe</h1>
  <span class="ce-mip-mp-party">Liberal</span>
  <span class="ce-mip-mp-constituency">Ottawa Centre</span>
  <span class="ce-mip-mp-province">Ontario</span>
  <div class="ce-mip-mp-role">Member of Parliament</div>
  <div class="ce-mip-mp-picture">
    <img src="/photo/123006.jpg" alt="Jane Doe photo">
  </div>
  <a href="mailto:jane.doe@parl.gc.ca">jane.doe@parl.gc.ca</a>
</body></html>`

func TestCrawlMemberProfile_ParsesName(t *testing.T) {
	srv := newTestServer(sampleProfileHTML)
	defer srv.Close()

	profile, err := scraper.CrawlMemberProfile("123006", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlMemberProfile: %v", err)
	}
	if profile.Name != "Jane Doe" {
		t.Errorf("Name=%q want Jane Doe", profile.Name)
	}
}

func TestCrawlMemberProfile_ParsesParty(t *testing.T) {
	srv := newTestServer(sampleProfileHTML)
	defer srv.Close()

	profile, _ := scraper.CrawlMemberProfile("123006", srv.URL, srv.Client())
	if profile.Party != "Liberal" {
		t.Errorf("Party=%q want Liberal", profile.Party)
	}
}

func TestCrawlMemberProfile_ParsesEmail(t *testing.T) {
	srv := newTestServer(sampleProfileHTML)
	defer srv.Close()

	profile, _ := scraper.CrawlMemberProfile("123006", srv.URL, srv.Client())
	if profile.Email != "jane.doe@parl.gc.ca" {
		t.Errorf("Email=%q want jane.doe@parl.gc.ca", profile.Email)
	}
}

func TestCrawlMemberProfile_ParsesPhotoURL(t *testing.T) {
	srv := newTestServer(sampleProfileHTML)
	defer srv.Close()

	profile, _ := scraper.CrawlMemberProfile("123006", srv.URL, srv.Client())
	want := "https://www.ourcommons.ca/photo/123006.jpg"
	if profile.PhotoURL != want {
		t.Errorf("PhotoURL=%q want %q", profile.PhotoURL, want)
	}
}

func TestCrawlMemberProfile_PreservesIDOnError(t *testing.T) {
	// Even when the server is unreachable we still get a stub back (no panic).
	profile, _ := scraper.CrawlMemberProfile("123006", "http://localhost:0/no-server", nil)
	if profile.ID != "123006" {
		t.Errorf("ID=%q want 123006", profile.ID)
	}
}

// ── CrawlMembersFromAPI ───────────────────────────────────────────────────────

const sampleAPIResponse = `{
  "objects": [
    {
      "name": "Jane Doe",
      "party_name": "Liberal",
      "district_name": "Ottawa Centre",
      "email": "jane.doe@parl.gc.ca",
      "url": "https://www.ourcommons.ca/Members/en/jane-doe(111)",
      "personal_url": "https://janedoe.ca",
      "photo_url": "https://www.ourcommons.ca/photo/111.jpg",
      "offices": [
        {"type": "legislature", "postal": "House of Commons\nOttawa ON  K1A 0A6"},
        {"type": "constituency", "postal": "Main office\n123 Main St\nOttawa ON  K1A 0A6"}
      ],
      "extra": {}
    },
    {
      "name": "John Smith",
      "party_name": "Conservative",
      "district_name": "Calgary East",
      "email": "john.smith@parl.gc.ca",
      "url": "https://www.ourcommons.ca/Members/en/john-smith(222)",
      "personal_url": "",
      "photo_url": "https://www.ourcommons.ca/photo/222.jpg",
      "offices": [
        {"type": "constituency", "postal": "555 9 Ave SW\nCalgary AB  T2P 3S5"}
      ],
      "extra": {"roles": ["Minister of Finance"]}
    }
  ],
  "meta": {"next": null}
}`

func TestCrawlMembersFromAPI_ReturnsTwoProfiles(t *testing.T) {
	srv := newJSONTestServer(sampleAPIResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlMembersFromAPI: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("len=%d, want 2", len(profiles))
	}
}

func TestCrawlMembersFromAPI_ParsesFirstMember(t *testing.T) {
	srv := newJSONTestServer(sampleAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	m := profiles[0]
	if m.ID != "111" {
		t.Errorf("ID=%q want 111", m.ID)
	}
	if m.Name != "Jane Doe" {
		t.Errorf("Name=%q want Jane Doe", m.Name)
	}
	if m.Party != "Liberal" {
		t.Errorf("Party=%q want Liberal", m.Party)
	}
	if m.Riding != "Ottawa Centre" {
		t.Errorf("Riding=%q want Ottawa Centre", m.Riding)
	}
	if m.Province != "Ontario" {
		t.Errorf("Province=%q want Ontario", m.Province)
	}
	if m.Email != "jane.doe@parl.gc.ca" {
		t.Errorf("Email=%q want jane.doe@parl.gc.ca", m.Email)
	}
	if m.PhotoURL != "https://www.ourcommons.ca/photo/111.jpg" {
		t.Errorf("PhotoURL=%q want https://www.ourcommons.ca/photo/111.jpg", m.PhotoURL)
	}
	if m.Website != "https://janedoe.ca" {
		t.Errorf("Website=%q want https://janedoe.ca", m.Website)
	}
	if m.Chamber != "commons" {
		t.Errorf("Chamber=%q want commons", m.Chamber)
	}
	if !m.Active {
		t.Error("Active should be true")
	}
	if m.GovernmentLevel != "federal" {
		t.Errorf("GovernmentLevel=%q want federal", m.GovernmentLevel)
	}
}

func TestCrawlMembersFromAPI_ParsesRoleFromExtra(t *testing.T) {
	srv := newJSONTestServer(sampleAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if profiles[1].Role != "Minister of Finance" {
		t.Errorf("Role=%q want Minister of Finance", profiles[1].Role)
	}
}

func TestCrawlMembersFromAPI_DefaultRoleWhenExtraEmpty(t *testing.T) {
	srv := newJSONTestServer(sampleAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if profiles[0].Role != "Member of Parliament" {
		t.Errorf("Role=%q want Member of Parliament", profiles[0].Role)
	}
}

func TestCrawlMembersFromAPI_ParsesProvinceFromOffices(t *testing.T) {
	srv := newJSONTestServer(sampleAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if profiles[1].Province != "Alberta" {
		t.Errorf("Province=%q want Alberta", profiles[1].Province)
	}
}

func TestCrawlMembersFromAPI_ErrorOnBadServer(t *testing.T) {
	_, err := scraper.CrawlMembersFromAPI("http://localhost:0/no-server", nil)
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestCrawlMembersFromAPI_EmptyOnNoObjects(t *testing.T) {
	srv := newJSONTestServer(`{"objects":[],"meta":{"next":null}}`)
	defer srv.Close()

	profiles, err := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestCrawlMembersFromAPI_FollowsPagination(t *testing.T) {
	page1 := `{"objects":[{"name":"Jane Doe","party_name":"Liberal","district_name":"Ottawa Centre","email":"","url":"https://www.ourcommons.ca/Members/en/jane-doe(111)","personal_url":"","photo_url":"","offices":[],"extra":{}}],"meta":{"next":"/page2"}}`
	page2 := `{"objects":[{"name":"John Smith","party_name":"Conservative","district_name":"Calgary East","email":"","url":"https://www.ourcommons.ca/Members/en/john-smith(222)","personal_url":"","photo_url":"","offices":[],"extra":{}}],"meta":{"next":null}}`

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/page2" {
			w.Write([]byte(page2))
		} else {
			w.Write([]byte(page1))
		}
	}))
	defer srv.Close()

	profiles, err := scraper.CrawlMembersFromAPI(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlMembersFromAPI: %v", err)
	}
	if len(profiles) != 2 {
		t.Errorf("len=%d, want 2 (expected pagination to be followed)", len(profiles))
	}
}

// ── CrawlProvincialMembersFromAPI ─────────────────────────────────────────────

const sampleProvincialAPIResponse = `{
  "objects": [
    {
      "name": "Laura Smith",
      "party_name": "Progressive Conservative Party of Ontario",
      "district_name": "Thornhill",
      "email": "laura.smith@pc.ola.org",
      "url": "https://www.ola.org/en/members/all/laura-smith",
      "personal_url": "",
      "photo_url": "https://www.ola.org/sites/default/files/Laura_Smith.png",
      "elected_office": "MPP",
      "offices": [
        {"type": "constituency", "postal": "1136 Centre St.\nThornhill ON  L4J 3M8"}
      ],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

const sampleProvincialRelativePhotoAPIResponse = `{
  "objects": [
    {
      "name": "Marc Lagasse",
      "party_name": "Party",
      "district_name": "District",
      "email": "",
      "url": "https://www.gov.mb.ca/legislature/members/mla_list_bios_details.html?constituency=lagasse",
      "personal_url": "",
      "photo_url": "/legislature/members/images/lagasse.jpg",
      "elected_office": "MLA",
      "offices": [],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

func TestCrawlProvincialMembersFromAPI_ReturnsProfile(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialAPIResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("ontario-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len=%d, want 1", len(profiles))
	}
}

func TestCrawlProvincialMembersFromAPI_GovernmentLevel(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlProvincialMembersFromAPI("ontario-legislature", srv.URL, srv.Client())
	if profiles[0].GovernmentLevel != "provincial" {
		t.Errorf("GovernmentLevel=%q want provincial", profiles[0].GovernmentLevel)
	}
}

func TestCrawlProvincialMembersFromAPI_IDFromSetSlugAndURLSlug(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlProvincialMembersFromAPI("ontario-legislature", srv.URL, srv.Client())
	wantID := "ontario-legislature-laura-smith"
	if profiles[0].ID != wantID {
		t.Errorf("ID=%q want %q", profiles[0].ID, wantID)
	}
}

func TestCrawlProvincialMembersFromAPI_ElectedOfficeAsRole(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlProvincialMembersFromAPI("ontario-legislature", srv.URL, srv.Client())
	if profiles[0].Role != "MPP" {
		t.Errorf("Role=%q want MPP", profiles[0].Role)
	}
}

func TestCrawlProvincialMembersFromAPI_ProvinceFromOffices(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialAPIResponse)
	defer srv.Close()

	profiles, _ := scraper.CrawlProvincialMembersFromAPI("ontario-legislature", srv.URL, srv.Client())
	if profiles[0].Province != "Ontario" {
		t.Errorf("Province=%q want Ontario", profiles[0].Province)
	}
}

func TestCrawlProvincialMembersFromAPI_ResolvesRelativePhotoURL(t *testing.T) {
	srv := newJSONTestServer(sampleProvincialRelativePhotoAPIResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("manitoba-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len=%d, want 1", len(profiles))
	}
	want := "https://www.gov.mb.ca/legislature/members/images/lagasse.jpg"
	if profiles[0].PhotoURL != want {
		t.Errorf("PhotoURL=%q want %q", profiles[0].PhotoURL, want)
	}
}

func TestCrawlProvincialMembersFromAPI_ErrorOnUnknownSetWithNoURL(t *testing.T) {
	_, err := scraper.CrawlProvincialMembersFromAPI("nonexistent-set", "", nil)
	if err == nil {
		t.Error("expected error for unknown set slug with no apiURL")
	}
}

// ── urlLastSegment / province fallback ───────────────────────────────────────

// sampleNBAPIResponse simulates a Represent API response for NB (no offices).
const sampleNBAPIResponse = `{
  "objects": [
    {
      "name": "Jane Doe",
      "party_name": "Progressive Conservative Party",
      "district_name": "Moncton Centre",
      "email": "",
      "url": "https://represent.opennorth.ca/representatives/nb-legislature/42/",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MLA",
      "offices": [],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

// sampleQueryStringURLResponse simulates a member whose API URL has a query string.
const sampleQueryStringURLResponse = `{
  "objects": [
    {
      "name": "Bob Jones",
      "party_name": "Progressive Conservative",
      "district_name": "Calgary East",
      "email": "",
      "url": "https://www.assembly.ab.ca/members/mla?SpecificMember=42",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MLA",
      "offices": [
        {"type": "constituency", "postal": "123 Main St\nCalgary AB  T2P 1E3"}
      ],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

const sampleIndexHTMLURLResponse = `{
  "objects": [
    {
      "name": "François Bonnardel",
      "party_name": "Coalition Avenir Québec",
      "district_name": "Granby",
      "email": "",
      "url": "http://www.assnat.qc.ca/fr/deputes/bonnardel-francois-11/index.html",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MNA",
      "offices": [],
      "extra": {}
    },
    {
      "name": "Christine Fréchette",
      "party_name": "Coalition Avenir Québec",
      "district_name": "Sanguinet",
      "email": "",
      "url": "http://www.assnat.qc.ca/fr/deputes/frechette-christine-18385/index.html",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MNA",
      "offices": [],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

func TestCrawlProvincialMembersFromAPI_ProvinceFromSetSlugWhenNoOffices(t *testing.T) {
	srv := newJSONTestServer(sampleNBAPIResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("nb-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len=%d, want 1", len(profiles))
	}
	if profiles[0].Province != "New Brunswick" {
		t.Errorf("Province=%q want New Brunswick (derived from set slug)", profiles[0].Province)
	}
}

func TestCrawlProvincialMembersFromAPI_IDFromQueryStringURL(t *testing.T) {
	// When the member URL contains a query string the ID must not include it;
	// the query-string segment after ? must be stripped.
	srv := newJSONTestServer(sampleQueryStringURLResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("alberta-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len=%d, want 1", len(profiles))
	}
	// The last path segment of the URL (before the query string) is "mla",
	// not "mla?SpecificMember=42", so the ID must NOT contain "?".
	id := profiles[0].ID
	if strings.Contains(id, "?") {
		t.Errorf("ID=%q must not contain a '?' character", id)
	}
}

func TestCrawlProvincialMembersFromAPI_IDFromIndexHTMLURLUsesParentSegment(t *testing.T) {
	srv := newJSONTestServer(sampleIndexHTMLURLResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("quebec-assemblee-nationale", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("len=%d, want 2", len(profiles))
	}
	if profiles[0].ID == profiles[1].ID {
		t.Fatalf("expected unique IDs, got duplicate %q", profiles[0].ID)
	}
	if !strings.Contains(profiles[0].ID, "bonnardel-francois-11") {
		t.Fatalf("unexpected first ID: %q", profiles[0].ID)
	}
	if !strings.Contains(profiles[1].ID, "frechette-christine-18385") {
		t.Fatalf("unexpected second ID: %q", profiles[1].ID)
	}
}

// ── CrawlNewBrunswickMembersFromWebsite ───────────────────────────────────────

const sampleNBWebsitePage = `<!DOCTYPE html><html><body>
<div class="member-card ">
  <div class="member-card-avatar">
    <img alt="Ames, Richard" src="/content/members/portraits/61-1/Richard_Ames_sm.jpg" />
  </div>
  <ul class="member-card-description">
    <li class="member-card-description-name">
      <a href="/en/members/current/165/ames-richard"><h3>Ames, Richard</h3></a>
    </li>
    <li class="member-card-description-party">
      <div class="member-card-party-dot" style="background-color:#005DAC"></div>Progressive Conservative Party
    </li>
    <li class="member-card-description-riding">
      <i class="fas fa-map-marker-alt"></i><span>Carleton-York</span>
    </li>
  </ul>
</div>
<div class="member-card ">
  <div class="member-card-avatar">
    <img alt="Boudreau, Lyne Chantal" src="/content/members/portraits/61-1/Lyne_Boudreau_sm.jpg" />
  </div>
  <ul class="member-card-description">
    <li class="member-card-description-name">
      <a href="/en/members/current/208/boudreau-lyne-chantal"><h3>Boudreau, Lyne Chantal</h3></a>
    </li>
    <li class="member-card-description-party">
      <div class="member-card-party-dot" style="background-color:#C0161D"></div>Liberal Party
    </li>
    <li class="member-card-description-riding">
      <i class="fas fa-map-marker-alt"></i><span>Champdoré-Irishtown</span>
    </li>
  </ul>
</div>
</body></html>`

func newHTMLTestServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(body))
	}))
}

func TestCrawlNewBrunswickMembersFromWebsite_ReturnsTwoProfiles(t *testing.T) {
	srv := newHTMLTestServer(sampleNBWebsitePage)
	defer srv.Close()

	profiles, err := scraper.CrawlNewBrunswickMembersFromWebsite(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlNewBrunswickMembersFromWebsite: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("len=%d, want 2", len(profiles))
	}
}

func TestCrawlNewBrunswickMembersFromWebsite_ParsesFirstMember(t *testing.T) {
	srv := newHTMLTestServer(sampleNBWebsitePage)
	defer srv.Close()

	profiles, _ := scraper.CrawlNewBrunswickMembersFromWebsite(srv.URL, srv.Client())
	p := profiles[0]

	if p.ID != "nb-legislature-ames-richard" {
		t.Errorf("ID=%q, want nb-legislature-ames-richard", p.ID)
	}
	if p.Name != "Richard Ames" {
		t.Errorf("Name=%q, want Richard Ames", p.Name)
	}
	if p.Party != "Progressive Conservative Party" {
		t.Errorf("Party=%q, want Progressive Conservative Party", p.Party)
	}
	if p.Riding != "Carleton-York" {
		t.Errorf("Riding=%q, want Carleton-York", p.Riding)
	}
	if p.Province != "New Brunswick" {
		t.Errorf("Province=%q, want New Brunswick", p.Province)
	}
	if p.GovernmentLevel != "provincial" {
		t.Errorf("GovernmentLevel=%q, want provincial", p.GovernmentLevel)
	}
}

func TestCrawlNewBrunswickMembersFromWebsite_MultiWordFirstName(t *testing.T) {
	// "Boudreau, Lyne Chantal" should become "Lyne Chantal Boudreau"
	srv := newHTMLTestServer(sampleNBWebsitePage)
	defer srv.Close()

	profiles, _ := scraper.CrawlNewBrunswickMembersFromWebsite(srv.URL, srv.Client())
	p := profiles[1]

	if p.Name != "Lyne Chantal Boudreau" {
		t.Errorf("Name=%q, want 'Lyne Chantal Boudreau'", p.Name)
	}
	if p.ID != "nb-legislature-boudreau-lyne-chantal" {
		t.Errorf("ID=%q, want nb-legislature-boudreau-lyne-chantal", p.ID)
	}
}

func TestCrawlNewBrunswickMembersFromWebsite_ErrorOnBadServer(t *testing.T) {
	_, err := scraper.CrawlNewBrunswickMembersFromWebsite("http://localhost:0/no-server", nil)
	if err == nil {
		t.Error("expected error for bad server")
	}
}

// ── AB / SK generic-page-segment fallback ────────────────────────────────────

// sampleABGenericURLResponse simulates two Alberta members who share the same
// last path segment ("member-information") and differ only by query params.
// Before the fix, both would get ID "alberta-legislature-member-information".
const sampleABGenericURLResponse = `{
  "objects": [
    {
      "name": "Irfan Sabir",
      "party_name": "NDP",
      "district_name": "Calgary-McCall",
      "email": "",
      "url": "https://www.assembly.ab.ca/members/members-of-the-legislative-assembly/member-information?mid=0871&legl=31",
      "personal_url": "",
      "photo_url": "https://www.assembly.ab.ca/images/default-source/members/mla-photos/ph-mla0871.jpg",
      "elected_office": "MLA",
      "offices": [{"type": "constituency", "postal": "123 Main St\nCalgary AB  T2P 1E3"}],
      "extra": {}
    },
    {
      "name": "Kathleen Ganley",
      "party_name": "NDP",
      "district_name": "Calgary-Mountain View",
      "email": "",
      "url": "https://www.assembly.ab.ca/members/members-of-the-legislative-assembly/member-information?mid=0846&legl=31",
      "personal_url": "",
      "photo_url": "https://www.assembly.ab.ca/images/default-source/members/mla-photos/ph-mla0846.jpg",
      "elected_office": "MLA",
      "offices": [{"type": "constituency", "postal": "456 Oak Ave\nCalgary AB  T2P 2E3"}],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

// sampleSKGenericURLResponse simulates two Saskatchewan members who share the
// last path segment ("member-details") and differ only by query params.
const sampleSKGenericURLResponse = `{
  "objects": [
    {
      "name": "Betty Nippi-Albright",
      "party_name": "NDP",
      "district_name": "Saskatoon Fairview",
      "email": "",
      "url": "http://www.legassembly.sk.ca/mlas/member-details?first=Betty&last=Nippi-Albright",
      "personal_url": "",
      "photo_url": "http://www.legassembly.sk.ca/media/abc123/nippi-albright_betty.jpg",
      "elected_office": "MLA",
      "offices": [{"type": "constituency", "postal": "789 Elm St\nSaskatoon SK  S7K 1E3"}],
      "extra": {}
    },
    {
      "name": "Tim McLeod",
      "party_name": "Saskatchewan Party",
      "district_name": "Regina Northeast",
      "email": "",
      "url": "http://www.legassembly.sk.ca/mlas/member-details?first=Tim&last=McLeod",
      "personal_url": "",
      "photo_url": "http://www.legassembly.sk.ca/media/def456/mcleod_tim.jpg",
      "elected_office": "MLA",
      "offices": [{"type": "constituency", "postal": "10 Broad St\nRegina SK  S4P 1E1"}],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

func TestCrawlProvincialMembersFromAPI_ABGenericPageFallsBackToName(t *testing.T) {
	// Alberta member URLs all end in "member-information"; without the fix,
	// both members would get ID "alberta-legislature-member-information".
	srv := newJSONTestServer(sampleABGenericURLResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("alberta-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("len=%d, want 2", len(profiles))
	}
	// Both IDs must be unique and must NOT contain the generic page name.
	if profiles[0].ID == profiles[1].ID {
		t.Fatalf("expected unique IDs, got duplicate %q", profiles[0].ID)
	}
	for _, p := range profiles {
		if strings.Contains(p.ID, "member-information") {
			t.Errorf("ID=%q still contains generic page segment 'member-information'", p.ID)
		}
		if strings.Contains(p.ID, "?") {
			t.Errorf("ID=%q must not contain a '?' character", p.ID)
		}
	}
	// Verify name-based slugs.
	if profiles[0].ID != "alberta-legislature-irfan-sabir" {
		t.Errorf("ID=%q, want alberta-legislature-irfan-sabir", profiles[0].ID)
	}
	if profiles[1].ID != "alberta-legislature-kathleen-ganley" {
		t.Errorf("ID=%q, want alberta-legislature-kathleen-ganley", profiles[1].ID)
	}
}

func TestCrawlProvincialMembersFromAPI_SKGenericPageFallsBackToName(t *testing.T) {
	// Saskatchewan member URLs all end in "member-details"; without the fix,
	// both members would get ID "saskatchewan-legislature-member-details".
	srv := newJSONTestServer(sampleSKGenericURLResponse)
	defer srv.Close()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("saskatchewan-legislature", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("len=%d, want 2", len(profiles))
	}
	if profiles[0].ID == profiles[1].ID {
		t.Fatalf("expected unique IDs, got duplicate %q", profiles[0].ID)
	}
	for _, p := range profiles {
		if strings.Contains(p.ID, "member-details") {
			t.Errorf("ID=%q still contains generic page segment 'member-details'", p.ID)
		}
	}
	// Verify name-based slugs.
	if profiles[0].ID != "saskatchewan-legislature-betty-nippi-albright" {
		t.Errorf("ID=%q, want saskatchewan-legislature-betty-nippi-albright", profiles[0].ID)
	}
	if profiles[1].ID != "saskatchewan-legislature-tim-mcleod" {
		t.Errorf("ID=%q, want saskatchewan-legislature-tim-mcleod", profiles[1].ID)
	}
}

// ── NL photo enrichment ───────────────────────────────────────────────────────

// sampleNLMembersJS is a minimal subset of the NL members-index.js file.
const sampleNLMembersJS = `var members = [
    {
        name: '<a href="/Members/YourMember/DwyerJeff.aspx">Dwyer, Jeff</a>',
        district: 'Placentia West - Bellevue',
        party: 'Progressive Conservative',
        phone: '(709) 279-2912',
        email: '<a href="mailto:MHAJeffDwyer@assembly.nl.ca">MHAJeffDwyer@assembly.nl.ca</a>'
    },
    {
        name: '<a href="/Members/YourMember/GambinWalshSherry.aspx">Gambin-Walsh, Sherry</a>',
        district: 'Placentia - St. Mary\'s',
        party: 'Liberal',
        phone: '(709) 227-1304',
        email: '<a href="mailto:MHASherryGambinWalsh@assembly.nl.ca">MHASherryGambinWalsh@assembly.nl.ca</a>'
    },
    {
        name: '<a href="/Members/YourMember/ODriscollLoyola.aspx">O\'Driscoll, Loyola</a>',
        district: 'Ferryland',
        party: 'Progressive Conservative',
        phone: '(709) 432-3211',
        email: '<a href="mailto:MHALoyolaODriscoll@assembly.nl.ca">MHALoyolaODriscoll@assembly.nl.ca</a>'
    }
];`

// sampleNLAPIResponse is a minimal Represent API response for NL (no photo URLs).
const sampleNLAPIResponse = `{
  "objects": [
    {
      "name": "Jeff Dwyer",
      "party_name": "Progressive Conservative",
      "district_name": "Placentia West\u2014Bellevue",
      "email": "MHAJeffDwyer@assembly.nl.ca",
      "url": "",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MHA",
      "offices": [{"type": "legislature", "postal": "PO Box 8700 St. John's NL  A1B 4J6"}],
      "extra": {}
    },
    {
      "name": "Sherry Gambin-Walsh",
      "party_name": "Liberal",
      "district_name": "Placentia\u2014St. Mary's",
      "email": "MHASherryGambinWalsh@assembly.nl.ca",
      "url": "",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MHA",
      "offices": [{"type": "legislature", "postal": "PO Box 8700 St. John's NL  A1B 4J6"}],
      "extra": {}
    },
    {
      "name": "Loyola O'Driscoll",
      "party_name": "Progressive Conservative",
      "district_name": "Ferryland",
      "email": "MHALoyolaODriscoll@assembly.nl.ca",
      "url": "",
      "personal_url": "",
      "photo_url": "",
      "elected_office": "MHA",
      "offices": [{"type": "legislature", "postal": "PO Box 8700 St. John's NL  A1B 4J6"}],
      "extra": {}
    }
  ],
  "meta": {"next": null}
}`

func newTextTestServer(contentType, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write([]byte(body))
	}))
}

func TestCrawlProvincialMembersFromAPI_NLEnrichesPhotos(t *testing.T) {
	// Set up two servers: one returns the Represent API JSON, the other serves
	// the NL members-index.js stub.
	apiSrv := newJSONTestServer(sampleNLAPIResponse)
	defer apiSrv.Close()
	jsSrv := newTextTestServer("application/javascript", sampleNLMembersJS)
	defer jsSrv.Close()

	// Inject JS URL override via EnrichNLMemberPhotosURL.
	scraper.NLMembersJSURLOverride = jsSrv.URL
	defer func() { scraper.NLMembersJSURLOverride = "" }()

	profiles, err := scraper.CrawlProvincialMembersFromAPI("newfoundland-labrador-legislature", apiSrv.URL, apiSrv.Client())
	if err != nil {
		t.Fatalf("CrawlProvincialMembersFromAPI: %v", err)
	}
	if len(profiles) != 3 {
		t.Fatalf("len=%d, want 3", len(profiles))
	}

	byName := make(map[string]string)
	for _, p := range profiles {
		byName[p.Name] = p.PhotoURL
	}

	wantPhotos := map[string]string{
		"Jeff Dwyer":          "https://www.assembly.nl.ca/Members/YourMember/BioPhotos/DwyerJeff.jpg",
		"Sherry Gambin-Walsh": "https://www.assembly.nl.ca/Members/YourMember/BioPhotos/GambinWalshSherry.jpg",
		"Loyola O'Driscoll":   "https://www.assembly.nl.ca/Members/YourMember/BioPhotos/ODriscollLoyola.jpg",
	}

	for name, wantPhoto := range wantPhotos {
		if got := byName[name]; got != wantPhoto {
			t.Errorf("member %q: PhotoURL=%q, want %q", name, got, wantPhoto)
		}
	}
}
