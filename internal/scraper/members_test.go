package scraper_test

import (
	"testing"

	"github.com/philspins/open-democracy/internal/scraper"
)

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
