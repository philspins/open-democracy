package templates

import (
	"sync"
	"testing"

	"github.com/a-h/templ"
	"github.com/philspins/open-democracy/internal/store"
)

func TestLoadPartyTheme_NoEnvVarFallbacksSuccessfully(t *testing.T) {
	// Ensure PARTY_THEME_FILE is not set so loader must use fallback behavior.
	t.Setenv("PARTY_THEME_FILE", "")

	// Reset package-level cache for deterministic test behavior.
	oldOnce := partyThemeOnce
	oldCfg := partyThemeCfg
	partyThemeOnce = sync.Once{}
	partyThemeCfg = PartyThemeConfig{}
	defer func() {
		partyThemeOnce = oldOnce
		partyThemeCfg = oldCfg
	}()

	cfg := loadPartyTheme()
	if cfg.FederalDefaultParty == "" {
		t.Fatalf("expected FederalDefaultParty to be populated when PARTY_THEME_FILE is unset")
	}
	if len(cfg.PartyStyleRules) == 0 {
		t.Fatalf("expected PartyStyleRules to be populated when PARTY_THEME_FILE is unset")
	}
	if len(cfg.ProvinceGoverningParty) == 0 {
		t.Fatalf("expected ProvinceGoverningParty to be populated when PARTY_THEME_FILE is unset")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"Hello", 10, "Hello"},
		{"Hello World", 5, "Hello…"},
		{"Hello World", 11, "Hello World"},
		{"", 5, ""},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d): got %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestSafeMailtoURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  templ.SafeURL
	}{
		{"empty string returns #", "", templ.SafeURL("#")},
		{"valid email", "user@example.com", templ.SafeURL("mailto:user@example.com")},
		{"valid email with subdomain", "user@mail.example.com", templ.SafeURL("mailto:user@mail.example.com")},
		{"newline injection blocked", "user@example.com\r\nBcc: evil@example.com", templ.SafeURL("#")},
		{"tab blocked", "user@example.com\t", templ.SafeURL("#")},
		{"query param injection blocked", "user@example.com?subject=spam&bcc=evil@x.com", templ.SafeURL("#")},
		{"missing @ blocked", "userexample.com", templ.SafeURL("#")},
		{"leading @ blocked", "@example.com", templ.SafeURL("#")},
		{"trailing @ blocked", "user@", templ.SafeURL("#")},
		{"multiple @ blocked", "user@host@example.com", templ.SafeURL("#")},
		{"domain without dot blocked", "user@localhost", templ.SafeURL("#")},
		{"HTML injection blocked", "user@example.com<script>", templ.SafeURL("#")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeMailtoURL(tt.input)
			if got != tt.want {
				t.Errorf("safeMailtoURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeExternalURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  templ.SafeURL
	}{
		{"empty string returns #", "", templ.SafeURL("#")},
		{"http URL allowed", "http://example.com", templ.SafeURL("http://example.com")},
		{"https URL allowed", "https://example.com/path?q=1", templ.SafeURL("https://example.com/path?q=1")},
		{"javascript scheme blocked", "javascript:alert(1)", templ.SafeURL("#")},
		{"data scheme blocked", "data:text/html,<h1>xss</h1>", templ.SafeURL("#")},
		{"vbscript scheme blocked", "vbscript:msgbox(1)", templ.SafeURL("#")},
		{"ftp scheme blocked", "ftp://example.com", templ.SafeURL("#")},
		{"relative URL blocked", "/relative/path", templ.SafeURL("#")},
		{"uppercase HTTPS allowed", "HTTPS://example.com", templ.SafeURL("HTTPS://example.com")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeExternalURL(tt.input)
			if got != tt.want {
				t.Errorf("safeExternalURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBillLevelLabel(t *testing.T) {
	tests := []struct {
		name string
		bill store.BillRow
		want string
	}{
		{name: "federal commons", bill: store.BillRow{ID: "45-1-c-47", Chamber: "commons"}, want: "Federal"},
		{name: "federal senate", bill: store.BillRow{ID: "45-1-s-209", Chamber: "senate"}, want: "Federal"},
		{name: "provincial chamber", bill: store.BillRow{ID: "on-43-1-12", Chamber: "ontario"}, want: "Provincial"},
		{name: "provincial id fallback", bill: store.BillRow{ID: "ab-31-1-10", Chamber: ""}, want: "Provincial"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BillLevelLabel(tt.bill); got != tt.want {
				t.Fatalf("BillLevelLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
