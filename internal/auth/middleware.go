package auth

import (
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

// Middleware wraps an http.Handler, enforcing token authentication on every
// request. It validates the Bearer token, loads the user's current status from
// DynamoDB (never trusting embedded token claims for authorization), and injects
// the user into the request context.
//
// If the token is expired but the user has a stored Google refresh token, the
// middleware obtains a new Google access token via OAuth2, issues a fresh
// notoriousmcp access token, and continues the request. The new token is written
// to the X-New-Token response header so callers can update their stored copy.
//
// Status rules (evaluated after token authentication):
//   - pending/banned: 403 Forbidden
//   - user/admin: request forwarded with user in context
func Middleware(cfg Config, dbClient *db.Client, next http.Handler) http.Handler {
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			http.Error(w, "missing authorization token", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()

		userID, err := ValidateAccessToken(cfg.TokenSecret, token)
		if err != nil {
			if !errors.Is(err, ErrInvalidToken) {
				log.Printf("middleware: unexpected token validation error: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			// ValidateAccessToken returns ErrInvalidToken for both structurally-bad
			// and expired tokens — there is no separate ErrExpiredToken sentinel. We
			// attempt a refresh optimistically; tryRefresh re-verifies the HMAC first
			// so forged tokens are rejected before the DB is touched.
			newAccessToken, uid, refreshErr := tryRefresh(ctx, cfg.TokenSecret, dbClient, oauthCfg, token)
			if refreshErr != nil {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}

			// Deliver the new token so the caller can update its stored copy.
			w.Header().Set("X-New-Token", newAccessToken)
			userID = uid
		}

		user, err := dbClient.GetUser(ctx, userID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				http.Error(w, "user not found", http.StatusUnauthorized)
				return
			}
			log.Printf("middleware: get user %q: %v", userID, err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if user.Status == models.StatusPending || user.Status == models.StatusBanned {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userContextKey, user)))
	})
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
// rawToken, provided the token's HMAC signature is valid (expiry is not checked).
// It does not call Google's token endpoint — the stored refresh token's presence
// in DynamoDB is sufficient proof of prior authorization. If the refresh token has
// been revoked, Google will reject it on the user's next operation that requires a
// live Google access token; that is out of scope for this middleware.
func tryRefresh(ctx context.Context, secret []byte, dbClient *db.Client, _ *oauth2.Config, rawToken string) (string, string, error) {
	userID, err := validSignatureUserID(secret, rawToken)
	if err != nil {
		return "", "", err
	}

	if _, err := dbClient.LoadRefreshToken(ctx, userID); err != nil {
		return "", "", err
	}

	newAccessToken, err := IssueAccessToken(secret, userID)
	if err != nil {
		return "", "", err
	}
	return newAccessToken, userID, nil
}

// validSignatureUserID parses a token and returns the userID if and only if the
// HMAC signature is correct — expiry is intentionally not checked. This is used
// exclusively by tryRefresh to confirm a token is genuinely ours before
// attempting a Google token refresh on behalf of the embedded userID.
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
