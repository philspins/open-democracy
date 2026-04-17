// Package server wires HTTP routes to the store and renders templ templates.
package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/philspins/open-democracy/internal/auth"
	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/riding"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

// Server holds application dependencies.
type Server struct {
	store   *store.Store
	mux     *http.ServeMux
	auth    *auth.Service
	riding  *riding.Service
	baseURL string
	// baseHost is the hostname extracted from baseURL at construction time.
	// It is used for the HTTP→HTTPS redirect to prevent host-header injection.
	// Empty when baseURL cannot be parsed or contains no host.
	baseHost   string
	trustProxy bool
}

// New creates a Server and registers all routes.
func New(st *store.Store) *Server {
	baseURL := strings.TrimRight(os.Getenv("OAUTH_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	googleMapsKey := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY"))

	parsed, err := url.Parse(baseURL)
	baseHost := ""
	if err != nil {
		log.Printf("warning: could not parse OAUTH_BASE_URL %q: %v; HTTP→HTTPS redirect disabled", baseURL, err)
	} else if parsed != nil {
		baseHost = parsed.Host
	}

	s := &Server{
		store:      st,
		mux:        http.NewServeMux(),
		auth:       auth.New(st, baseURL),
		riding:     riding.New(st, googleMapsKey),
		baseURL:    baseURL,
		baseHost:   baseHost,
		trustProxy: strings.ToLower(strings.TrimSpace(os.Getenv("TRUST_PROXY"))) == "true",
	}

	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /", s.handleHome)
	s.mux.HandleFunc("GET /bills", s.handleBills)
	s.mux.HandleFunc("GET /bills/{id}", s.handleBillDetail)
	s.mux.HandleFunc("GET /votes", s.handleVotes)
	s.mux.HandleFunc("GET /members", s.handleMembers)
	s.mux.HandleFunc("GET /members/{id}", s.handleMemberProfile)
	s.mux.HandleFunc("GET /compare", s.handleCompare)
	s.mux.HandleFunc("GET /profile", s.handleProfile)
	s.mux.HandleFunc("POST /profile", s.handleProfile)
	s.mux.HandleFunc("GET /privacy", s.handlePrivacy)
	s.mux.HandleFunc("GET /tos", s.handleTerms)
	s.mux.HandleFunc("GET /delete-data", s.handleDeleteDataPage)
	s.mux.HandleFunc("POST /delete-data", s.handleDeleteDataCallback)
	s.mux.HandleFunc("GET /riding", s.handleRiding)
	s.mux.HandleFunc("POST /api/follow", s.handleFollow)
	s.mux.HandleFunc("POST /api/react", s.handleReact)
	s.mux.HandleFunc("POST /api/log-submission", s.handleLogSubmission)
	s.auth.RegisterRoutes(s.mux)

	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// When running behind a trusted reverse proxy in HTTPS mode, redirect
	// plain-HTTP requests to HTTPS. The /health path is exempt so that ALB
	// health checks always succeed regardless of protocol.
	// s.baseHost is derived from OAUTH_BASE_URL at startup; if it is empty the
	// redirect is skipped to avoid an open-redirect via a spoofed Host header.
	if s.trustProxy && s.baseHost != "" && strings.HasPrefix(s.baseURL, "https://") &&
		strings.TrimSuffix(r.URL.Path, "/") != "/health" {
		if strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) == "http" {
			http.Redirect(w, r, "https://"+s.baseHost+r.RequestURI, http.StatusMovedPermanently)
			return
		}
	}
	s.applySecurityHeaders(w, r)
	s.mux.ServeHTTP(w, r)
}

func (s *Server) applySecurityHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"img-src 'self' data: https:",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com https://accounts.google.com https://connect.facebook.net https://maps.googleapis.com https://maps.gstatic.com",
		"connect-src 'self' https://graph.facebook.com https://www.googleapis.com https://oauth2.googleapis.com https://maps.googleapis.com",
	}, "; "))

	isHTTPS := r.TLS != nil
	if !isHTTPS && s.trustProxy {
		isHTTPS = strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) == "https"
	}
	if isHTTPS {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}

func (s *Server) parliamentStatus() store.ParliamentStatus {
	ps, _ := s.store.GetParliamentStatus(scraper.CurrentParliament, scraper.CurrentSession)
	return ps
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ps := s.parliamentStatus()
	bills, _ := s.store.GetRecentBills(5)
	divs, _ := s.store.GetRecentDivisions(10)
	var (
		savedAddress  string
		federalRep    opennorth.Representative
		provincialRep opennorth.Representative
	)
	if user, ok := s.auth.SessionUser(r); ok {
		savedAddr, result := s.loadRepresentativeContext(r, user)
		savedAddress = savedAddr
		federalRep = result.FederalRepresentative
		provincialRep = result.ProvincialRepresentative
	}
	_ = templates.Home(ps, bills, divs, savedAddress, federalRep, provincialRep).Render(r.Context(), w)
}

func (s *Server) handleBills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	f := store.BillFilter{
		Search:   q.Get("q"),
		Stage:    q.Get("stage"),
		Category: q.Get("category"),
		Chamber:  q.Get("chamber"),
		Level:    q.Get("level"),
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
	members, err := s.store.ListMembers(q.Get("q"), q.Get("party"), q.Get("province"), q.Get("riding"), q.Get("level"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	parties, _ := s.store.ListDistinctParties()
	provinces, _ := s.store.ListDistinctProvinces()
	ridings, _ := s.store.ListDistinctRidings()
	_ = templates.MembersDirectory(ps, members, q.Get("q"), q.Get("party"), q.Get("province"), q.Get("riding"), q.Get("level"), parties, provinces, ridings).Render(r.Context(), w)
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
	catScores, _ := s.store.GetMemberCategoryScores(id)
	_ = templates.MemberProfile(ps, member, votes, stats, catScores).Render(r.Context(), w)
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

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	u, ok := s.auth.RequireVerifiedSessionUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	memberID := r.FormValue("member_id")
	if strings.TrimSpace(memberID) == "" {
		http.Error(w, "member_id required", http.StatusBadRequest)
		return
	}
	if err := s.store.FollowMember(u.Email, memberID); err != nil {
		http.Error(w, "failed to follow", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/members/"+memberID, http.StatusSeeOther)
}

func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	u, ok := s.auth.RequireVerifiedSessionUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	billID := r.FormValue("bill_id")
	reaction := r.FormValue("reaction")
	note := r.FormValue("note")
	if strings.TrimSpace(billID) == "" {
		http.Error(w, "bill_id required", http.StatusBadRequest)
		return
	}
	if err := s.store.ReactToBill(u.Email, billID, reaction, note); err != nil {
		http.Error(w, "failed to save reaction", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/bills/"+billID, http.StatusSeeOther)
}

func (s *Server) handleLogSubmission(w http.ResponseWriter, r *http.Request) {
	u, ok := s.auth.RequireVerifiedSessionUser(w, r)
	if !ok {
		return
	}
	var payload struct {
		MemberID string `json:"member_id"`
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		Category string `json:"category"`
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
		payload.MemberID = r.FormValue("member_id")
		payload.Subject = r.FormValue("subject")
		payload.Body = r.FormValue("body")
		payload.Category = r.FormValue("category")
	}

	if strings.TrimSpace(payload.MemberID) == "" {
		http.Error(w, "member_id required", http.StatusBadRequest)
		return
	}
	if err := s.store.LogPolicySubmission(
		u.Email,
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
