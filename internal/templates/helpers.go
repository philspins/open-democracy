// Package templates provides templ components and helper functions for CivicTracker.
package templates

import (
	"strings"
	"time"

	"github.com/philspins/open-democracy/internal/store"
)

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
		"Budget":          "background-color:#3b82f6",
		"Criminal Justice": "background-color:#ef4444",
		"Environment":     "background-color:#22c55e",
		"Health":          "background-color:#ec4899",
		"Housing":         "background-color:#f97316",
		"Immigration":     "background-color:#8b5cf6",
		"Indigenous":      "background-color:#d97706",
		"Infrastructure":  "background-color:#6b7280",
		"Justice":         "background-color:#dc2626",
		"Labour":          "background-color:#0ea5e9",
		"National Security": "background-color:#1d4ed8",
		"Social Policy":   "background-color:#7c3aed",
		"Trade":           "background-color:#059669",
		"Veterans":        "background-color:#b45309",
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
