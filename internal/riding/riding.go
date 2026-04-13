package riding

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

type LookupResult struct {
	Representatives          []opennorth.Representative
	FederalRepresentative    opennorth.Representative
	ProvincialRepresentative opennorth.Representative
	FederalRidingID          string
	ProvincialRidingID       string
}

// Service owns riding lookup behavior and rendering.
type Service struct {
	store         *store.Store
	googleMapsKey string
	placesApiKey  string
	geocodeFn     func(ctx context.Context, address, apiKey string) (float64, float64, error)
	repsFn        func(ctx context.Context, lat, lng float64) ([]opennorth.Representative, error)
}

func New(st *store.Store, googleMapsKey string) *Service {
	if strings.TrimSpace(googleMapsKey) == "" {
		log.Printf("warning: GOOGLE_MAPS_API_KEY not set; address geocoding disabled")
	}
	return &Service{
		store:         st,
		googleMapsKey: strings.TrimSpace(googleMapsKey),
		placesApiKey:  strings.TrimSpace(os.Getenv("GOOGLE_PLACES_API_KEY")),
		geocodeFn:     opennorth.GeocodeAddress,
		repsFn:        opennorth.GetRepresentativesByLatLng,
	}
}

func (s *Service) SetLookups(
	geocodeFn func(ctx context.Context, address, apiKey string) (float64, float64, error),
	repsFn func(ctx context.Context, lat, lng float64) ([]opennorth.Representative, error),
) {
	if geocodeFn != nil {
		s.geocodeFn = geocodeFn
	}
	if repsFn != nil {
		s.repsFn = repsFn
	}
}

func (s *Service) parliamentStatus() store.ParliamentStatus {
	ps, _ := s.store.GetParliamentStatus(scraper.CurrentParliament, scraper.CurrentSession)
	return ps
}

func (s *Service) PlacesAPIKey() string {
	return s.placesApiKey
}

func (s *Service) Lookup(ctx context.Context, address string) (LookupResult, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return LookupResult{}, nil
	}
	if s.googleMapsKey == "" {
		return LookupResult{}, fmt.Errorf("address lookup is not configured (missing GOOGLE_MAPS_API_KEY)")
	}

	lat, lng, err := s.geocodeFn(ctx, address, s.googleMapsKey)
	if err != nil {
		return LookupResult{}, fmt.Errorf("geocode: %w", err)
	}
	reps, err := s.repsFn(ctx, lat, lng)
	if err != nil {
		return LookupResult{}, fmt.Errorf("representatives: %w", err)
	}

	result := LookupResult{Representatives: s.attachLocalMemberIDs(reps)}
	for _, rep := range result.Representatives {
		if result.FederalRidingID == "" && strings.EqualFold(rep.ElectedOffice, "MP") {
			result.FederalRepresentative = rep
			result.FederalRidingID = rep.DistrictName
			continue
		}
		if result.ProvincialRidingID == "" && isProvincialOffice(rep.ElectedOffice) {
			result.ProvincialRepresentative = rep
			result.ProvincialRidingID = rep.DistrictName
		}
	}

	return result, nil
}

func (s *Service) attachLocalMemberIDs(reps []opennorth.Representative) []opennorth.Representative {
	out := make([]opennorth.Representative, len(reps))
	copy(out, reps)
	for i, rep := range out {
		if !strings.EqualFold(rep.ElectedOffice, "MP") {
			continue
		}
		local, _ := s.store.GetMembersByRiding(rep.DistrictName)
		for _, member := range local {
			if strings.EqualFold(member.Name, rep.Name) {
				out[i].LocalMemberID = member.ID
				break
			}
		}
	}
	return out
}

func isProvincialOffice(office string) bool {
	office = strings.ToLower(strings.TrimSpace(office))
	switch office {
	case "mla", "mpp", "mna", "mha", "mha (nl)", "member of the legislative assembly", "member of provincial parliament", "member of the national assembly", "member of the house of assembly":
		return true
	}
	return strings.Contains(office, "legislative assembly") || strings.Contains(office, "national assembly") || strings.Contains(office, "provincial parliament") || strings.Contains(office, "house of assembly")
}

func (s *Service) HandleLookup(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	ps := s.parliamentStatus()
	var (
		reps      []opennorth.Representative
		lookupErr string
	)
	if address != "" {
		result, err := s.Lookup(r.Context(), address)
		if err != nil {
			log.Printf("riding lookup error for %q: %v", address, err)
			if strings.Contains(err.Error(), "missing GOOGLE_MAPS_API_KEY") {
				lookupErr = "Address lookup is not configured (missing GOOGLE_MAPS_API_KEY)."
			} else if strings.HasPrefix(err.Error(), "representatives:") {
				lookupErr = "Could not look up representatives. Please try again."
			} else {
				lookupErr = "Could not locate that address. Please try a more specific Canadian address."
			}
		} else {
			reps = result.Representatives
		}
	}
	_ = templates.RidingLookup(ps, address, reps, lookupErr, s.placesApiKey).Render(r.Context(), w)
}
