package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("SES_FROM_EMAIL", "")
	t.Setenv("OAUTH_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("GOOGLE_MAPS_API_KEY", "test-maps-key")

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	st := store.New(conn)
	srv := New(st)
	return srv, st
}

func TestHandleRequestVerification_DoesNotLeakSecrets(t *testing.T) {
	srv, _ := newTestServer(t)

	form := url.Values{}
	form.Set("email", "verify1@example.com")
	req := httptest.NewRequest(http.MethodPost, "/auth/request-verification", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if strings.Contains(body, "token") || strings.Contains(body, "code") || strings.Contains(body, "verify_url") {
		t.Fatalf("response leaked secrets: %s", body)
	}
	if strings.TrimSpace(body) != `{"ok":true}` {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestHandleVerifyEmail_ByCode_SetsSessionAndVerifies(t *testing.T) {
	srv, st := newTestServer(t)

	email := "verify-code@example.com"
	_, code, err := st.CreateEmailVerification(email, time.Hour)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	form := url.Values{}
	form.Set("email", email)
	form.Set("code", code)
	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "od_session" && c.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected od_session cookie to be set")
	}

	u, err := st.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !u.EmailVerified {
		t.Fatalf("expected user to be email-verified")
	}
}

func TestHandleVerifyEmail_ByToken_SetsSessionAndVerifies(t *testing.T) {
	srv, st := newTestServer(t)

	email := "verify-token@example.com"
	token, _, err := st.CreateEmailVerification(email, time.Hour)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	form := url.Values{}
	form.Set("token", token)
	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	u, err := st.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !u.EmailVerified {
		t.Fatalf("expected user to be email-verified")
	}
}

func TestHandleVerifyEmail_InvalidCredentials(t *testing.T) {
	srv, _ := newTestServer(t)

	form := url.Values{}
	form.Set("email", "nobody@example.com")
	form.Set("code", "000000")
	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleRequestVerification_RateLimitedByEmail(t *testing.T) {
	srv, _ := newTestServer(t)

	email := "rate@example.com"
	for i := 0; i < 3; i++ {
		form := url.Values{}
		form.Set("email", email)
		req := httptest.NewRequest(http.MethodPost, "/auth/request-verification", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status=%d want %d", i+1, rr.Code, http.StatusOK)
		}
	}

	form := url.Values{}
	form.Set("email", email)
	req := httptest.NewRequest(http.MethodPost, "/auth/request-verification", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusTooManyRequests)
	}
}

func TestSecurityHeaders_AreSetOnResponses(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing X-Content-Type-Options header")
	}
	if rr.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing X-Frame-Options header")
	}
	if rr.Header().Get("Referrer-Policy") == "" {
		t.Fatalf("missing Referrer-Policy header")
	}
	if rr.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("missing Content-Security-Policy header")
	}
}

func TestHealthz_ReturnsOKJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("unexpected content-type: %s", got)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected ok=true response, got: %v", resp)
	}
}

func TestHandleFollow_RequiresAuthenticatedSession(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{}
	form.Set("member_id", "m1")
	req := httptest.NewRequest(http.MethodPost, "/api/follow", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(rr.Body.String(), "authentication_required") {
		t.Fatalf("expected authentication_required error body, got: %s", rr.Body.String())
	}
}

func TestLegalPages_Render(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, path := range []string{"/privacy", "/tos", "/delete-data"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d want %d", path, rr.Code, http.StatusOK)
		}
	}
}

func TestDeleteDataCallback_ValidSignedRequest(t *testing.T) {
	srv, _ := newTestServer(t)
	t.Setenv("FACEBOOK_CLIENT_ID", "123456789")
	t.Setenv("FACEBOOK_CLIENT_SECRET", "test-app-secret")
	t.Setenv("OAUTH_BASE_URL", "https://open-democracy.ca")

	signed := buildSignedRequest(t, "test-app-secret", map[string]any{
		"algorithm": "HMAC-SHA256",
		"app_id":    "123456789",
		"user_id":   "meta-user-1",
	})

	form := url.Values{}
	form.Set("signed_request", signed)
	req := httptest.NewRequest(http.MethodPost, "/delete-data", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp["confirmation_code"] == "" {
		t.Fatalf("expected confirmation_code in response")
	}
	if !strings.HasPrefix(resp["url"], "https://open-democracy.ca/delete-data?confirmation_code=") {
		t.Fatalf("unexpected status url: %s", resp["url"])
	}
}

func TestDeleteDataCallback_RejectsInvalidRequest(t *testing.T) {
	srv, _ := newTestServer(t)
	t.Setenv("FACEBOOK_CLIENT_SECRET", "test-app-secret")

	form := url.Values{}
	form.Set("signed_request", "invalid")
	req := httptest.NewRequest(http.MethodPost, "/delete-data", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rr.Body.String(), "invalid_signed_request") {
		t.Fatalf("expected invalid_signed_request error body, got: %s", rr.Body.String())
	}
}

func TestHandleRiding_PersistsLookupForSessionUser(t *testing.T) {
	srv, st := newTestServer(t)
	u, err := st.UpsertUser("lookup@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	sid, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srv.riding.SetLookups(
		func(_ context.Context, _ string, _ string) (float64, float64, error) {
			return 45.0, -75.0, nil
		},
		func(_ context.Context, _, _ float64) ([]opennorth.Representative, error) {
			return []opennorth.Representative{
				{Name: "Jane MP", ElectedOffice: "MP", DistrictName: "Ottawa Centre"},
				{Name: "John MPP", ElectedOffice: "MPP", DistrictName: "Ottawa South"},
			}, nil
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/riding?address=123+Main+St,+Ottawa,+ON", nil)
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sid})
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}

	got, err := st.GetUserByEmail("lookup@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.Address != "123 Main St, Ottawa, ON" {
		t.Fatalf("Address=%q want %q", got.Address, "123 Main St, Ottawa, ON")
	}
	if got.FederalRidingID != "Ottawa Centre" || got.ProvincialRidingID != "Ottawa South" {
		t.Fatalf("unexpected riding ids: %+v", got)
	}
}

func TestHandleProfile_PostSavesAddress(t *testing.T) {
	srv, st := newTestServer(t)
	u, err := st.UpsertUser("profile-save@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	sid, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srv.riding.SetLookups(
		func(_ context.Context, _ string, _ string) (float64, float64, error) {
			return 45.0, -75.0, nil
		},
		func(_ context.Context, _, _ float64) ([]opennorth.Representative, error) {
			return []opennorth.Representative{
				{Name: "Jane MP", ElectedOffice: "MP", DistrictName: "Ottawa Centre"},
				{Name: "John MPP", ElectedOffice: "MPP", DistrictName: "Ottawa South"},
			}, nil
		},
	)

	form := url.Values{}
	form.Set("address", "456 Elm St, Ottawa, ON")
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sid})
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	if rr.Header().Get("Location") != "/profile?updated=1" {
		t.Fatalf("unexpected redirect location: %s", rr.Header().Get("Location"))
	}

	got, err := st.GetUserByEmail("profile-save@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.Address != "456 Elm St, Ottawa, ON" {
		t.Fatalf("Address=%q want %q", got.Address, "456 Elm St, Ottawa, ON")
	}
}

func TestHandleHome_UsesSavedRepresentativesAndHidesLookupHero(t *testing.T) {
	srv, st := newTestServer(t)
	u, err := st.UpsertUser("home@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	_, err = st.UpdateUserLocation(u.ID, "789 Pine St, Ottawa, ON", "Ottawa Centre", "Ottawa South")
	if err != nil {
		t.Fatalf("UpdateUserLocation: %v", err)
	}
	sid, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srv.riding.SetLookups(
		func(_ context.Context, _ string, _ string) (float64, float64, error) {
			return 45.0, -75.0, nil
		},
		func(_ context.Context, _, _ float64) ([]opennorth.Representative, error) {
			return []opennorth.Representative{
				{Name: "Jane MP", ElectedOffice: "MP", DistrictName: "Ottawa Centre", PartyName: "Liberal"},
				{Name: "John MPP", ElectedOffice: "MPP", DistrictName: "Ottawa South", PartyName: "Progressive Conservative"},
			}, nil
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sid})
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Jane MP") || !strings.Contains(body, "John MPP") {
		t.Fatalf("expected saved representative names in home page body")
	}
	if strings.Contains(body, "Find Your Riding") {
		t.Fatalf("expected lookup hero to be hidden once address is saved")
	}
}

func buildSignedRequest(t *testing.T, secret string, payload map[string]any) string {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal payload: %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payloadPart))
	sigPart := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sigPart + "." + payloadPart
}
