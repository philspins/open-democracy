// Package templates provides templ components and helper functions for Open Democracy.
package templates

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/philspins/open-democracy/internal/store"
)

type PartyStyleRule struct {
	Match string `json:"match"`
	Style string `json:"style"`
}

type PartyThemeConfig struct {
	FederalDefaultParty    string            `json:"federal_default_party"`
	DefaultStyle           string            `json:"default_style"`
	ProvinceFallbackParty  string            `json:"province_fallback_party"`
	PartyStyleRules        []PartyStyleRule  `json:"party_style_rules"`
	ProvinceGoverningParty map[string]string `json:"province_governing_party"`
}

var (
	partyThemeOnce sync.Once
	partyThemeCfg  PartyThemeConfig
)

func loadPartyTheme() PartyThemeConfig {
	partyThemeOnce.Do(func() {
		cfg := defaultPartyThemeConfig()
		path := os.Getenv("PARTY_THEME_FILE")
		if strings.TrimSpace(path) == "" {
			path = "config/party-theme.json"
		}
		if b, err := os.ReadFile(path); err == nil {
			var fileCfg PartyThemeConfig
			if json.Unmarshal(b, &fileCfg) == nil {
				cfg = mergePartyThemeConfig(cfg, fileCfg)
			}
		}
		partyThemeCfg = cfg
	})
	return partyThemeCfg
}

func mergePartyThemeConfig(base, override PartyThemeConfig) PartyThemeConfig {
	out := base
	if strings.TrimSpace(override.FederalDefaultParty) != "" {
		out.FederalDefaultParty = override.FederalDefaultParty
	}
	if strings.TrimSpace(override.DefaultStyle) != "" {
		out.DefaultStyle = override.DefaultStyle
	}
	if strings.TrimSpace(override.ProvinceFallbackParty) != "" {
		out.ProvinceFallbackParty = override.ProvinceFallbackParty
	}
	if len(override.PartyStyleRules) > 0 {
		out.PartyStyleRules = override.PartyStyleRules
	}
	if len(override.ProvinceGoverningParty) > 0 {
		if out.ProvinceGoverningParty == nil {
			out.ProvinceGoverningParty = map[string]string{}
		}
		for k, v := range override.ProvinceGoverningParty {
			out.ProvinceGoverningParty[strings.ToUpper(strings.TrimSpace(k))] = v
		}
	}
	return out
}

func defaultPartyThemeConfig() PartyThemeConfig {
	return PartyThemeConfig{
		FederalDefaultParty:   "Liberal",
		DefaultStyle:          "background:linear-gradient(90deg,#d4dde7,#b8c6d6);color:#1f3346",
		ProvinceFallbackParty: "Government Party",
		PartyStyleRules: []PartyStyleRule{
			{Match: "progressive conservative", Style: "background:linear-gradient(90deg,#4f8ff0,#3d74c1);color:#082348"},
			{Match: "united conservative", Style: "background:linear-gradient(90deg,#3f7fdd,#2e63b3);color:#071d3c"},
			{Match: "conservative", Style: "background:linear-gradient(90deg,#4c8fe9,#3f77c8);color:#082348"},
			{Match: "liberal", Style: "background:linear-gradient(90deg,#ef7d7d,#db5353);color:#4b0f0f"},
			{Match: "ndp", Style: "background:linear-gradient(90deg,#f4b060,#e79335);color:#4b2a08"},
			{Match: "new democrat", Style: "background:linear-gradient(90deg,#f4b060,#e79335);color:#4b2a08"},
			{Match: "green", Style: "background:linear-gradient(90deg,#92cc7e,#65ad4b);color:#16360d"},
			{Match: "bloc", Style: "background:linear-gradient(90deg,#8dc9f4,#59a7dd);color:#0f3252"},
			{Match: "coalition avenir quebec", Style: "background:linear-gradient(90deg,#79b7e6,#4f8fcd);color:#0d2b45"},
			{Match: "saskatchewan party", Style: "background:linear-gradient(90deg,#69b45f,#4a9141);color:#11330d"},
			{Match: "consensus government", Style: "background:linear-gradient(90deg,#189491,#7f96ad);color:#1e3248"},
		},
		ProvinceGoverningParty: map[string]string{
			"AB":                        "United Conservative",
			"ALBERTA":                   "United Conservative",
			"BC":                        "New Democratic",
			"BRITISH COLUMBIA":          "New Democratic",
			"MB":                        "New Democratic",
			"MANITOBA":                  "New Democratic",
			"NB":                        "Progressive Conservative",
			"NEW BRUNSWICK":             "Progressive Conservative",
			"NL":                        "Liberal",
			"NEWFOUNDLAND AND LABRADOR": "Liberal",
			"NS":                        "Progressive Conservative",
			"NOVA SCOTIA":               "Progressive Conservative",
			"NT":                        "Consensus Government",
			"NORTHWEST TERRITORIES":     "Consensus Government",
			"NU":                        "Consensus Government",
			"NUNAVUT":                   "Consensus Government",
			"ON":                        "Progressive Conservative",
			"ONTARIO":                   "Progressive Conservative",
			"PE":                        "Progressive Conservative",
			"PRINCE EDWARD ISLAND":      "Progressive Conservative",
			"QC":                        "Coalition Avenir Quebec",
			"QUEBEC":                    "Coalition Avenir Quebec",
			"SK":                        "Saskatchewan Party",
			"SASKATCHEWAN":              "Saskatchewan Party",
			"YT":                        "Liberal",
			"YUKON":                     "Liberal",
		},
	}
}

func defaultFederalParty() string {
	return loadPartyTheme().FederalDefaultParty
}

func partyBannerStyle(party string) string {
	cfg := loadPartyTheme()
	low := strings.ToLower(party)
	for _, rule := range cfg.PartyStyleRules {
		if strings.Contains(low, strings.ToLower(rule.Match)) {
			return rule.Style
		}
	}
	return cfg.DefaultStyle
}

func provinceGoverningParty(province string) string {
	cfg := loadPartyTheme()
	key := strings.ToUpper(strings.TrimSpace(province))
	if p, ok := cfg.ProvinceGoverningParty[key]; ok {
		return p
	}
	return cfg.ProvinceFallbackParty
}

// StageEntry pairs a stage key with its human-readable label.
type StageEntry struct {
	Key   string
	Label string
}

// StageOrder defines the canonical bill-progress order shown in the progress bar.
var StageOrder = []StageEntry{
	{Key: "1st_reading", Label: "1st"},
	{Key: "2nd_reading", Label: "2nd"},
	{Key: "committee", Label: "Cmte"},
	{Key: "report_stage", Label: "Report"},
	{Key: "3rd_reading", Label: "3rd"},
	{Key: "senate", Label: "Senate"},
	{Key: "royal_assent", Label: "Assent"},
}

// Stages is the ordered list of stage keys for filter dropdowns.
var Stages = func() []string {
	keys := make([]string, len(StageOrder))
	for i, s := range StageOrder {
		keys[i] = s.Key
	}
	return keys
}()

// Categories is the list of known bill categories.
var Categories = []string{
	"Budget", "Criminal Justice", "Environment", "Health",
	"Housing", "Immigration", "Indigenous", "Infrastructure",
	"Justice", "Labour", "National Security", "Social Policy",
	"Trade", "Veterans",
}

// StageLabel returns a human-readable label for a stage key.
func StageLabel(key string) string {
	for _, s := range StageOrder {
		if s.Key == key {
			return s.Label + " Reading"
		}
	}
	// Fallback: replace underscores with spaces and capitalise each word.
	words := strings.Fields(strings.ReplaceAll(key, "_", " "))
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// StageIndexOf returns the 0-based index of a stage in StageOrder, or -1 if not found.
func StageIndexOf(key string) int {
	for i, s := range StageOrder {
		if s.Key == key {
			return i
		}
	}
	return -1
}

// FormatDate converts an ISO date string (2006-01-02) to a short readable form.
func FormatDate(d string) string {
	if d == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("Jan 2, 2006")
}

// ShortOrFullTitle returns the short title if set, otherwise the full title.
func ShortOrFullTitle(b store.BillRow) string {
	if b.ShortTitle != "" {
		return b.ShortTitle
	}
	return b.Title
}

// PartyClass returns a Tailwind text-color class for a party name.
func PartyClass(party string) string {
	switch {
	case strings.Contains(party, "Liberal"):
		return "text-red-600"
	case strings.Contains(party, "Conservative"):
		return "text-blue-700"
	case strings.Contains(party, "NDP"), strings.Contains(party, "New Democrat"):
		return "text-orange-600"
	case strings.Contains(party, "Bloc"):
		return "text-sky-600"
	case strings.Contains(party, "Green"):
		return "text-green-600"
	default:
		return "text-gray-600"
	}
}

// VoteBadgeClass returns a Tailwind text-color class for a vote value.
func VoteBadgeClass(vote string) string {
	switch vote {
	case "Yea":
		return "font-medium text-green-600"
	case "Nay":
		return "font-medium text-red-600"
	default:
		return "text-gray-500"
	}
}

// CategoryBadgeStyle returns an inline background-color style for a category badge.
func CategoryBadgeStyle(category string) string {
	colors := map[string]string{
		"Budget":            "background-color:#3b82f6",
		"Criminal Justice":  "background-color:#ef4444",
		"Environment":       "background-color:#22c55e",
		"Health":            "background-color:#ec4899",
		"Housing":           "background-color:#f97316",
		"Immigration":       "background-color:#8b5cf6",
		"Indigenous":        "background-color:#d97706",
		"Infrastructure":    "background-color:#6b7280",
		"Justice":           "background-color:#dc2626",
		"Labour":            "background-color:#0ea5e9",
		"National Security": "background-color:#1d4ed8",
		"Social Policy":     "background-color:#7c3aed",
		"Trade":             "background-color:#059669",
		"Veterans":          "background-color:#b45309",
	}
	if c, ok := colors[category]; ok {
		return c
	}
	return "background-color:#6b7280"
}

// PageInfo holds pagination state for rendering prev/next links.
type PageInfo struct {
	Current  int
	Total    int
	HasPrev  bool
	HasNext  bool
	PrevPage int
	NextPage int
}

// ordinal returns the ordinal suffix for a number (1st, 2nd, 3rd, 4th...).
func ordinal(n int) string {
	switch n % 100 {
	case 11, 12, 13:
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// initial returns the first character of s, or "?" if s is empty.
func initial(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return "?"
	}
	return string(runes[0])
}

// NewPageInfo computes PageInfo from the current page, total items, and page size.
func NewPageInfo(page, total, perPage int) PageInfo {
	if perPage <= 0 {
		perPage = 20
	}
	pages := (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	return PageInfo{
		Current:  page,
		Total:    pages,
		HasPrev:  page > 1,
		HasNext:  page < pages,
		PrevPage: page - 1,
		NextPage: page + 1,
	}
}

// ── Summary helpers ───────────────────────────────────────────────────────────

// ParsedSummary represents a parsed AI-generated bill summary.
type ParsedSummary struct {
	OneSentence           string   `json:"one_sentence"`
	PlainSummary          string   `json:"plain_summary"`
	KeyChanges            []string `json:"key_changes"`
	WhoIsAffected         []string `json:"who_is_affected"`
	NotableConsiderations []string `json:"notable_considerations"`
	EstimatedCost         string   `json:"estimated_cost"`
	Category              string   `json:"category"`
}

// ParseAISummary parses a JSON-encoded summary string. Returns zero value if parsing fails.
func ParseAISummary(summaryJSON string) ParsedSummary {
	if strings.TrimSpace(summaryJSON) == "" {
		return ParsedSummary{}
	}
	var result ParsedSummary
	_ = json.Unmarshal([]byte(summaryJSON), &result)
	return result
}

// truncate returns the first n characters of a string, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// HasSummary checks if a bill has either LoP or AI summary.
func HasSummary(b store.BillRow) bool {
	return strings.TrimSpace(b.SummaryLoP) != "" || strings.TrimSpace(b.SummaryAI) != ""
}

func ReactionPercent(count, total int) int {
	if total <= 0 {
		return 0
	}
	return (count * 100) / total
}

// safeExternalURL validates that rawURL has an http or https scheme and returns
// templ.SafeURL(rawURL). If the scheme is not http/https (e.g. "javascript:"),
// it returns templ.SafeURL("#") to prevent XSS via unsafe URL schemes.
func safeExternalURL(rawURL string) templ.SafeURL {
	if rawURL == "" {
		return templ.SafeURL("#")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return templ.SafeURL("#")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return templ.SafeURL("#")
	}
	return templ.SafeURL(rawURL)
}

// safeMailtoURL validates an email address and returns a templ.SafeURL for a
// mailto: link. Returns templ.SafeURL("#") when the email is empty, contains
// characters that could enable RFC 2822 header injection (e.g. newlines, query
// params introduced by '?' or '&'), or does not have the shape local@domain.tld.
func safeMailtoURL(email string) templ.SafeURL {
	if email == "" {
		return templ.SafeURL("#")
	}
	// Reject characters that could inject extra headers or malform the URI.
	// Intentionally stricter than RFC 5321: quoted local-parts with spaces
	// (e.g. "john doe"@example.com) are not common in practice and the
	// additional complexity is not worth the risk for external API input.
	if strings.ContainsAny(email, "\r\n\t ?&<>\"'\\") {
		return templ.SafeURL("#")
	}
	// Must have exactly one '@' with non-empty local and domain parts.
	at := strings.Index(email, "@")
	if at <= 0 || at >= len(email)-1 || strings.Contains(email[at+1:], "@") {
		return templ.SafeURL("#")
	}
	// Domain must contain at least one dot.
	if !strings.Contains(email[at+1:], ".") {
		return templ.SafeURL("#")
	}
	return templ.SafeURL("mailto:" + email)
}
