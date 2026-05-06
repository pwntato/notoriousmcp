package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

// Handler implements the OAuth 2.0 endpoints.
type Handler struct {
	cfg      Config
	db       *db.Client
	oauthCfg *oauth2.Config
}

// New creates an auth Handler. Panics on invalid config — this is intentional:
// misconfiguration is a programming error caught at process startup, not a
// runtime condition. Call cfg.Validate() explicitly if you need an error return.
func New(cfg Config, dbClient *db.Client) *Handler {
	if err := cfg.Validate(); err != nil {
		panic("invalid auth config: " + err.Error())
	}
	// Log admin sub IDs at startup so promotions are observable in logs.
	// Logged as count + first-8-chars of each ID for auditability without
	// exposing full Google subject IDs.
	adminHints := make([]string, len(cfg.AdminGoogleIDs))
	for i, id := range cfg.AdminGoogleIDs {
		if len(id) > 8 {
			adminHints[i] = id[:8] + "..."
		} else {
			adminHints[i] = id
		}
	}
	log.Printf("auth: %d admin ID(s) configured: %v", len(cfg.AdminGoogleIDs), adminHints)
	return &Handler{
		cfg: cfg,
		db:  dbClient,
		oauthCfg: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

// RegisterRoutes wires the auth endpoints onto the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.wellKnown)
	mux.HandleFunc("GET /auth/login", h.login)
	mux.HandleFunc("GET /auth/callback", h.callback)
}

// requestScheme returns https when TLS is active or, if trustProxy is true,
// when X-Forwarded-Proto is "https". Only set trustProxy when the server is
// behind a trusted reverse proxy (CloudFront/ALB) — never for direct-to-internet.
func requestScheme(r *http.Request, trustProxy bool) string {
	if r.TLS != nil {
		return "https"
	}
	if trustProxy {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
			return "https"
		}
	}
	return "http"
}

// wellKnown serves the OAuth 2.0 authorization server metadata required by the MCP spec.
// token_endpoint is intentionally omitted until issue #4 implements it.
func (h *Handler) wellKnown(w http.ResponseWriter, r *http.Request) {
	base := requestScheme(r, h.cfg.TrustProxy) + "://" + r.Host
	meta := map[string]any{
		"issuer":                   base,
		"authorization_endpoint":   base + "/auth/login",
		"response_types_supported": []string{"code"},
		"grant_types_supported":    []string{"authorization_code"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// oauthStatePayload is the data packed into the Google state parameter.
type oauthStatePayload struct {
	Nonce             string `json:"n"`
	ClientRedirectURI string `json:"r"`
	ClientState       string `json:"s"`
}

// login initiates the OAuth flow by redirecting to Google.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	clientRedirectURI := r.URL.Query().Get("redirect_uri")

	// Validate redirect_uri against the configured redirect URL origin to
	// prevent open-redirect attacks.
	if clientRedirectURI != "" {
		if err := ValidateRedirectURI(h.cfg.RedirectURL, clientRedirectURI); err != nil {
			http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
			return
		}
	}

	nonce, err := generateState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Encode state payload as base64 JSON to avoid delimiter fragility.
	payload := oauthStatePayload{
		Nonce:             nonce,
		ClientRedirectURI: clientRedirectURI,
		ClientState:       r.URL.Query().Get("state"),
	}
	stateJSON, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	stateEncoded := base64.RawURLEncoding.EncodeToString(stateJSON)

	// Store nonce in a short-lived httpOnly cookie for CSRF validation.
	// Secure is set based on requestScheme — always true in production behind CloudFront.
	// Note: oauthStatePayload (redirect_uri + client state) is base64-encoded, not
	// encrypted — it is visible to the browser. It contains no secrets; the nonce is
	// the only security-critical value and it is validated separately via this cookie.
	// PKCE (RFC 7636) is not enforced here. If public/native MCP clients are supported
	// in future, add PKCE via oauth2.S256ChallengeOption before deploying to them.
	secure := requestScheme(r, h.cfg.TrustProxy) == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_nonce",
		Value:    nonce,
		Path:     "/auth",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	// AccessTypeOffline requests a refresh token. Google only returns one on
	// first authorization (or re-grant), so we don't need ApprovalForce.
	// upsertUser only stores the token when Google actually returns one.
	authURL := h.oauthCfg.AuthCodeURL(stateEncoded, oauth2.AccessTypeOffline)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// callback handles the Google OAuth callback, exchanges the code for tokens,
// creates or updates the user record, and redirects back to the MCP client.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check for OAuth error response first (e.g. user denied access).
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		desc := r.URL.Query().Get("error_description")
		if desc == "" {
			desc = oauthErr
		}
		// Truncate to avoid echoing arbitrarily large Google-sourced strings.
		if len(desc) > 200 {
			desc = desc[:200]
		}
		http.Error(w, "authorization denied: "+desc, http.StatusBadRequest)
		return
	}

	// Decode state payload.
	stateEncoded := r.URL.Query().Get("state")
	stateJSON, err := base64.RawURLEncoding.DecodeString(stateEncoded)
	if err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(stateJSON, &payload); err != nil {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	// Validate nonce against cookie to prevent CSRF.
	cookie, err := r.Cookie("oauth_nonce")
	if err != nil || cookie.Value != payload.Nonce {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	// Clear the nonce cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_nonce",
		Path:   "/auth",
		MaxAge: -1,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	// Exchange auth code for tokens.
	oauthToken, err := h.oauthCfg.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Fetch Google user info.
	info, err := fetchGoogleUserInfo(ctx, h.oauthCfg.Client(ctx, oauthToken))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Upsert user and apply admin bootstrap rule.
	if err := h.upsertUser(ctx, info, oauthToken.RefreshToken); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Issue our own short-lived access token.
	accessToken, err := IssueAccessToken(h.cfg.TokenSecret, info.Sub)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// If the login request included a redirect_uri, send the token there.
	// Otherwise fall back to returning JSON directly (useful for CLI/testing).
	// Known limitation: tokens are stateless with a 1h TTL. Revoking a user's
	// access (DB status change, removal from AdminGoogleIDs) does not invalidate
	// already-issued tokens — they remain valid until expiry.
	clientRedirectURI := payload.ClientRedirectURI
	if clientRedirectURI == "" {
		// Explicit JSON fallback — not an error condition.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   int(accessTokenTTL.Seconds()),
		})
		return
	}

	sep := "?"
	if strings.Contains(clientRedirectURI, "?") {
		sep = "&"
	}
	// The access token is passed as the `code` parameter in the redirect URL,
	// which means it appears in server logs, Referer headers, and browser history.
	// Tracked in issue #21: implement a short-lived opaque code exchange so the
	// bearer token is never exposed in a URL.
	target := clientRedirectURI + sep + "code=" + accessToken
	if payload.ClientState != "" {
		target += "&state=" + url.QueryEscape(payload.ClientState)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// ValidateRedirectURI ensures the client redirect URI exactly matches the
// server's configured redirect URL (scheme, host, and path). RFC 6749 §3.1.2
// requires exact matching. path.Clean neutralises traversal sequences first.
func ValidateRedirectURI(configuredRedirectURL, clientRedirectURI string) error {
	configured, err := url.Parse(configuredRedirectURL)
	if err != nil {
		return fmt.Errorf("invalid configured redirect URL: %w", err)
	}
	client, err := url.Parse(clientRedirectURI)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri: %w", err)
	}
	if configured.Scheme != client.Scheme ||
		configured.Host != client.Host ||
		path.Clean(configured.Path) != path.Clean(client.Path) {
		return fmt.Errorf("redirect_uri %q not allowed", clientRedirectURI)
	}
	return nil
}

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

type googleUserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func fetchGoogleUserInfo(ctx context.Context, client *http.Client) (*googleUserInfo, error) {
	resp, err := client.Get(googleUserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("userinfo decode: %w", err)
	}
	if info.Sub == "" {
		return nil, fmt.Errorf("userinfo missing sub")
	}
	return &info, nil
}

// upsertUser creates or updates the user record and applies the admin bootstrap rule.
// Note: GetUser → SaveUser is a non-atomic read-modify-write. Two concurrent
// first-logins race safely because: (a) SaveUser uses if_not_exists(CreatedAt)
// so CreatedAt is never overwritten, and (b) both concurrent reads return
// ErrNotFound, both set status=pending, so both writes produce the same result.
// A non-admin user whose status was already set won't be overwritten because the
// changed check skips the write when status/email/name haven't changed.
func (h *Handler) upsertUser(ctx context.Context, info *googleUserInfo, refreshToken string) error {
	existing, err := h.db.GetUser(ctx, info.Sub)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("get user: %w", err)
	}

	status := models.StatusPending
	if existing != nil {
		status = existing.Status
	}

	// Admin bootstrap: ADMIN_GOOGLE_IDS always wins, self-heals on every login.
	if slices.Contains(h.cfg.AdminGoogleIDs, info.Sub) {
		status = models.StatusAdmin
	}

	createdAt := time.Now().UTC()
	if existing != nil {
		createdAt = existing.CreatedAt
	}

	// Skip the write if nothing has changed — avoids an unconditional DB write
	// on every login for users whose profile is already current.
	changed := existing == nil ||
		existing.Status != status ||
		existing.Email != info.Email ||
		existing.Name != info.Name
	if changed {
		u := &models.User{
			UserID:    info.Sub,
			Email:     info.Email,
			Name:      info.Name,
			Status:    status,
			CreatedAt: createdAt,
		}
		if err := h.db.SaveUser(ctx, u); err != nil {
			return fmt.Errorf("save user: %w", err)
		}
	}

	// Only update the refresh token if Google returned one (only on first
	// authorization or when the user re-grants access).
	if refreshToken != "" {
		if err := h.db.SaveRefreshToken(ctx, info.Sub, refreshToken); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
	}

	return nil
}
