// Package server wires HTTP routes to the store and renders templ templates.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

// Server holds application dependencies.
type Server struct {
	store       *store.Store
	mux         *http.ServeMux
	baseURL     string
	emailer     verificationEmailSender
	rateLimiter *simpleRateLimiter
}

type verificationEmailSender interface {
	SendVerificationEmail(ctx context.Context, toEmail, verifyURL, code string) error
}

type sesVerificationSender struct {
	client    *sesv2.Client
	fromEmail string
	baseURL   string
}

func (s *sesVerificationSender) SendVerificationEmail(ctx context.Context, toEmail, verifyURL, code string) error {
	if s == nil || s.client == nil || s.fromEmail == "" {
		return fmt.Errorf("ses sender not configured")
	}
	subject := "Open Democracy verification code"
	bodyText := fmt.Sprintf("Use this code to verify your email: %s\n\nOr verify with this link: %s\n\nIf you did not request this, you can ignore this email.", code, verifyURL)
	bodyHTML := fmt.Sprintf("<p>Use this code to verify your email:</p><p><strong>%s</strong></p><p>Or verify with this link: <a href=\"%s\">Verify email</a></p><p>If you did not request this, you can ignore this email.</p>", code, verifyURL)
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.fromEmail),
		Destination: &types.Destination{
			ToAddresses: []string{toEmail},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: aws.String(subject), Charset: aws.String("UTF-8")},
				Body: &types.Body{
					Text: &types.Content{Data: aws.String(bodyText), Charset: aws.String("UTF-8")},
					Html: &types.Content{Data: aws.String(bodyHTML), Charset: aws.String("UTF-8")},
				},
			},
		},
	})
	return err
}

type rateRecord struct {
	windowStart time.Time
	count       int
}

type simpleRateLimiter struct {
	mu      sync.Mutex
	records map[string]rateRecord
}

func newSimpleRateLimiter() *simpleRateLimiter {
	return &simpleRateLimiter{records: map[string]rateRecord{}}
}

func (r *simpleRateLimiter) allow(key string, limit int, window time.Duration, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[key]
	if !ok || now.Sub(rec.windowStart) >= window {
		r.records[key] = rateRecord{windowStart: now, count: 1}
		return true
	}
	if rec.count >= limit {
		return false
	}
	rec.count++
	r.records[key] = rec
	return true
}

func (s *Server) clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	hostPort := strings.TrimSpace(r.RemoteAddr)
	if i := strings.LastIndex(hostPort, ":"); i > 0 {
		return hostPort[:i]
	}
	return hostPort
}

func (s *Server) rateLimitAllowed(key string, limit int, window time.Duration) bool {
	if s.rateLimiter == nil {
		return true
	}
	return s.rateLimiter.allow(key, limit, window, time.Now().UTC())
}

// New creates a Server and registers all routes.
func New(st *store.Store) *Server {
	baseURL := strings.TrimRight(os.Getenv("OAUTH_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	var emailer verificationEmailSender
	fromEmail := strings.TrimSpace(os.Getenv("SES_FROM_EMAIL"))
	if fromEmail != "" {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Printf("ses config load failed: %v", err)
		} else {
			emailer = &sesVerificationSender{client: sesv2.NewFromConfig(cfg), fromEmail: fromEmail, baseURL: baseURL}
		}
	}
	s := &Server{store: st, mux: http.NewServeMux(), baseURL: baseURL, emailer: emailer, rateLimiter: newSimpleRateLimiter()}
	s.mux.HandleFunc("GET /", s.handleHome)
	s.mux.HandleFunc("GET /bills", s.handleBills)
	s.mux.HandleFunc("GET /bills/{id}", s.handleBillDetail)
	s.mux.HandleFunc("GET /votes", s.handleVotes)
	s.mux.HandleFunc("GET /members", s.handleMembers)
	s.mux.HandleFunc("GET /members/{id}", s.handleMemberProfile)
	s.mux.HandleFunc("GET /compare", s.handleCompare)
	s.mux.HandleFunc("GET /riding", s.handleRiding)
	s.mux.HandleFunc("POST /api/follow", s.handleFollow)
	s.mux.HandleFunc("POST /api/react", s.handleReact)
	s.mux.HandleFunc("POST /api/log-submission", s.handleLogSubmission)
	s.mux.HandleFunc("POST /auth/request-verification", s.handleRequestVerification)
	s.mux.HandleFunc("POST /auth/verify", s.handleVerifyEmail)
	s.mux.HandleFunc("POST /auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /auth/me", s.handleWhoAmI)
	s.mux.HandleFunc("GET /auth/google/login", s.handleGoogleLogin)
	s.mux.HandleFunc("GET /auth/google/callback", s.handleGoogleCallback)
	s.mux.HandleFunc("GET /auth/facebook/login", s.handleFacebookLogin)
	s.mux.HandleFunc("GET /auth/facebook/callback", s.handleFacebookCallback)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) parliamentStatus() store.ParliamentStatus {
	ps, _ := s.store.GetParliamentStatus(scraper.CurrentParliament, scraper.CurrentSession)
	return ps
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ps := s.parliamentStatus()
	bills, _ := s.store.GetRecentBills(5)
	divs, _ := s.store.GetRecentDivisions(10)
	postal := strings.TrimSpace(r.URL.Query().Get("postal"))
	var federalRep store.MemberRow
	if postal != "" {
		members, _ := s.store.GetMembersByRiding(postal)
		for _, m := range members {
			if strings.EqualFold(m.Chamber, "commons") {
				federalRep = m
				break
			}
		}
		if federalRep.ID == "" && len(members) > 0 {
			federalRep = members[0]
		}
	}
	_ = templates.Home(ps, bills, divs, postal, federalRep).Render(r.Context(), w)
}

func (s *Server) handleBills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	f := store.BillFilter{
		Search:   q.Get("q"),
		Stage:    q.Get("stage"),
		Category: q.Get("category"),
		Chamber:  q.Get("chamber"),
		Page:     page,
		PerPage:  20,
	}
	ps := s.parliamentStatus()
	bills, total, err := s.store.ListBills(f)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = templates.BillsFeed(ps, bills, total, f).Render(r.Context(), w)
}

func (s *Server) handleBillDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps := s.parliamentStatus()
	bill, err := s.store.GetBill(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stages, _ := s.store.GetBillStages(id)
	divs, _ := s.store.GetBillDivisions(id)
	reactions, _ := s.store.GetBillReactionCounts(id)
	_ = templates.BillDetail(ps, bill, stages, divs, reactions).Render(r.Context(), w)
}

func (s *Server) handleVotes(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	ps := s.parliamentStatus()
	divs, total, err := s.store.ListDivisions(page, 50)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = templates.VotesFeed(ps, divs, total, page).Render(r.Context(), w)
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ps := s.parliamentStatus()
	members, err := s.store.ListMembers(q.Get("q"), q.Get("party"), q.Get("province"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = templates.MembersDirectory(ps, members, q.Get("q"), q.Get("party"), q.Get("province")).Render(r.Context(), w)
}

func (s *Server) handleMemberProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps := s.parliamentStatus()
	member, err := s.store.GetMember(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	votes, _ := s.store.GetMemberVotes(id, 50)
	stats, _ := s.store.GetMemberStats(id)
	_ = templates.MemberProfile(ps, member, votes, stats).Render(r.Context(), w)
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ps := s.parliamentStatus()
	var m1, m2 store.MemberRow
	var overlap, total int
	idA, idB := q.Get("a"), q.Get("b")
	if idA != "" {
		m1, _ = s.store.GetMember(idA)
	}
	if idB != "" {
		m2, _ = s.store.GetMember(idB)
	}
	if m1.ID != "" && m2.ID != "" {
		overlap, total, _ = s.store.CompareMemberVotes(m1.ID, m2.ID)
	}
	_ = templates.CompareMPs(ps, m1, m2, overlap, total).Render(r.Context(), w)
}

func (s *Server) handleRiding(w http.ResponseWriter, r *http.Request) {
	postal := r.URL.Query().Get("postal")
	ps := s.parliamentStatus()
	var members []store.MemberRow
	if postal != "" {
		members, _ = s.store.GetMembersByRiding(postal)
	}
	_ = templates.RidingLookup(ps, postal, members).Render(r.Context(), w)
}

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	postal := r.FormValue("postal_code")
	memberID := r.FormValue("member_id")
	if user, ok := s.sessionUser(r); ok {
		email = user.Email
		if postal == "" {
			postal = user.PostalCode
		}
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(memberID) == "" {
		http.Error(w, "email and member_id required", http.StatusBadRequest)
		return
	}
	if !s.requireVerifiedEmail(w, email, postal) {
		return
	}
	if err := s.store.FollowMember(email, postal, memberID); err != nil {
		http.Error(w, "failed to follow", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/members/"+memberID, http.StatusSeeOther)
}

func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	postal := r.FormValue("postal_code")
	billID := r.FormValue("bill_id")
	reaction := r.FormValue("reaction")
	note := r.FormValue("note")
	if user, ok := s.sessionUser(r); ok {
		email = user.Email
		if postal == "" {
			postal = user.PostalCode
		}
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(billID) == "" {
		http.Error(w, "email and bill_id required", http.StatusBadRequest)
		return
	}
	if !s.requireVerifiedEmail(w, email, postal) {
		return
	}
	if err := s.store.ReactToBill(email, postal, billID, reaction, note); err != nil {
		http.Error(w, "failed to save reaction", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/bills/"+billID, http.StatusSeeOther)
}

func (s *Server) handleLogSubmission(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email      string `json:"email"`
		PostalCode string `json:"postal_code"`
		MemberID   string `json:"member_id"`
		Subject    string `json:"subject"`
		Body       string `json:"body"`
		Category   string `json:"category"`
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		payload.Email = r.FormValue("email")
		payload.PostalCode = r.FormValue("postal_code")
		payload.MemberID = r.FormValue("member_id")
		payload.Subject = r.FormValue("subject")
		payload.Body = r.FormValue("body")
		payload.Category = r.FormValue("category")
	}

	if strings.TrimSpace(payload.Email) == "" || strings.TrimSpace(payload.MemberID) == "" {
		if user, ok := s.sessionUser(r); ok {
			payload.Email = user.Email
			if payload.PostalCode == "" {
				payload.PostalCode = user.PostalCode
			}
		}
	}

	if strings.TrimSpace(payload.Email) == "" || strings.TrimSpace(payload.MemberID) == "" {
		http.Error(w, "email and member_id required", http.StatusBadRequest)
		return
	}
	if !s.requireVerifiedEmail(w, payload.Email, payload.PostalCode) {
		return
	}
	if err := s.store.LogPolicySubmission(
		payload.Email,
		payload.PostalCode,
		payload.MemberID,
		payload.Subject,
		payload.Body,
		payload.Category,
	); err != nil {
		http.Error(w, "failed to log submission", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) sessionUser(r *http.Request) (store.UserRow, bool) {
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

func (s *Server) requireVerifiedEmail(w http.ResponseWriter, email, postalCode string) bool {
	u, err := s.store.GetUserByEmail(email)
	if err == nil && u.EmailVerified {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"email_verification_required","message":"request a verification code via /auth/request-verification"}`))
	return false
}

func (s *Server) setSessionCookie(w http.ResponseWriter, userID string) error {
	sessionID, err := s.store.CreateSession(userID, 30*24*time.Hour)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "od_session",
		Value:    sessionID,
		Path:     "/",
		Secure:   strings.HasPrefix(strings.ToLower(s.baseURL), "https://"),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
	return nil
}

func (s *Server) handleRequestVerification(w http.ResponseWriter, r *http.Request) {
	if !s.rateLimitAllowed("auth:request-verification:ip:"+s.clientIP(r), 10, time.Minute) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	postal := strings.TrimSpace(r.FormValue("postal_code"))
	if email == "" {
		if u, ok := s.sessionUser(r); ok {
			email = u.Email
			if postal == "" {
				postal = u.PostalCode
			}
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
	_, code, err := s.store.CreateEmailVerification(email, postal, 30*time.Minute)
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

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	u, ok := s.sessionUser(r)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("od_session"); err == nil {
		_ = s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "od_session", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: strings.HasPrefix(strings.ToLower(s.baseURL), "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func randomOAuthState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) startOAuth(w http.ResponseWriter, r *http.Request, provider, authURL, clientID, redirectURI, scope string) {
	if clientID == "" {
		http.Error(w, provider+" oauth not configured", http.StatusNotImplemented)
		return
	}
	state, err := randomOAuthState()
	if err != nil {
		http.Error(w, "failed to initialize oauth", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: state, Path: "/", Secure: strings.HasPrefix(strings.ToLower(s.baseURL), "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(10 * time.Minute)})
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

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(
		w, r,
		"google",
		"https://accounts.google.com/o/oauth2/v2/auth",
		os.Getenv("GOOGLE_CLIENT_ID"),
		s.baseURL+"/auth/google/callback",
		"openid email profile",
	)
}

func (s *Server) handleFacebookLogin(w http.ResponseWriter, r *http.Request) {
	s.startOAuth(
		w, r,
		"facebook",
		"https://www.facebook.com/v19.0/dialog/oauth",
		os.Getenv("FACEBOOK_CLIENT_ID"),
		s.baseURL+"/auth/facebook/callback",
		"email public_profile",
	)
}

func (s *Server) readOAuthState(r *http.Request) bool {
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

func exchangeCode(tokenURL string, params url.Values) ([]byte, error) {
	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", bytes.NewBufferString(params.Encode()))
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

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.readOAuthState(r) {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: strings.HasPrefix(strings.ToLower(s.baseURL), "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	code := r.URL.Query().Get("code")
	params := url.Values{}
	params.Set("code", code)
	params.Set("client_id", os.Getenv("GOOGLE_CLIENT_ID"))
	params.Set("client_secret", os.Getenv("GOOGLE_CLIENT_SECRET"))
	params.Set("redirect_uri", s.baseURL+"/auth/google/callback")
	params.Set("grant_type", "authorization_code")
	b, err := exchangeCode("https://oauth2.googleapis.com/token", params)
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
	resp, err := http.DefaultClient.Do(req)
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

func (s *Server) handleFacebookCallback(w http.ResponseWriter, r *http.Request) {
	if !s.readOAuthState(r) {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "od_oauth_state", Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1, Secure: strings.HasPrefix(strings.ToLower(s.baseURL), "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	code := r.URL.Query().Get("code")
	params := url.Values{}
	params.Set("client_id", os.Getenv("FACEBOOK_CLIENT_ID"))
	params.Set("client_secret", os.Getenv("FACEBOOK_CLIENT_SECRET"))
	params.Set("redirect_uri", s.baseURL+"/auth/facebook/callback")
	params.Set("code", code)
	b, err := exchangeCode("https://graph.facebook.com/v19.0/oauth/access_token", params)
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
	uinfoResp, err := http.Get("https://graph.facebook.com/me?fields=id,name,email&access_token=" + url.QueryEscape(tok.AccessToken))
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
