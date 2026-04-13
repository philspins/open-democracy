package riding

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/philspins/open-democracy/internal/opennorth"
	"github.com/philspins/open-democracy/internal/scraper"
	"github.com/philspins/open-democracy/internal/store"
	"github.com/philspins/open-democracy/internal/templates"
)

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

func (s *Service) HandleLookup(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	ps := s.parliamentStatus()
	var (
		reps      []opennorth.Representative
		lookupErr string
	)
	if address != "" {
		if s.googleMapsKey == "" {
			lookupErr = "Address lookup is not configured (missing GOOGLE_MAPS_API_KEY)."
		} else {
			lat, lng, err := s.geocodeFn(r.Context(), address, s.googleMapsKey)
			if err != nil {
				log.Printf("geocode error for %q: %v", address, err)
				lookupErr = "Could not locate that address. Please try a more specific Canadian address."
			} else {
				reps, err = s.repsFn(r.Context(), lat, lng)
				if err != nil {
					log.Printf("open north error lat=%f lng=%f: %v", lat, lng, err)
					lookupErr = "Could not look up representatives. Please try again."
				} else {
					for i, rep := range reps {
						if !strings.EqualFold(rep.ElectedOffice, "MP") {
							continue
						}
						local, _ := s.store.GetMembersByRiding(rep.DistrictName)
						for _, m := range local {
							if strings.EqualFold(m.Name, rep.Name) || strings.EqualFold(m.Riding, rep.DistrictName) {
								reps[i].LocalMemberID = m.ID
								break
							}
						}
					}
				}
			}
		}
	}
	_ = templates.RidingLookup(ps, address, reps, lookupErr, s.placesApiKey).Render(r.Context(), w)
}
