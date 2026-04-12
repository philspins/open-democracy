package templates

import (
	"sync"
	"testing"
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
