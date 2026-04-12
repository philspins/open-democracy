// Package server wires HTTP routes to the store and renders templ templates.
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/philspins/open-democracy/internal/auth"
	"github.com/philspins/open-democracy/internal/riding"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

// Server holds application dependencies.
type Server struct {
	store  *store.Store
	mux    *http.ServeMux
	auth   *auth.Service
	riding *riding.Service
}

// New creates a Server and registers all routes.
func New(st *store.Store) *Server {
	baseURL := strings.TrimRight(os.Getenv("OAUTH_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	googleMapsKey := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY"))

	s := &Server{
		store:  st,
		mux:    http.NewServeMux(),
		auth:   auth.New(st, baseURL),
		riding: riding.New(st, googleMapsKey),
	}

	s.mux.HandleFunc("GET /", s.handleHome)
	s.mux.HandleFunc("GET /bills", s.handleBills)
	s.mux.HandleFunc("GET /bills/{id}", s.handleBillDetail)
	s.mux.HandleFunc("GET /votes", s.handleVotes)
	s.mux.HandleFunc("GET /members", s.handleMembers)
	s.mux.HandleFunc("GET /members/{id}", s.handleMemberProfile)
	s.mux.HandleFunc("GET /compare", s.handleCompare)
	s.mux.HandleFunc("GET /riding", s.riding.HandleLookup)
	s.mux.HandleFunc("POST /api/follow", s.handleFollow)
	s.mux.HandleFunc("POST /api/react", s.handleReact)
	s.mux.HandleFunc("POST /api/log-submission", s.handleLogSubmission)
	s.auth.RegisterRoutes(s.mux)

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
	_ = templates.Home(ps, bills, divs, "", store.MemberRow{}).Render(r.Context(), w)
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

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	postal := r.FormValue("postal_code")
	memberID := r.FormValue("member_id")
	if user, ok := s.auth.SessionUser(r); ok {
		email = user.Email
		if postal == "" {
			postal = user.PostalCode
		}
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(memberID) == "" {
		http.Error(w, "email and member_id required", http.StatusBadRequest)
		return
	}
	if !s.auth.RequireVerifiedEmail(w, email, postal) {
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
	if user, ok := s.auth.SessionUser(r); ok {
		email = user.Email
		if postal == "" {
			postal = user.PostalCode
		}
	}
	if strings.TrimSpace(email) == "" || strings.TrimSpace(billID) == "" {
		http.Error(w, "email and bill_id required", http.StatusBadRequest)
		return
	}
	if !s.auth.RequireVerifiedEmail(w, email, postal) {
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
		if user, ok := s.auth.SessionUser(r); ok {
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
	if !s.auth.RequireVerifiedEmail(w, payload.Email, payload.PostalCode) {
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
