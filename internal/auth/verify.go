package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/philspins/open-democracy/internal/store"
)

func (s *Service) HandleRequestVerification(w http.ResponseWriter, r *http.Request) {
	if !s.rateLimitAllowed("auth:request-verification:ip:"+s.clientIP(r), 10, time.Minute) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		if u, ok := s.SessionUser(r); ok {
			email = u.Email
		}
	}
	if email == "" {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}
	if !s.rateLimitAllowed("auth:request-verification:email:"+strings.ToLower(email), 3, 10*time.Minute) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	_, code, err := s.store.CreateEmailVerification(email, 30*time.Minute)
	if err == nil && s.emailer != nil {
		verifyURL := s.baseURL + "/auth/verify"
		if sendErr := s.emailer.SendVerificationEmail(r.Context(), email, verifyURL, code); sendErr != nil {
			log.Printf("verification email send failed for %s: %v", email, sendErr)
		} else {
			log.Printf("verification email sent to %s", email)
		}
	} else if err == nil {
		log.Printf("verification requested for %s but SES is not configured", email)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Service) HandleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.rateLimitAllowed("auth:verify:ip:"+s.clientIP(r), 20, time.Minute) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	var token, email, code string
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var payload struct {
			Token string `json:"token"`
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		token = strings.TrimSpace(payload.Token)
		email = strings.TrimSpace(payload.Email)
		code = strings.TrimSpace(payload.Code)
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		token = strings.TrimSpace(r.FormValue("token"))
		email = strings.TrimSpace(r.FormValue("email"))
		code = strings.TrimSpace(r.FormValue("code"))
	}

	var (
		u   store.UserRow
		err error
	)
	if token != "" {
		u, err = s.store.VerifyEmailToken(token)
	} else {
		if email != "" && !s.rateLimitAllowed("auth:verify:email:"+strings.ToLower(email), 8, 10*time.Minute) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		u, err = s.store.VerifyEmailCode(email, code)
	}
	if err != nil {
		http.Error(w, "invalid verification credentials", http.StatusBadRequest)
		return
	}
	if err := s.setSessionCookie(w, u.ID); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
