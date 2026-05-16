package auth

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OAuthProvider selects which upstream OAuth 2.0 / OIDC provider to use.
type OAuthProvider string

const (
	ProviderGoogle OAuthProvider = "google"
	ProviderOkta   OAuthProvider = "okta"
)

// Config holds OAuth credentials and server settings needed by the auth layer.
// Call Validate() before passing to New().
type Config struct {
	Provider     OAuthProvider // "google" (default) or "okta"
	OktaDomain   string        // required when Provider == ProviderOkta, e.g. "dev-123.okta.com"
	ClientID     string
	ClientSecret string
	RedirectURL  string   // e.g. https://notoriousmcp.com/auth/callback
	AdminIDs     []string // subject IDs (provider "sub" claim) always promoted to admin on login
	TokenSecret  []byte   // HMAC-SHA256 secret for signing access tokens; min 32 bytes
	// TrustProxy enables X-Forwarded-Proto scheme detection. Set true only when
	// running behind a trusted reverse proxy (CloudFront/ALB). Never set on
	// direct-to-internet deployments — it allows scheme downgrade spoofing.
	TrustProxy bool
	// PublicBaseURL is the canonical public base URL (e.g. https://d2eudgpkavi25i.cloudfront.net).
	// When set, used verbatim in /.well-known/oauth-authorization-server instead of
	// deriving from r.Host (which reflects the Lambda URL behind CloudFront, not the
	// public CloudFront domain).
	PublicBaseURL string
	// OverrideTokenURL overrides the provider token endpoint. Empty means use the
	// provider default. Set in tests only to point at a fake server.
	OverrideTokenURL string
}

// providerEndpoint returns the oauth2.Endpoint for the configured provider.
// For Okta it derives endpoints from the domain; for Google it uses the
// well-known constant from golang.org/x/oauth2/google.
func (c Config) providerEndpoint() oauth2.Endpoint {
	if c.OverrideTokenURL != "" {
		return oauth2.Endpoint{TokenURL: c.OverrideTokenURL}
	}
	switch c.provider() {
	case ProviderOkta:
		base := "https://" + c.OktaDomain + "/oauth2/default"
		return oauth2.Endpoint{
			AuthURL:  base + "/v1/authorize",
			TokenURL: base + "/v1/token",
		}
	default:
		return google.Endpoint
	}
}

// userInfoURL returns the OIDC userinfo endpoint for the configured provider.
func (c Config) userInfoURL() string {
	switch c.provider() {
	case ProviderOkta:
		return "https://" + c.OktaDomain + "/oauth2/default/v1/userinfo"
	default:
		return "https://openidconnect.googleapis.com/v1/userinfo"
	}
}

// provider returns the effective provider, defaulting to Google when unset.
func (c Config) provider() OAuthProvider {
	if c.Provider == "" {
		return ProviderGoogle
	}
	return c.Provider
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
	if c.provider() == ProviderOkta && c.OktaDomain == "" {
		return fmt.Errorf("auth.Config.OktaDomain is required when Provider is okta")
	}
	return nil
}
