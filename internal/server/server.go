// Package server wires HTTP routes to the store and renders templ templates.
package server

import (
	"net/http"
	"strconv"

	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

// Server holds application dependencies.
type Server struct {
	store *store.Store
	mux   *http.ServeMux
}

// New creates a Server and registers all routes.
func New(st *store.Store) *Server {
	s := &Server{store: st, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /", s.handleHome)
	s.mux.HandleFunc("GET /bills", s.handleBills)
	s.mux.HandleFunc("GET /bills/{id}", s.handleBillDetail)
	s.mux.HandleFunc("GET /votes", s.handleVotes)
	s.mux.HandleFunc("GET /members", s.handleMembers)
	s.mux.HandleFunc("GET /members/{id}", s.handleMemberProfile)
	s.mux.HandleFunc("GET /compare", s.handleCompare)
	s.mux.HandleFunc("GET /riding", s.handleRiding)
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
	_ = templates.Home(ps, bills, divs).Render(r.Context(), w)
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
	_ = templates.BillDetail(ps, bill, stages, divs).Render(r.Context(), w)
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
