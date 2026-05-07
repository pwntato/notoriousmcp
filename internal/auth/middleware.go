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
// a fresh notoriousmcp access token is issued and delivered via X-New-Token.
// X-New-Token is only written when next responds with a 2xx status code —
// the response is buffered to enforce this. A 401, 403, or 5xx from next
// will not carry the header.
//
// Note: the stored Google refresh token is not exchanged with Google on every
// request — its presence in DynamoDB is treated as proof of prior authorization.
// If it has been revoked, Google will reject it the next time a live Google API
// call is made. A future PR can add live token validation here if required.
//
// Status rules (evaluated after token authentication):
//   - pending/banned: 403 Forbidden
//   - user/admin: request forwarded with user in context
func Middleware(cfg Config, dbClient *db.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			http.Error(w, "missing authorization token", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()

		userID, newToken, err := resolveToken(ctx, cfg.TokenSecret, dbClient, token)
		if err != nil {
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
func resolveToken(ctx context.Context, secret []byte, dbClient *db.Client, token string) (userID, newToken string, err error) {
	userID, err = ValidateAccessToken(secret, token)
	if err == nil {
		return userID, "", nil
	}
	if !errors.Is(err, ErrInvalidToken) {
		return "", "", err
	}

	newToken, userID, err = tryRefresh(ctx, secret, dbClient, token)
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
// rawToken, provided the token's HMAC signature is valid (expiry is not checked).
// The stored Google refresh token's presence in DynamoDB is used as proof of
// prior authorization without calling Google's token endpoint. A future PR should
// exchange the stored token with Google to detect revocation — tracked in issue #5.
func tryRefresh(ctx context.Context, secret []byte, dbClient *db.Client, rawToken string) (newToken, userID string, err error) {
	userID, err = validSignatureUserID(secret, rawToken)
	if err != nil {
		return "", "", err
	}

	if _, err = dbClient.LoadRefreshToken(ctx, userID); err != nil {
		return "", "", err
	}

	newToken, err = IssueAccessToken(secret, userID)
	if err != nil {
		return "", "", err
	}
	return newToken, userID, nil
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
