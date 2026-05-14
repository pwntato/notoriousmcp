package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.protectedResource)
	mux.HandleFunc("GET /auth/login", h.login)
	mux.HandleFunc("GET /auth/callback", h.callback)
	mux.HandleFunc("POST /auth/token", h.token)
	mux.HandleFunc("POST /register", h.register)
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
func (h *Handler) wellKnown(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.publicBase(r)
	meta := map[string]any{
		"issuer":                            base,
		"authorization_endpoint":            base + "/auth/login",
		"token_endpoint":                    base + "/auth/token",
		"registration_endpoint":             base + "/register",
		"response_types_supported":          []string{"code"},
		"grant_types_supported":             []string{"authorization_code"},
		"code_challenge_methods_supported":   []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// protectedResource serves the OAuth 2.0 Protected Resource Metadata (RFC 9728).
// Claude Code's MCP SDK reads this document (linked from the WWW-Authenticate header
// on 401 responses) to discover the authorization server before starting OAuth.
func (h *Handler) protectedResource(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.publicBase(r)
	meta := map[string]any{
		"resource":               base,
		"authorization_servers": []string{base},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

// oauthStatePayload is the data packed into the Google state parameter.
type oauthStatePayload struct {
	Nonce             string `json:"n"`
	ClientRedirectURI string `json:"r"`
	ClientState       string `json:"s"`
	CodeChallenge     string `json:"cc,omitempty"`
}

// login initiates the OAuth flow by redirecting to Google.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	clientRedirectURI := r.URL.Query().Get("redirect_uri")

	// Validate redirect_uri against the configured redirect URL origin to
	// prevent open-redirect attacks.
	if clientRedirectURI != "" {
		if err := ValidateRedirectURI(h.cfg.RedirectURL, clientRedirectURI); err != nil {
			http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
			return
		}
	}

	// If a client_id is provided (dynamically registered client), verify it
	// exists in the registry and that its stored redirect URI matches.
	// Clients that omit client_id are allowed through for backwards compatibility
	// (pre-registration flows and direct token use); the redirect_uri check above
	// is still the primary open-redirect guard for those callers.
	if clientID := r.URL.Query().Get("client_id"); clientID != "" {
		registered, err := h.db.GetClient(ctx, clientID)
		if errors.Is(err, db.ErrNotFound) {
			http.Error(w, "invalid client_id", http.StatusUnauthorized)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if clientRedirectURI != "" && registered.RedirectURI != clientRedirectURI {
			http.Error(w, "redirect_uri mismatch", http.StatusBadRequest)
			return
		}
	}

	// PKCE (RFC 7636): if code_challenge is present, method must be S256.
	codeChallenge := r.URL.Query().Get("code_challenge")
	if codeChallenge != "" {
		if r.URL.Query().Get("code_challenge_method") != "S256" {
			http.Error(w, "unsupported code_challenge_method", http.StatusBadRequest)
			return
		}
	}

	nonce, err := generateRandomCode()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Encode state payload as base64 JSON to avoid delimiter fragility.
	payload := oauthStatePayload{
		Nonce:             nonce,
		ClientRedirectURI: clientRedirectURI,
		ClientState:       r.URL.Query().Get("state"),
		CodeChallenge:     codeChallenge,
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

	clientRedirectURI := payload.ClientRedirectURI
	if clientRedirectURI == "" {
		// JSON fallback — intended for CLI/testing flows where there is no redirect URI.
		// The token is returned directly in the response body and never appears in a URL,
		// so the security properties differ from the code-exchange path only in that the
		// caller must protect the response body rather than the redirect. This path should
		// not be used for browser-based clients.
		// Issue the access token directly; it never touches a URL here.
		accessToken, err := IssueAccessToken(h.cfg.TokenSecret, info.Sub)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   int(accessTokenTTL.Seconds()),
		})
		return
	}

	// Issue a short-lived opaque exchange code. The MCP client redeems it via
	// POST /auth/token — the bearer token never appears in a URL or log.
	// Retry once on the astronomically unlikely collision (128-bit random key space).
	var exchangeCode string
	for range 2 {
		var code string
		code, err = generateRandomCode()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		codeChallengeMethod := ""
		if payload.CodeChallenge != "" {
			codeChallengeMethod = "S256"
		}
		if saveErr := h.db.SaveAuthCode(ctx, code, info.Sub, clientRedirectURI, payload.CodeChallenge, codeChallengeMethod, authCodeTTL); saveErr == nil {
			exchangeCode = code
			break
		} else if !errors.Is(saveErr, db.ErrAuthCodeCollision) {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	if exchangeCode == "" {
		log.Printf("auth: exchange code collision exhausted retries for user %s", info.Sub)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	sep := "?"
	if strings.Contains(clientRedirectURI, "?") {
		sep = "&"
	}
	target := clientRedirectURI + sep + "code=" + url.QueryEscape(exchangeCode)
	if payload.ClientState != "" {
		target += "&state=" + url.QueryEscape(payload.ClientState)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// ValidateRedirectURI ensures the client redirect URI is one of the two
// allowed forms (RFC 6749 §3.1.2):
//
//  1. Exact match against the server's configured redirect URL (scheme, host,
//     path, no query string). path.Clean neutralises traversal sequences.
//
//  2. http://127.0.0.1:<port>/callback — the loopback redirect form used by
//     native/CLI OAuth clients (RFC 8252 §7.3). Any port is accepted; the path
//     must be exactly "/callback". This lets MCP clients (e.g. Claude Code with
//     --callback-port) register a fixed localhost port without needing a separate
//     configured redirect URL for each client.
//
// Query strings are rejected on the client URI because they are not part of a
// valid callback URL and could be used to smuggle state via an open redirector.
func ValidateRedirectURI(configuredRedirectURL, clientRedirectURI string) error {
	client, err := url.Parse(clientRedirectURI)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri: %w", err)
	}
	if client.RawQuery != "" {
		return fmt.Errorf("redirect_uri must not contain a query string")
	}

	// Loopback form: http://127.0.0.1:<port>/callback
	if client.Scheme == "http" && client.Hostname() == "127.0.0.1" && path.Clean(client.Path) == "/callback" {
		if client.Port() == "" {
			return fmt.Errorf("redirect_uri loopback form requires an explicit port")
		}
		return nil
	}

	// Exact match against the configured redirect URL.
	configured, err := url.Parse(configuredRedirectURL)
	if err != nil {
		return fmt.Errorf("invalid configured redirect URL: %w", err)
	}
	if configured.Scheme != client.Scheme ||
		configured.Host != client.Host ||
		path.Clean(configured.Path) != path.Clean(client.Path) {
		return fmt.Errorf("redirect_uri %q not allowed", clientRedirectURI)
	}
	return nil
}

const (
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	authCodeTTL       = 60 * time.Second
)

// writeJSONError writes an RFC 6749-style JSON error response.
func writeJSONError(w http.ResponseWriter, status int, errCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode})
}

// registerRequest is a subset of the RFC 7591 §2 dynamic client registration request body.
type registerRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// registerResponse is the RFC 7591 §3.2.1 successful registration response.
type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// register handles POST /register — RFC 7591 dynamic client registration.
// Public clients (MCP CLI tools) register a loopback redirect URI and receive
// a client_id they use in subsequent authorization requests. No client secret
// is issued because Claude Code uses PKCE (public client flow).
//
// Rate limiting: this endpoint is unauthenticated. Abuse (flooding registrations)
// is mitigated by the loopback-only redirect URI policy — invalid URIs are
// rejected before any DynamoDB write. CloudFront/WAF provides the IP-level
// rate limit layer; no in-process limiting is applied.
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limited := &io.LimitedReader{R: r.Body, N: 64 * 1024}
	var req registerRequest
	if err := json.NewDecoder(limited).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}

	if len(req.RedirectURIs) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_redirect_uri")
		return
	}
	// Only one redirect URI is supported per registration. Accepting multiple
	// while only persisting one would create a mismatch at /auth/login.
	if len(req.RedirectURIs) > 1 {
		writeJSONError(w, http.StatusBadRequest, "invalid_redirect_uri")
		return
	}

	if err := ValidateRedirectURI(h.cfg.RedirectURL, req.RedirectURIs[0]); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_redirect_uri")
		return
	}

	// Only "none" is supported (public client / PKCE flow). Reject other methods
	// rather than echo them back, which would imply support we don't provide.
	if req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none" {
		writeJSONError(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}

	clientID, err := generateRandomCode()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := h.db.SaveClient(ctx, clientID, req.RedirectURIs[0], req.ClientName); err != nil {
		log.Printf("register: save client: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := registerResponse{
		ClientID:                clientID,
		ClientIDIssuedAt:        time.Now().Unix(),
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: "none",
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// token handles POST /auth/token — the RFC 6749 §4.1.3 token endpoint.
// The MCP client exchanges the short-lived opaque code from the callback
// redirect for an actual bearer token. The code is single-use: it is deleted
// atomically on redemption.
//
// client_id validation is not required for public clients (CLI/MCP flows)
// per RFC 6749 §3.2.1.
func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if r.FormValue("grant_type") != "authorization_code" {
		writeJSONError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	code := r.FormValue("code")
	if code == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	redeemed, err := h.db.RedeemAuthCode(ctx, code)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	// Defensively reject codes with no stored redirect_uri. By construction the
	// callback handler only calls SaveAuthCode after the early-return JSON path
	// (which handles the no-redirect_uri case inline), so RedirectURI is always
	// non-empty here. Treat a missing value as a corrupted/invalid code rather
	// than silently accepting any redirect_uri the client presents.
	if redeemed.RedirectURI == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	// RFC 6749 §4.1.3: the redirect_uri presented at the token endpoint must
	// exactly match the value stored when the auth code was issued.
	if redeemed.RedirectURI != r.FormValue("redirect_uri") {
		writeJSONError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	// RFC 7636: if the auth code was bound to a PKCE challenge, the verifier
	// must be present and must produce the stored challenge when hashed.
	if redeemed.CodeChallenge != "" {
		verifier := r.FormValue("code_verifier")
		if verifier == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		if !verifyS256(verifier, redeemed.CodeChallenge) {
			writeJSONError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
	}

	accessToken, err := IssueAccessToken(h.cfg.TokenSecret, redeemed.UserID)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(accessTokenTTL.Seconds()),
	})
}

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
	// Limit response size — Google's userinfo payload is tiny but defense-in-depth.
	// io.LimitedReader is used rather than http.MaxBytesReader to avoid passing
	// a nil ResponseWriter (undocumented behaviour).
	limited := &io.LimitedReader{R: resp.Body, N: 64 * 1024}
	var info googleUserInfo
	if err := json.NewDecoder(limited).Decode(&info); err != nil {
		return nil, fmt.Errorf("userinfo decode: %w", err)
	}
	if info.Sub == "" {
		return nil, fmt.Errorf("userinfo missing sub")
	}
	return &info, nil
}

// upsertUser creates or updates the user record and applies the admin bootstrap rule.
// Note: GetUser → SaveUser is a non-atomic read-modify-write. Two concurrent
// first-logins race safely: (a) SaveUser uses if_not_exists(CreatedAt) so
// CreatedAt is never overwritten, (b) both concurrent reads return ErrNotFound,
// both set status=pending (or status=admin for AdminGoogleIDs users), so both
// writes produce the same result. Subsequent logins skip the write entirely when
// status/email/name haven't changed, so they are also idempotent.
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
	// Note: if SaveUser succeeds but SaveRefreshToken fails, the user record exists
	// but has no stored token. On the next login Google won't re-send the token
	// (unless the user re-grants), leaving the server unable to refresh on their
	// behalf. This is an accepted gap — a future improvement would be to use a
	// DynamoDB transaction, or to surface the partial failure to the user.
	if refreshToken != "" {
		if err := h.db.SaveRefreshToken(ctx, info.Sub, refreshToken); err != nil {
			return fmt.Errorf("save refresh token: %w", err)
		}
	}

	return nil
}

// verifyS256 checks that SHA-256(verifier), base64url-encoded without padding,
// equals the stored challenge — the RFC 7636 S256 method.
func verifyS256(verifier, challenge string) bool {
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}
