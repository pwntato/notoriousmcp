package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// New creates an auth Handler.
func New(cfg Config, dbClient *db.Client) *Handler {
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

// wellKnown serves the OAuth 2.0 authorization server metadata required by the MCP spec.
func (h *Handler) wellKnown(w http.ResponseWriter, r *http.Request) {
	// Derive the server base URL from the request so it works on any deployment.
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	} else if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	base := scheme + "://" + r.Host

	meta := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":               base + "/auth/login",
		"token_endpoint":                        base + "/auth/token",
		"response_types_supported":             []string{"code"},
		"grant_types_supported":                []string{"authorization_code"},
		"code_challenge_methods_supported":     []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// login initiates the OAuth flow by redirecting to Google.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store state in a short-lived cookie for CSRF validation on callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/auth",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	// Preserve the MCP client's redirect_uri and state across the Google round-trip.
	clientRedirectURI := r.URL.Query().Get("redirect_uri")
	clientState := r.URL.Query().Get("state")
	combinedState := state + "|" + clientRedirectURI + "|" + clientState

	url := h.oauthCfg.AuthCodeURL(combinedState, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, url, http.StatusFound)
}

// callback handles the Google OAuth callback, exchanges the code for tokens,
// creates or updates the user record, and redirects back to the MCP client.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Validate state to prevent CSRF.
	cookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	rawState := r.URL.Query().Get("state")
	parts := strings.SplitN(rawState, "|", 3)
	if len(parts) != 3 || parts[0] != cookie.Value {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	clientRedirectURI, clientState := parts[1], parts[2]

	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
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
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	// Fetch Google user info.
	info, err := fetchGoogleUserInfo(ctx, h.oauthCfg.Client(ctx, oauthToken))
	if err != nil {
		http.Error(w, "failed to fetch user info", http.StatusInternalServerError)
		return
	}

	// Upsert user and apply admin bootstrap rule.
	if err := h.upsertUser(ctx, info, oauthToken.RefreshToken); err != nil {
		http.Error(w, "failed to save user", http.StatusInternalServerError)
		return
	}

	// Issue our own short-lived access token.
	accessToken, err := IssueAccessToken(h.cfg.TokenSecret, info.Sub)
	if err != nil {
		http.Error(w, "failed to issue token", http.StatusInternalServerError)
		return
	}

	// Redirect back to MCP client with the access token.
	redirectURL := clientRedirectURI
	if redirectURL == "" {
		// No client redirect — return token as JSON (useful for testing).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   int(accessTokenTTL.Seconds()),
		})
		return
	}

	sep := "?"
	if strings.Contains(redirectURL, "?") {
		sep = "&"
	}
	target := redirectURL + sep + "code=" + accessToken
	if clientState != "" {
		target += "&state=" + clientState
	}
	http.Redirect(w, r, target, http.StatusFound)
}

type googleUserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func fetchGoogleUserInfo(ctx context.Context, client *http.Client) (*googleUserInfo, error) {
	resp, err := client.Get("https://openidconnect.googleapis.com/v1/userinfo")
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
func (h *Handler) upsertUser(ctx context.Context, info *googleUserInfo, refreshToken string) error {
	existing, err := h.db.GetUser(ctx, info.Sub)
	if err != nil && err != db.ErrNotFound {
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

	// Only update the refresh token if Google returned one (it's only returned
	// on first authorization or when access is re-granted).
	if refreshToken != "" {
		if err := h.db.SaveRefreshToken(ctx, info.Sub, refreshToken); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
	}

	return nil
}
