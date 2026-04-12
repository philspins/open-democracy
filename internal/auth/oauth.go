package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func randomOAuthState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) startOAuth(w http.ResponseWriter, r *http.Request, provider, authURL, clientID, redirectURI, scope string) {
	if clientID == "" {
		http.Error(w, provider+" oauth not configured", http.StatusNotImplemented)
		return
	}
	state, err := randomOAuthState()
	if err != nil {
		http.Error(w, "failed to initialize oauth", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: state, Path: "/", Secure: s.isSecureCookie(), HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(10 * time.Minute)})
	u, _ := url.Parse(authURL)
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scope)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Service) readOAuthState(r *http.Request) bool {
	state := r.URL.Query().Get("state")
	c, err := r.Cookie("od_oauth_state")
	if err != nil || c.Value == "" || state == "" {
		return false
	}
	if len(c.Value) != len(state) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) == 1
}

func exchangeCode(client *http.Client, tokenURL string, params url.Values) ([]byte, error) {
	resp, err := client.Post(tokenURL, "application/x-www-form-urlencoded", bytes.NewBufferString(params.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("oauth token exchange failed: %s", string(b))
	}
	return io.ReadAll(resp.Body)
}
