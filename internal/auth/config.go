package auth

import "fmt"

// Config holds OAuth credentials and server settings needed by the auth layer.
// Call Validate() before passing to New().
type Config struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string   // e.g. https://notoriousmcp.com/auth/callback
	AdminGoogleIDs []string // subject IDs that are always promoted to admin on login
	TokenSecret    []byte   // HMAC-SHA256 secret for signing access tokens; min 32 bytes
	TrustProxy     bool     // set true when running behind a trusted reverse proxy (CloudFront/ALB)
	                        // that sets X-Forwarded-Proto; do not enable on direct-to-internet deployments
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
