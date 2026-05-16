package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/pwntato/notoriousmcp/internal/auth"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

var testSecret = []byte("test-secret-key-at-least-32-bytes!!")

func testMiddlewareCfg() auth.Config {
	return auth.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL:  "https://example.com/auth/callback",
		TokenSecret:  testSecret,
	}
}

func testMiddlewareCfgWithTokenURL(tokenURL string) auth.Config {
	cfg := testMiddlewareCfg()
	cfg.OverrideTokenURL = tokenURL
	return cfg
}

// okHandler is a sentinel next handler that writes 200 and echoes the user ID
// from context, so tests can confirm the user was injected correctly.
func okHandler(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u != nil {
		w.Header().Set("X-User-ID", u.UserID)
	}
	w.WriteHeader(http.StatusOK)
}

// ---- pure unit tests (no DB required) ----

func TestMiddlewareMissingToken(t *testing.T) {
	h := auth.Middleware(testMiddlewareCfg(), nil, http.HandlerFunc(okHandler))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", w.Code)
	}
}

func TestMiddlewareInvalidToken(t *testing.T) {
	h := auth.Middleware(testMiddlewareCfg(), nil, http.HandlerFunc(okHandler))

	for _, bad := range []string{"garbage", "Bearer ", "Basic abc", "Bearer \t"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", bad)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("input %q: got %d want 401", bad, w.Code)
		}
	}
}

func TestMiddlewareWWWAuthenticateOnMissingToken(t *testing.T) {
	cfg := testMiddlewareCfg()
	cfg.PublicBaseURL = "https://example.com"
	h := auth.Middleware(cfg, nil, http.HandlerFunc(okHandler))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", w.Code)
	}
	got := w.Header().Get("WWW-Authenticate")
	want := `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`
	if got != want {
		t.Errorf("WWW-Authenticate: got %q want %q", got, want)
	}
}

func TestMiddlewareWWWAuthenticateOnBadToken(t *testing.T) {
	cfg := testMiddlewareCfg()
	cfg.PublicBaseURL = "https://example.com"
	h := auth.Middleware(cfg, nil, http.HandlerFunc(okHandler))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", w.Code)
	}
	got := w.Header().Get("WWW-Authenticate")
	want := `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`
	if got != want {
		t.Errorf("WWW-Authenticate: got %q want %q", got, want)
	}
}

func TestMiddlewareTamperedToken(t *testing.T) {
	token, err := auth.IssueAccessToken(testSecret, "user-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tampered := token[:len(token)-4] + "XXXX"

	h := auth.Middleware(testMiddlewareCfg(), nil, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("tampered token: got %d want 401", w.Code)
	}
}

func TestValidSignatureUserID(t *testing.T) {
	secret := testSecret
	userID := "google-sub-xyz"

	// Valid token should return the user ID.
	token, err := auth.IssueAccessToken(secret, userID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := auth.ValidSignatureUserID(secret, token)
	if err != nil {
		t.Errorf("valid token: unexpected error: %v", err)
	}
	if got != userID {
		t.Errorf("userID: got %q want %q", got, userID)
	}

	// Expired token with valid signature should still return the user ID.
	expired, err := auth.IssueExpiredToken(secret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	got, err = auth.ValidSignatureUserID(secret, expired)
	if err != nil {
		t.Errorf("expired token: unexpected error: %v", err)
	}
	if got != userID {
		t.Errorf("expired token userID: got %q want %q", got, userID)
	}

	// Tampered signature should be rejected.
	tampered := expired[:len(expired)-4] + "XXXX"
	_, err = auth.ValidSignatureUserID(secret, tampered)
	if err != auth.ErrInvalidToken {
		t.Errorf("tampered: expected ErrInvalidToken, got %v", err)
	}

	// Malformed tokens should be rejected.
	for _, bad := range []string{"", "nodot", ".", "a.", ".b"} {
		_, err = auth.ValidSignatureUserID(secret, bad)
		if err != auth.ErrInvalidToken {
			t.Errorf("malformed %q: expected ErrInvalidToken, got %v", bad, err)
		}
	}
}

func TestUserFromContextNil(t *testing.T) {
	u := auth.UserFromContext(context.Background())
	if u != nil {
		t.Errorf("empty context: expected nil user, got %+v", u)
	}
}

// fakeGoogleTokenServer starts an httptest server that simulates Google's token
// endpoint. When accept is true it returns a well-formed token response;
// otherwise it returns 400 with an "invalid_grant" error body (revoked token).
// The caller must call server.Close() when done.
func fakeGoogleTokenServer(t *testing.T, accept bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accept {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "google-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "google-refresh-token",
		})
	}))
}

// ---- integration tests (require DYNAMODB_ENDPOINT) ----

func newTestDBClient(t *testing.T) *db.Client {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; skipping integration test")
	}
	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		tableName = "notoriousmcp"
	}
	c, err := db.New(context.Background(), tableName, endpoint)
	if err != nil {
		t.Fatalf("new db client: %v", err)
	}
	return c
}

func randUID() string { return fmt.Sprintf("%x", rand.Uint64()) }

func saveTestUser(t *testing.T, c *db.Client, userID string, status models.UserStatus) *models.User {
	t.Helper()
	u := &models.User{
		UserID:    userID,
		Email:     userID + "@example.com",
		Name:      "Test User " + userID,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := c.SaveUser(context.Background(), u); err != nil {
		t.Fatalf("save test user: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteUser(context.Background(), userID) })
	return u
}

func TestMiddlewareValidToken(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)

	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", w.Code)
	}
	if got := w.Header().Get("X-User-ID"); got != userID {
		t.Errorf("X-User-ID: got %q want %q", got, userID)
	}
	if got := w.Header().Get("X-New-Token"); got != "" {
		t.Errorf("X-New-Token should be absent on non-refresh request, got %q", got)
	}
}

func TestMiddlewareAdminUser(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusAdmin)

	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("admin user: got %d want 200", w.Code)
	}
}

func TestMiddlewarePendingUserForbidden(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusPending)

	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("pending user: got %d want 403", w.Code)
	}
}

func TestMiddlewareBannedUserForbidden(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusBanned)

	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("banned user: got %d want 403", w.Code)
	}
}

func TestMiddlewareUserNotInDB(t *testing.T) {
	dbClient := newTestDBClient(t)
	// Issue a valid token for a user that was never saved to DynamoDB.
	userID := "ghost-" + randUID()
	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("ghost user: got %d want 401", w.Code)
	}
}

func TestMiddlewareDBLoadsCurrentStatus(t *testing.T) {
	// Verify that the middleware reads the user's current DB status, not any
	// status embedded in the token. Issue a token when the user is StatusUser,
	// then downgrade them to StatusBanned — the middleware must deny the request.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)

	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	// Downgrade status after issuing the token.
	if err := dbClient.UpdateUserStatus(context.Background(), userID, models.StatusBanned); err != nil {
		t.Fatalf("update status: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("downgraded user: got %d want 403", w.Code)
	}
}

func TestMiddlewareExpiredTokenRefreshSuccess(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	if err := dbClient.SaveRefreshToken(context.Background(), userID, "google-refresh-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	googleSrv := fakeGoogleTokenServer(t, true)
	defer googleSrv.Close()

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expired + refresh token: got %d want 200", w.Code)
	}
	newToken := w.Header().Get("X-New-Token")
	if newToken == "" {
		t.Error("X-New-Token header not set")
	}
	if newToken == expired {
		t.Error("X-New-Token should be a new token, not the expired one")
	}
	// The new token must be valid and identify the same user.
	gotUserID, err := auth.ValidateAccessToken(testSecret, newToken)
	if err != nil {
		t.Errorf("X-New-Token failed validation: %v", err)
	}
	if gotUserID != userID {
		t.Errorf("X-New-Token userID: got %q want %q", gotUserID, userID)
	}
	if got := w.Header().Get("X-User-ID"); got != userID {
		t.Errorf("user in context: got %q want %q", got, userID)
	}
}

func TestMiddlewareXNewTokenAbsentOnNextError(t *testing.T) {
	// X-New-Token must not appear when next returns a non-2xx status, even when
	// token refresh succeeded. Otherwise a client that treats its presence as
	// "session renewed" would be misled by an upstream 404 or 500.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	if err := dbClient.SaveRefreshToken(context.Background(), userID, "google-refresh-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	googleSrv := fakeGoogleTokenServer(t, true)
	defer googleSrv.Close()

	errorHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, errorHandler)
	req := httptest.NewRequest("GET", "/missing", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", w.Code)
	}
	if got := w.Header().Get("X-New-Token"); got != "" {
		t.Errorf("X-New-Token must be absent when next returns non-2xx, got %q", got)
	}
}

func TestMiddlewareExpiredTokenNoRefreshToken(t *testing.T) {
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	// No refresh token stored — middleware must reject the expired token.

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired, no refresh token: got %d want 401", w.Code)
	}
}

func TestMiddlewareExpiredTokenRefreshBannedUser(t *testing.T) {
	// A banned user whose token is expired but has a stored refresh token must
	// get 403, not a new token — X-New-Token must not be present on the response.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusBanned)
	if err := dbClient.SaveRefreshToken(context.Background(), userID, "google-refresh-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	googleSrv := fakeGoogleTokenServer(t, true)
	defer googleSrv.Close()

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("banned+expired+refresh: got %d want 403", w.Code)
	}
	if got := w.Header().Get("X-New-Token"); got != "" {
		t.Errorf("X-New-Token must not be set on 403 response, got %q", got)
	}
}

func TestMiddlewareRefreshTokenRotation(t *testing.T) {
	// When Google returns a new refresh token during exchange, the middleware must
	// persist it so the stored DynamoDB value stays current.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	if err := dbClient.SaveRefreshToken(context.Background(), userID, "old-google-refresh-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	// Fake Google server returns a *different* refresh token, simulating rotation.
	rotatedToken := "rotated-google-refresh-token"
	googleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "google-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": rotatedToken,
		})
	}))
	defer googleSrv.Close()

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}

	// The rotated token must now be persisted in DynamoDB.
	stored, err := dbClient.LoadRefreshToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("load refresh token after rotation: %v", err)
	}
	if stored != rotatedToken {
		t.Errorf("stored refresh token: got %q want %q", stored, rotatedToken)
	}
}

func TestMiddlewareRefreshTokenNoRotation(t *testing.T) {
	// When Google does not rotate the refresh token (empty refresh_token in response),
	// the stored DynamoDB token must be unchanged — the rotatedToken != "" guard.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	original := "original-google-refresh-token"
	if err := dbClient.SaveRefreshToken(context.Background(), userID, original); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	// Fake Google server returns no refresh_token field (omitted = empty string in oauth2 lib).
	googleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "google-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			// refresh_token intentionally omitted
		})
	}))
	defer googleSrv.Close()

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}

	// Stored token must be unchanged — no rotation occurred.
	stored, err := dbClient.LoadRefreshToken(context.Background(), userID)
	if err != nil {
		t.Fatalf("load refresh token: %v", err)
	}
	if stored != original {
		t.Errorf("stored refresh token changed without rotation: got %q want %q", stored, original)
	}
}

func TestMiddlewareExpiredTokenRevokedRefreshToken(t *testing.T) {
	// When Google rejects the stored refresh token (revoked), the middleware must
	// return 401 and delete the token from DynamoDB so future attempts fail fast.
	dbClient := newTestDBClient(t)
	userID := randUID()
	saveTestUser(t, dbClient, userID, models.StatusUser)
	if err := dbClient.SaveRefreshToken(context.Background(), userID, "revoked-google-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}

	expired, err := auth.IssueExpiredToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	googleSrv := fakeGoogleTokenServer(t, false) // Google returns 400 invalid_grant
	defer googleSrv.Close()

	h := auth.Middleware(testMiddlewareCfgWithTokenURL(googleSrv.URL), dbClient, http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: got %d want 401", w.Code)
	}
	if got := w.Header().Get("X-New-Token"); got != "" {
		t.Errorf("X-New-Token must not be set on revoked refresh, got %q", got)
	}

	// Confirm the stored token was deleted — a second request must also fail with
	// ErrNoRefreshToken rather than hitting Google again.
	_, loadErr := dbClient.LoadRefreshToken(context.Background(), userID)
	if loadErr == nil {
		t.Error("revoked token should have been deleted from DynamoDB")
	}
}
