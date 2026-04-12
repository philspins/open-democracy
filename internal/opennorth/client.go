// Package opennorth provides a client for the Open North Represent API
// (https://represent.opennorth.ca/api/) and a helper for the Google Maps
// Geocoding API.
package opennorth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const (
	openNorthBase = "https://represent.opennorth.ca"
	geocodeBase   = "https://maps.googleapis.com/maps/api/geocode/json"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Office holds contact information for one of a representative's offices.
type Office struct {
	Type   string `json:"type"`
	Tel    string `json:"tel"`
	Fax    string `json:"fax"`
	Postal string `json:"postal"`
}

// Representative is a single record returned by the Open North Represent API.
type Representative struct {
	Name          string   `json:"name"`
	ElectedOffice string   `json:"elected_office"`
	PartyName     string   `json:"party_name"`
	DistrictName  string   `json:"district_name"`
	Email         string   `json:"email"`
	URL           string   `json:"url"`
	PhotoURL      string   `json:"photo_url"`
	PersonalURL   string   `json:"personal_url"`
	Offices       []Office `json:"offices"`

	// LocalMemberID is set by callers when the rep can be matched to a local DB
	// member, enabling a link to the member's profile page.
	LocalMemberID string `json:"-"`
}

type openNorthPage struct {
	Objects []Representative `json:"objects"`
}

// GetRepresentativesByLatLng queries the Open North API for all representatives
// at the given latitude and longitude.
func GetRepresentativesByLatLng(ctx context.Context, lat, lng float64) ([]Representative, error) {
	endpoint := fmt.Sprintf("%s/representatives/?point=%.6f,%.6f&format=json", openNorthBase, lat, lng)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open north api returned status %d", resp.StatusCode)
	}
	var page openNorthPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}
	return page.Objects, nil
}

// geocodeResponse is the minimal subset of the Google Maps Geocoding API
// response that we need.
type geocodeResponse struct {
	Results []struct {
		Geometry struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
	} `json:"results"`
	Status string `json:"status"`
}

// GeocodeAddress converts a free-text address to latitude and longitude using
// the Google Maps Geocoding API. Results are biased toward Canada.
// apiKey must be a valid server-side restricted API key with the Geocoding API
// enabled.
func GeocodeAddress(ctx context.Context, address, apiKey string) (lat, lng float64, err error) {
	params := url.Values{
		"address":    {address},
		"key":        {apiKey},
		"region":     {"ca"},
		"components": {"country:CA"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geocodeBase+"?"+params.Encode(), nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	var gr geocodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return 0, 0, err
	}
	if gr.Status != "OK" || len(gr.Results) == 0 {
		return 0, 0, fmt.Errorf("geocode failed: status %s", gr.Status)
	}
	loc := gr.Results[0].Geometry.Location
	return loc.Lat, loc.Lng, nil
}
