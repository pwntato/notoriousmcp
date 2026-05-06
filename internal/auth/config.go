package auth

// Config holds OAuth credentials and server settings needed by the auth layer.
// All fields must be populated before calling New.
type Config struct {
	ClientID      string
	ClientSecret  string
	RedirectURL   string // e.g. https://notoriousmcp.com/auth/callback
	AdminGoogleIDs []string // subject IDs that are always promoted to admin
	TokenSecret   []byte   // HMAC secret for signing access tokens
}
