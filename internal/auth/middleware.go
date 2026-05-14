package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

type contextKey int

const userContextKey contextKey = 1

// UserFromContext returns the authenticated user injected by Middleware, or nil.
func UserFromContext(ctx context.Context) *models.User {
	u, _ := ctx.Value(userContextKey).(*models.User)
	return u
}

// WithUserContext returns a copy of ctx with user injected as the authenticated
// user. Intended for tests that bypass Middleware.
func WithUserContext(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// Middleware wraps an http.Handler, enforcing token authentication on every
// request. It validates the Bearer token, loads the user's current status from
// DynamoDB (never trusting embedded token claims for authorization), and injects
// the user into the request context.
//
// If the token is expired and the user has a stored Google refresh token,
// the token is exchanged live with Google's token endpoint to confirm it is
// still valid. On success a fresh notoriousmcp access token is issued and
// delivered via X-New-Token. If Google rejects the token (revoked/expired),
// the stored token is deleted from DynamoDB and the request gets 401.
// X-New-Token is only written when next responds with a 2xx status code —
// the response is buffered to enforce this. A 401, 403, or 5xx from next
// will not carry the header.
//
// Status rules (evaluated after token authentication):
//   - pending/banned: 403 Forbidden
//   - user/admin: request forwarded with user in context
func Middleware(cfg Config, dbClient *db.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			setWWWAuthenticate(w, r, cfg)
			http.Error(w, "missing authorization token", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()

		userID, newToken, err := resolveToken(ctx, cfg, dbClient, token)
		if err != nil {
			setWWWAuthenticate(w, r, cfg)
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		user, err := dbClient.GetUser(ctx, userID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			log.Printf("middleware: get user: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if user.Status == models.StatusPending || user.Status == models.StatusBanned {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}

		if newToken == "" {
			// Common path: no refresh needed, write directly to the real ResponseWriter.
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userContextKey, user)))
			return
		}

		// Refresh path: buffer the response so we can inspect the status code before
		// deciding whether to include X-New-Token. The header must not appear on
		// non-2xx responses from next (e.g. 404, 500) — a client that treats its
		// presence as "refresh succeeded" would be misled.
		buf := &responseBuffer{header: make(http.Header)}
		next.ServeHTTP(buf, r.WithContext(context.WithValue(ctx, userContextKey, user)))
		// Copy buffered headers first (direct slice assignment preserves multi-value
		// headers), then write X-New-Token so next cannot accidentally overwrite it.
		for k, vs := range buf.header {
			w.Header()[k] = vs
		}
		if buf.status == 0 {
			buf.status = http.StatusOK
		}
		if buf.status >= 200 && buf.status < 300 {
			w.Header().Set("X-New-Token", newToken)
		}
		w.WriteHeader(buf.status)
		_, _ = w.Write(buf.body.Bytes())
	})
}

// resolveToken validates the bearer token and returns the userID. If the token
// fails validation with ErrInvalidToken (covers both expired and structurally-bad
// tokens — there is no separate ErrExpiredToken sentinel), the refresh path is
// attempted optimistically; validSignatureUserID re-verifies the HMAC first so
// forged tokens are rejected before the DB is touched. Non-ErrInvalidToken errors
// (e.g. an internal crypto failure) are propagated directly without attempting
// a refresh.
func resolveToken(ctx context.Context, cfg Config, dbClient *db.Client, token string) (userID, newToken string, err error) {
	userID, err = ValidateAccessToken(cfg.TokenSecret, token)
	if err == nil {
		return userID, "", nil
	}
	if !errors.Is(err, ErrInvalidToken) {
		return "", "", err
	}

	newToken, userID, err = tryRefresh(ctx, cfg, dbClient, token)
	return userID, newToken, err
}

// bearerToken extracts the token value from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || after == "" {
		return ""
	}
	return after
}

// tryRefresh issues a new notoriousmcp access token for the user embedded in
// rawToken, provided the token's HMAC signature is valid (expiry is not checked)
// and Google accepts the stored refresh token. If Google rejects the token
// (revoked or expired), the stored token is deleted from DynamoDB and the
// refresh fails with ErrInvalidToken so the middleware returns 401.
func tryRefresh(ctx context.Context, cfg Config, dbClient *db.Client, rawToken string) (newToken, userID string, err error) {
	userID, err = validSignatureUserID(cfg.TokenSecret, rawToken)
	if err != nil {
		return "", "", err
	}

	storedToken, err := dbClient.LoadRefreshToken(ctx, userID)
	if err != nil {
		return "", "", err
	}

	rotatedToken, err := exchangeGoogleRefreshToken(ctx, cfg, storedToken)
	if err != nil {
		// Token was revoked or otherwise rejected by Google — remove it so future
		// refresh attempts fail fast without hitting Google unnecessarily.
		if delErr := dbClient.DeleteRefreshToken(ctx, userID); delErr != nil {
			log.Printf("auth: delete revoked refresh token for %s: %v", userID, delErr)
		}
		return "", "", ErrInvalidToken
	}

	// Google rotates refresh tokens for inactive users. Persist the new token so
	// the stored value doesn't silently go stale.
	if rotatedToken != "" && rotatedToken != storedToken {
		if saveErr := dbClient.SaveRefreshToken(ctx, userID, rotatedToken); saveErr != nil {
			log.Printf("auth: save rotated refresh token for %s: %v", userID, saveErr)
		}
	}

	newToken, err = IssueAccessToken(cfg.TokenSecret, userID)
	if err != nil {
		return "", "", err
	}
	return newToken, userID, nil
}

// exchangeGoogleRefreshToken performs a live token exchange with Google to
// confirm the stored refresh token is still valid. It returns the new refresh
// token (non-empty only when Google rotates it) and a non-nil error if Google
// rejects the token.
func exchangeGoogleRefreshToken(ctx context.Context, cfg Config, refreshToken string) (newRefreshToken string, err error) {
	endpoint := google.Endpoint
	if cfg.GoogleTokenURL != "" {
		endpoint = oauth2.Endpoint{TokenURL: cfg.GoogleTokenURL}
	}
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     endpoint,
	}
	src := oauthCfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return "", err
	}
	return tok.RefreshToken, nil
}

// responseBuffer is a minimal http.ResponseWriter that captures the response
// from a downstream handler so the middleware can inspect the status code before
// flushing to the real writer.
//
// Limitations: it implements http.Flusher as a no-op (streaming/SSE handlers
// will receive the full body only after completion, not incrementally) and does
// not implement http.Hijacker (WebSocket or HTTP/1.1 upgrade handlers will panic
// on the interface assertion). Neither is a concern for this REST-only server, but
// if either is added in future, the refresh path will need a different approach.
type responseBuffer struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (rb *responseBuffer) Header() http.Header { return rb.header }

func (rb *responseBuffer) WriteHeader(code int) {
	if rb.status == 0 {
		rb.status = code
	}
}

func (rb *responseBuffer) Write(b []byte) (int, error) {
	if rb.status == 0 {
		rb.status = http.StatusOK
	}
	return rb.body.Write(b)
}

func (rb *responseBuffer) Flush() {}

// validSignatureUserID parses a token and returns the userID if and only if the
// HMAC signature is correct — expiry is intentionally not checked. This is used
// exclusively by tryRefresh to confirm a token is genuinely ours before
// attempting a refresh on behalf of the embedded userID.
func validSignatureUserID(secret []byte, token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ErrInvalidToken
	}
	encoded, sig := parts[0], parts[1]

	expected := sign(secret, encoded)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return "", ErrInvalidToken
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidToken
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ErrInvalidToken
	}
	if claims.UserID == "" {
		return "", ErrInvalidToken
	}
	return claims.UserID, nil
}

// setWWWAuthenticate writes the WWW-Authenticate header required by RFC 9728
// (OAuth 2.0 Protected Resource Metadata). Claude Code's MCP SDK reads the
// resource_metadata URL to discover the authorization server before starting OAuth.
func setWWWAuthenticate(w http.ResponseWriter, r *http.Request, cfg Config) {
	base := cfg.publicBase(r)
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource"`)
}
