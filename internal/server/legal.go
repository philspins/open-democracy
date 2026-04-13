package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/philspins/open-democracy/internal/templates"
)

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	_ = templates.PrivacyPolicyPage(s.parliamentStatus()).Render(r.Context(), w)
}

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	_ = templates.TermsOfServicePage(s.parliamentStatus()).Render(r.Context(), w)
}

func (s *Server) handleDeleteDataPage(w http.ResponseWriter, r *http.Request) {
	_ = templates.DataDeletionPage(s.parliamentStatus(), r.URL.Query().Get("confirmation_code")).Render(r.Context(), w)
}

func (s *Server) handleDeleteDataCallback(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	signed := strings.TrimSpace(r.FormValue("signed_request"))
	if signed == "" {
		writeDeleteDataError(w, "signed_request_required", http.StatusBadRequest)
		return
	}

	payload, err := parseMetaSignedRequest(signed, os.Getenv("FACEBOOK_CLIENT_SECRET"))
	if err != nil {
		writeDeleteDataError(w, "invalid_signed_request", http.StatusBadRequest)
		return
	}

	configuredAppID := strings.TrimSpace(os.Getenv("FACEBOOK_CLIENT_ID"))
	if configuredAppID != "" && payload.AppID != "" && payload.AppID != configuredAppID {
		writeDeleteDataError(w, "app_id_mismatch", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.UserID) == "" {
		writeDeleteDataError(w, "user_id_required", http.StatusBadRequest)
		return
	}

	confirmationCode, err := generateConfirmationCode()
	if err != nil {
		http.Error(w, "failed to generate confirmation code", http.StatusInternalServerError)
		return
	}

	statusURL := publicURLForRequest(r, "/delete-data?confirmation_code="+url.QueryEscape(confirmationCode))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"url":               statusURL,
		"confirmation_code": confirmationCode,
	})
}

type metaDeleteSignedPayload struct {
	Algorithm string `json:"algorithm"`
	AppID     string `json:"app_id"`
	UserID    string `json:"user_id"`
}

func parseMetaSignedRequest(signedRequest, appSecret string) (metaDeleteSignedPayload, error) {
	if strings.TrimSpace(appSecret) == "" {
		return metaDeleteSignedPayload{}, errors.New("missing app secret")
	}
	parts := strings.SplitN(signedRequest, ".", 2)
	if len(parts) != 2 {
		return metaDeleteSignedPayload{}, errors.New("invalid signed request format")
	}

	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return metaDeleteSignedPayload{}, err
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	_, _ = mac.Write([]byte(parts[1]))
	expectedSignature := mac.Sum(nil)
	if !hmac.Equal(providedSignature, expectedSignature) {
		return metaDeleteSignedPayload{}, errors.New("signature mismatch")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return metaDeleteSignedPayload{}, err
	}

	var payload metaDeleteSignedPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return metaDeleteSignedPayload{}, err
	}
	if !strings.EqualFold(payload.Algorithm, "HMAC-SHA256") {
		return metaDeleteSignedPayload{}, errors.New("unsupported algorithm")
	}

	return payload, nil
}

func generateConfirmationCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func publicURLForRequest(r *http.Request, path string) string {
	base := strings.TrimSpace(os.Getenv("OAUTH_BASE_URL"))
	if base != "" {
		return strings.TrimRight(base, "/") + path
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func writeDeleteDataError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
