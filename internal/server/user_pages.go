package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/riding"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

func preferredLookupAddress(user store.UserRow) string {
	if strings.TrimSpace(user.Address) != "" {
		return strings.TrimSpace(user.Address)
	}
	return strings.TrimSpace(user.PostalCode)
}

func (s *Server) loadRepresentativeContext(r *http.Request, user store.UserRow) (string, riding.LookupResult) {
	address := preferredLookupAddress(user)
	if address == "" {
		return "", riding.LookupResult{}
	}

	result, err := s.riding.Lookup(r.Context(), address)
	if err == nil {
		return address, result
	}

	log.Printf("loadRepresentativeContext lookup failed for %q: %v", address, err)
	return address, fallbackLookupResult(user, s.store)
}

func fallbackLookupResult(user store.UserRow, st *store.Store) riding.LookupResult {
	result := riding.LookupResult{
		FederalRidingID:    strings.TrimSpace(user.FederalRidingID),
		ProvincialRidingID: strings.TrimSpace(user.ProvincialRidingID),
	}
	if result.FederalRidingID != "" {
		members, _ := st.GetMembersByRiding(result.FederalRidingID)
		if len(members) > 0 {
			member := members[0]
			result.FederalRepresentative = opennorth.Representative{
				Name:          member.Name,
				ElectedOffice: "MP",
				PartyName:     member.Party,
				DistrictName:  member.Riding,
				Email:         member.Email,
				URL:           member.Website,
				PhotoURL:      member.PhotoURL,
				LocalMemberID: member.ID,
			}
		}
	}
	if result.ProvincialRidingID != "" {
		result.ProvincialRepresentative = opennorth.Representative{
			Name:          "Current provincial representative",
			ElectedOffice: "Provincial representative",
			DistrictName:  result.ProvincialRidingID,
		}
	}
	return result
}

func (s *Server) handleRiding(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	ps := s.parliamentStatus()
	var (
		reps      []opennorth.Representative
		lookupErr string
	)

	if address != "" {
		result, err := s.riding.Lookup(r.Context(), address)
		if err != nil {
			log.Printf("handleRiding lookup failed for %q: %v", address, err)
			switch {
			case strings.Contains(err.Error(), "missing GOOGLE_MAPS_API_KEY"):
				lookupErr = "Address lookup is not configured (missing GOOGLE_MAPS_API_KEY)."
			case strings.HasPrefix(err.Error(), "representatives:"):
				lookupErr = "Could not look up representatives. Please try again."
			default:
				lookupErr = "Could not locate that address. Please try a more specific Canadian address."
			}
		} else {
			reps = result.Representatives
			if user, ok := s.auth.SessionUser(r); ok {
				if _, saveErr := s.store.UpdateUserLocation(user.ID, address, result.FederalRidingID, result.ProvincialRidingID); saveErr != nil {
					log.Printf("handleRiding save failed for user=%q: %v", user.ID, saveErr)
				}
			}
		}
	}

	_ = templates.RidingLookup(ps, address, reps, lookupErr, s.riding.PlacesAPIKey()).Render(r.Context(), w)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth.SessionUser(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderProfile(w, r, user, preferredLookupAddress(user), "", r.URL.Query().Get("updated") == "1")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		address := strings.TrimSpace(r.FormValue("address"))
		if address == "" {
			if _, err := s.store.UpdateUserLocation(user.ID, "", "", ""); err != nil {
				http.Error(w, "failed to clear address", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/profile?updated=1", http.StatusSeeOther)
			return
		}

		result, err := s.riding.Lookup(r.Context(), address)
		if err != nil {
			lookupErr := "Could not locate that address. Please try a more specific Canadian address."
			if strings.Contains(err.Error(), "missing GOOGLE_MAPS_API_KEY") {
				lookupErr = "Address lookup is not configured (missing GOOGLE_MAPS_API_KEY)."
			} else if strings.HasPrefix(err.Error(), "representatives:") {
				lookupErr = "Could not look up representatives. Please try again."
			}
			s.renderProfile(w, r, user, address, lookupErr, false)
			return
		}

		if _, err := s.store.UpdateUserLocation(user.ID, address, result.FederalRidingID, result.ProvincialRidingID); err != nil {
			http.Error(w, "failed to save profile", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/profile?updated=1", http.StatusSeeOther)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderProfile(w http.ResponseWriter, r *http.Request, user store.UserRow, address, lookupErr string, updated bool) {
	address = strings.TrimSpace(address)
	var result riding.LookupResult
	if address != "" {
		lookup, err := s.riding.Lookup(r.Context(), address)
		if err == nil {
			result = lookup
		} else if lookupErr == "" {
			log.Printf("renderProfile lookup failed for %q: %v", address, err)
			result = fallbackLookupResult(user, s.store)
		}
	} else {
		result = fallbackLookupResult(user, s.store)
	}

	_ = templates.ProfilePage(
		s.parliamentStatus(),
		user,
		address,
		result.Representatives,
		result.FederalRepresentative,
		result.ProvincialRepresentative,
		lookupErr,
		updated,
		s.riding.PlacesAPIKey(),
	).Render(r.Context(), w)
}
