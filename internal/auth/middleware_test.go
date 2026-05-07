package auth_test

import (
	"context"
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

	for _, bad := range []string{"garbage", "Bearer ", "Basic abc"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", bad)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("input %q: got %d want 401", bad, w.Code)
		}
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

	h := auth.Middleware(testMiddlewareCfg(), dbClient, http.HandlerFunc(okHandler))
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
