package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/philspins/open-democracy/internal/db"
	"github.com/philspins/open-democracy/internal/store"
)

func TestHandleSignupPage_RendersOAuthWidgetsAndFallbacks(t *testing.T) {
	svc, _ := newTestService(t)
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("FACEBOOK_CLIENT_ID", "fb-client")

	req := httptest.NewRequest(http.MethodGet, "/auth/signup", nil)
	rr := httptest.NewRecorder()

	svc.HandleSignupPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Create Your Account") {
		t.Fatalf("expected signup heading in page body")
	}
	if !strings.Contains(body, "google-signin-widget") {
		t.Fatalf("expected google widget container")
	}
	if !strings.Contains(body, "fb:login-button") {
		t.Fatalf("expected facebook widget tag")
	}
	if !strings.Contains(body, "/auth/google/login") || !strings.Contains(body, "/auth/facebook/login") {
		t.Fatalf("expected oauth fallback links")
	}
}

func TestHandleLoginPage_RendersLoginVariant(t *testing.T) {
	svc, _ := newTestService(t)
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("FACEBOOK_CLIENT_ID", "fb-client")

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rr := httptest.NewRecorder()

	svc.HandleLoginPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Welcome Back") {
		t.Fatalf("expected login heading in page body")
	}
	if !strings.Contains(body, "signin_with") {
		t.Fatalf("expected login-specific google widget text")
	}
}

func TestHandleSignupPage_AuthenticatedUserRedirectsHome(t *testing.T) {
	svc, st := newTestService(t)
	u, err := st.UpsertUser("signed-in-signup@example.com", "")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	sessionID, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/signup", nil)
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sessionID})
	rr := httptest.NewRecorder()

	svc.HandleSignupPage(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	if rr.Header().Get("Location") != "/" {
		t.Fatalf("location=%q want /", rr.Header().Get("Location"))
	}
}

func TestHandleLoginPage_AuthenticatedUserRedirectsHome(t *testing.T) {
	svc, st := newTestService(t)
	u, err := st.UpsertUser("signed-in-login@example.com", "")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	sessionID, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sessionID})
	rr := httptest.NewRecorder()

	svc.HandleLoginPage(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	if rr.Header().Get("Location") != "/" {
		t.Fatalf("location=%q want /", rr.Header().Get("Location"))
	}
}

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	t.Setenv("SES_FROM_EMAIL", "")
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st := store.New(conn)
	svc := New(st, "http://127.0.0.1:8080")
	return svc, st
}

func TestHandleGoogleLogin_SetsStateCookieAndRedirect(t *testing.T) {
	svc, _ := newTestService(t)
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")

	req := httptest.NewRequest(http.MethodGet, "/auth/google/login", nil)
	rr := httptest.NewRecorder()

	svc.HandleGoogleLogin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "accounts.google.com") {
		t.Fatalf("unexpected redirect location: %s", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state in redirect")
	}

	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "od_oauth_state" && c.Value == state {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected od_oauth_state cookie matching redirect state")
	}
}

func TestHandleLogout_DeletesSessionAndClearsCookie(t *testing.T) {
	svc, st := newTestService(t)
	u, err := st.UpsertUser("logout@example.com", "")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	sessionID, err := st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "od_session", Value: sessionID})
	rr := httptest.NewRecorder()

	svc.HandleLogout(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	_, err = st.GetUserBySession(sessionID)
	if err == nil {
		t.Fatalf("expected session to be deleted")
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "od_session" && c.MaxAge == -1 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatalf("expected od_session clearing cookie")
	}
}

func TestHandleVerifyEmail_ByCode_SetsSession(t *testing.T) {
	svc, st := newTestService(t)
	email := "verify-code-auth@example.com"
	_, code, err := st.CreateEmailVerification(email, "", time.Hour)
	if err != nil {
		t.Fatalf("CreateEmailVerification: %v", err)
	}

	form := url.Values{}
	form.Set("email", email)
	form.Set("code", code)
	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	svc.HandleVerifyEmail(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusSeeOther)
	}
	found := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "od_session" && c.Value != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected od_session cookie to be set")
	}
}
