package riding

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	odb "github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/store"
)

func newTestRidingService(t *testing.T, apiKey string) (*Service, *store.Store, *sql.DB) {
	t.Helper()
	conn, err := odb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st := store.New(conn)
	svc := New(st, apiKey)
	return svc, st, conn
}

func TestHandleLookup_MissingAPIKeyShowsConfiguredMessage(t *testing.T) {
	svc, _, _ := newTestRidingService(t, "")

	req := httptest.NewRequest(http.MethodGet, "/riding?address=123+Main+St", nil)
	rr := httptest.NewRecorder()
	svc.HandleLookup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "missing GOOGLE_MAPS_API_KEY") {
		t.Fatalf("expected missing api key message in response body")
	}
}

func TestHandleLookup_MatchesLocalMemberID(t *testing.T) {
	svc, _, conn := newTestRidingService(t, "fake-key")

	err := odb.UpsertMember(conn, odb.Member{
		ID:          "mp-1",
		Name:        "Jane Doe",
		Riding:      "Test District",
		Chamber:     "commons",
		Active:      true,
		LastScraped: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	svc.SetLookups(
		func(ctx context.Context, address, apiKey string) (float64, float64, error) {
			return 45.0, -75.0, nil
		},
		func(ctx context.Context, lat, lng float64) ([]opennorth.Representative, error) {
			return []opennorth.Representative{{
				Name:          "Jane Doe",
				ElectedOffice: "MP",
				DistrictName:  "Test District",
			}}, nil
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/riding?address=123+Main+St", nil)
	rr := httptest.NewRecorder()
	svc.HandleLookup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "/members/mp-1") {
		t.Fatalf("expected riding page to link matched local member profile")
	}
}
