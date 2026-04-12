package auth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"time"
)

func (s *Service) HandleFacebookLogin(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(
		w, r,
		"facebook",
		"https://www.facebook.com/v19.0/dialog/oauth",
		os.Getenv("FACEBOOK_CLIENT_ID"),
		s.baseURL+"/auth/facebook/callback",
		"email public_profile",
	)
}

func (s *Service) HandleFacebookCallback(w http.ResponseWriter, r *http.Request) {
	if !s.readOAuthState(r) {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: s.isSecureCookie(), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	code := r.URL.Query().Get("code")
	params := url.Values{}
	params.Set("client_id", os.Getenv("FACEBOOK_CLIENT_ID"))
	params.Set("client_secret", os.Getenv("FACEBOOK_CLIENT_SECRET"))
	params.Set("redirect_uri", s.baseURL+"/auth/facebook/callback")
	params.Set("code", code)
	b, err := exchangeCode(s.httpClient, "https://graph.facebook.com/v19.0/oauth/access_token", params)
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
	uinfoResp, err := s.httpClient.Get("https://graph.facebook.com/me?fields=id,name,email&access_token=" + url.QueryEscape(tok.AccessToken))
	if err != nil {
		http.Error(w, "failed userinfo", http.StatusBadGateway)
		return
	}
	defer uinfoResp.Body.Close()
	var uinfo struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(uinfoResp.Body).Decode(&uinfo); err != nil || uinfo.ID == "" || uinfo.Email == "" {
		http.Error(w, "invalid userinfo (facebook email may be unavailable)", http.StatusBadGateway)
		return
	}
	u, err := s.store.AuthenticateOAuth("facebook", uinfo.ID, uinfo.Email, "", false)
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
