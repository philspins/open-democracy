package auth

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/philspins/open-democracy/internal/store"
)

// Service owns authentication and session flows.
type Service struct {
	store       *store.Store
	baseURL     string
	emailer     verificationEmailSender
	rateLimiter *simpleRateLimiter
	httpClient  *http.Client
	// trustProxy enables reading client IP from X-Forwarded-For / X-Real-IP.
	// Only set this when the service is guaranteed to sit behind a trusted
	// reverse proxy that strips and re-sets those headers — otherwise clients
	// can spoof their IP to bypass rate limits. Controlled by TRUST_PROXY=true.
	trustProxy bool
}

type verificationEmailSender interface {
	SendVerificationEmail(ctx context.Context, toEmail, verifyURL, code string) error
}

type sesVerificationSender struct {
	client    *sesv2.Client
	fromEmail string
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

func New(st *store.Store, baseURL string) *Service {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	var emailer verificationEmailSender
	fromEmail := strings.TrimSpace(os.Getenv("SES_FROM_EMAIL"))
	if fromEmail != "" {
		cfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			log.Printf("ses config load failed: %v", err)
		} else {
			emailer = &sesVerificationSender{client: sesv2.NewFromConfig(cfg), fromEmail: fromEmail}
		}
	}

	return &Service{
		store:       st,
		baseURL:     baseURL,
		emailer:     emailer,
		rateLimiter: newSimpleRateLimiter(),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		trustProxy:  strings.ToLower(strings.TrimSpace(os.Getenv("TRUST_PROXY"))) == "true",
	}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/signup", s.HandleSignupPage)
	mux.HandleFunc("GET /auth/login", s.HandleLoginPage)
	mux.HandleFunc("POST /auth/request-verification", s.HandleRequestVerification)
	mux.HandleFunc("POST /auth/verify", s.HandleVerifyEmail)
	mux.HandleFunc("POST /auth/logout", s.HandleLogout)
	mux.HandleFunc("GET /auth/me", s.HandleWhoAmI)
	mux.HandleFunc("GET /auth/google/login", s.HandleGoogleLogin)
	mux.HandleFunc("GET /auth/google/callback", s.HandleGoogleCallback)
	mux.HandleFunc("GET /auth/facebook/login", s.HandleFacebookLogin)
	mux.HandleFunc("GET /auth/facebook/callback", s.HandleFacebookCallback)
}

func (s *Service) isSecureCookie() bool {
	return strings.HasPrefix(strings.ToLower(s.baseURL), "https://")
}

func (s *Service) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.httpClient = client
	}
}

// SetTrustProxy enables or disables forwarded-header IP extraction.
// Enable only when the server is guaranteed to run behind a trusted reverse
// proxy that strips and re-sets X-Forwarded-For / X-Real-IP.
func (s *Service) SetTrustProxy(trust bool) {
	s.trustProxy = trust
}

func (s *Service) clientIP(r *http.Request) string {
	if s.trustProxy {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
		if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
			return xrip
		}
	}
	hostPort := strings.TrimSpace(r.RemoteAddr)
	if i := strings.LastIndex(hostPort, ":"); i > 0 {
		return hostPort[:i]
	}
	return hostPort
}

func (s *Service) rateLimitAllowed(key string, limit int, window time.Duration) bool {
	if s.rateLimiter == nil {
		return true
	}
	return s.rateLimiter.allow(key, limit, window, time.Now().UTC())
}
