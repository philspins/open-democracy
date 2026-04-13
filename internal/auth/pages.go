package auth

import (
	"net/http"
	"os"
	"strings"

	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

func (s *Service) parliamentStatus() store.ParliamentStatus {
	ps, _ := s.store.GetParliamentStatus(scraper.CurrentParliament, scraper.CurrentSession)
	return ps
}

func (s *Service) HandleSignupPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.SessionUser(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ps := s.parliamentStatus()
	googleClientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	facebookAppID := strings.TrimSpace(os.Getenv("FACEBOOK_CLIENT_ID"))
	_ = templates.AuthPage(ps, "signup", googleClientID, facebookAppID).Render(r.Context(), w)
}

func (s *Service) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.SessionUser(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ps := s.parliamentStatus()
	googleClientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	facebookAppID := strings.TrimSpace(os.Getenv("FACEBOOK_CLIENT_ID"))
	_ = templates.AuthPage(ps, "login", googleClientID, facebookAppID).Render(r.Context(), w)
}
