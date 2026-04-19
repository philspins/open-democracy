package scraper

import "testing"

func TestExtractLegislatureSessionCandidates_AlbertaFormats(t *testing.T) {
	tests := []struct {
		text string
		want legislatureSession
	}{
		{
			text: "Legislature, Session 31-2 (2025-2026)",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
		{
			text: "Legislature 31, Session 2 (2025-2026)",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
		{
			text: "https://www.assembly.ab.ca/assembly-business/assembly-dashboard?legl=31&session=2&sectionb=d&btn=i#page-menu",
			want: legislatureSession{Legislature: 31, Session: 2},
		},
	}

	for _, tc := range tests {
		candidates := extractLegislatureSessionCandidates("ab", tc.text, 50)
		best, ok := maxLegislatureSession(candidates)
		if !ok {
			t.Fatalf("no candidates for %q", tc.text)
		}
		if best.Legislature != tc.want.Legislature || best.Session != tc.want.Session {
			t.Fatalf("best=%+v, want legislature=%d session=%d", best, tc.want.Legislature, tc.want.Session)
		}
	}
}