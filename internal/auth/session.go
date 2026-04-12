package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/philspins/open-democracy/internal/store"
)

func (s *Service) SessionUser(r *http.Request) (store.UserRow, bool) {
	c, err := r.Cookie("od_session")
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return store.UserRow{}, false
	}
	u, err := s.store.GetUserBySession(c.Value)
	if err != nil {
		return store.UserRow{}, false
	}
	return u, true
}

func (s *Service) RequireVerifiedEmail(w http.ResponseWriter, email, postalCode string) bool {
	u, err := s.store.GetUserByEmail(email)
	if err == nil && u.EmailVerified {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"email_verification_required","message":"request a verification code via /auth/request-verification"}`))
	return false
}

func (s *Service) setSessionCookie(w http.ResponseWriter, userID string) error {
	sessionID, err := s.store.CreateSession(userID, 30*24*time.Hour)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "od_session",
		Value:    sessionID,
		Path:     "/",
		Secure:   s.isSecureCookie(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
	return nil
}

func (s *Service) HandleWhoAmI(w http.ResponseWriter, r *http.Request) {
	u, ok := s.SessionUser(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("od_session"); err == nil {
		_ = s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "od_session", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: s.isSecureCookie(), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
