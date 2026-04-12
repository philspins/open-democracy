package server

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

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("SES_FROM_EMAIL", "")
	t.Setenv("OAUTH_BASE_URL", "http://127.0.0.1:8080")

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
	token, _, err := st.CreateEmailVerification(email, "", time.Hour)
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
