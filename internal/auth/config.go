package auth

import (
	"fmt"
	"net/http"
	"strings"
)

// Config holds OAuth credentials and server settings needed by the auth layer.
// Call Validate() before passing to New().
type Config struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string   // e.g. https://notoriousmcp.com/auth/callback
	AdminGoogleIDs []string // subject IDs that are always promoted to admin on login
	TokenSecret    []byte   // HMAC-SHA256 secret for signing access tokens; min 32 bytes
	// TrustProxy enables X-Forwarded-Proto scheme detection. Set true only when
	// running behind a trusted reverse proxy (CloudFront/ALB). Never set on
	// direct-to-internet deployments — it allows scheme downgrade spoofing.
	TrustProxy bool
	// PublicBaseURL is the canonical public base URL (e.g. https://d2eudgpkavi25i.cloudfront.net).
	// When set, used verbatim in /.well-known/oauth-authorization-server instead of
	// deriving from r.Host (which reflects the Lambda URL behind CloudFront, not the
	// public CloudFront domain).
	PublicBaseURL string
	// GoogleTokenURL overrides Google's token endpoint. Empty means use the
	// default (google.Endpoint). Set in tests only to point at a fake server.
	GoogleTokenURL string
}

// publicBase returns the canonical public base URL with no trailing slash.
// Uses PublicBaseURL when set; otherwise derives it from the request.
func (c Config) publicBase(r *http.Request) string {
	if c.PublicBaseURL != "" {
		return strings.TrimRight(c.PublicBaseURL, "/")
	}
	return strings.TrimRight(requestScheme(r, c.TrustProxy)+"://"+r.Host, "/")
}

// Validate returns an error if any required field is missing or too short.
func (c Config) Validate() error {
	if c.ClientID == "" {
		return fmt.Errorf("auth.Config.ClientID is required")
	}
	if c.ClientSecret == "" {
		return fmt.Errorf("auth.Config.ClientSecret is required")
	}
	if c.RedirectURL == "" {
		return fmt.Errorf("auth.Config.RedirectURL is required")
	}
	if len(c.TokenSecret) < 32 {
		return fmt.Errorf("auth.Config.TokenSecret must be at least 32 bytes (got %d)", len(c.TokenSecret))
	}
	return nil
}
