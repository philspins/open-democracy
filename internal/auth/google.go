package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"time"
)

func (s *Service) HandleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(
		w, r,
		"google",
		"https://accounts.google.com/o/oauth2/v2/auth",
		os.Getenv("GOOGLE_CLIENT_ID"),
		s.baseURL+"/auth/google/callback",
		"openid email profile",
	)
}

func (s *Service) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.readOAuthState(r) {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: s.isSecureCookie(), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	code := r.URL.Query().Get("code")
	params := url.Values{}
	params.Set("code", code)
	params.Set("client_id", os.Getenv("GOOGLE_CLIENT_ID"))
	params.Set("client_secret", os.Getenv("GOOGLE_CLIENT_SECRET"))
	params.Set("redirect_uri", s.baseURL+"/auth/google/callback")
	params.Set("grant_type", "authorization_code")
	b, err := exchangeCode(s.httpClient, "https://oauth2.googleapis.com/token", params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &tok); err != nil || tok.AccessToken == "" {
		http.Error(w, "invalid oauth token response", http.StatusBadRequest)
		return
	}
	req, _ := http.NewRequest(http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, "failed userinfo", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var uinfo struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uinfo); err != nil || uinfo.Sub == "" || uinfo.Email == "" || !uinfo.EmailVerified {
		http.Error(w, "invalid userinfo", http.StatusBadGateway)
		return
	}
	u, err := s.store.AuthenticateOAuth("google", uinfo.Sub, uinfo.Email, "", true)
	if err != nil {
		http.Error(w, "failed oauth login", http.StatusInternalServerError)
		return
	}
	if err := s.setSessionCookie(w, u.ID); err != nil {
		http.Error(w, "failed session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
