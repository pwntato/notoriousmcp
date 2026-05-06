package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pwntato/notoriousmcp/internal/auth"
)

func newTestHandler(t *testing.T) *auth.Handler {
	t.Helper()
	cfg := auth.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL:  "https://example.com/auth/callback",
		TokenSecret:  []byte("test-secret-key-at-least-32-bytes!!"),
	}
	// Handler requires a db.Client; pass nil — the handler tests don't hit DB paths.
	return auth.New(cfg, nil)
}

func TestWellKnown(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q want application/json", ct)
	}

	var meta map[string]any
	if err := json.NewDecoder(w.Body).Decode(&meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta["issuer"] != "http://example.com" {
		t.Errorf("issuer: got %v", meta["issuer"])
	}
	if meta["authorization_endpoint"] != "http://example.com/auth/login" {
		t.Errorf("authorization_endpoint: got %v", meta["authorization_endpoint"])
	}
	// token_endpoint should not be present until implemented.
	if _, ok := meta["token_endpoint"]; ok {
		t.Error("token_endpoint should not be advertised until implemented")
	}
}

func TestLoginSetsNonceCookieAndRedirects(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/auth/login?redirect_uri=https://example.com/auth/callback&state=client-state-xyz", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status: got %d want 302", w.Code)
	}

	// Check nonce cookie is set.
	var nonceCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "oauth_nonce" {
			nonceCookie = c
		}
	}
	if nonceCookie == nil {
		t.Fatal("oauth_nonce cookie not set")
	}
	if !nonceCookie.HttpOnly {
		t.Error("oauth_nonce cookie must be HttpOnly")
	}
	if nonceCookie.MaxAge != 600 {
		t.Errorf("oauth_nonce MaxAge: got %d want 600", nonceCookie.MaxAge)
	}

	// Check redirect goes to Google.
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://accounts.google.com") {
		t.Errorf("redirect: got %q want Google OAuth URL", loc)
	}
}

func TestLoginRejectsInvalidRedirectURI(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/auth/login?redirect_uri=https://evil.com/steal", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 for invalid redirect_uri", w.Code)
	}
}

func TestCallbackMissingCode(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/auth/callback?state=badstate", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should fail at state decode (invalid base64), not panic.
	if w.Code == http.StatusOK {
		t.Error("expected error response for missing/invalid state, got 200")
	}
}

func TestCallbackMissingNonceCookie(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Provide a syntactically valid base64 state but no nonce cookie.
	validState := "eyJuIjoibm9uY2UiLCJyIjoiIiwicyI6IiJ9" // {"n":"nonce","r":"","s":""}
	req := httptest.NewRequest("GET", "/auth/callback?state="+validState, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 for missing nonce cookie", w.Code)
	}
}

func TestCallbackJSONFallback(t *testing.T) {
	// When the state payload has no redirect_uri, callback returns JSON.
	// This test reaches the nonce check (400) before JSON, since we can't
	// complete the Google exchange in a unit test. We verify the handler
	// does not panic and returns a well-formed error response.
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// State with empty redirect_uri ("r":"") and valid nonce structure.
	// {"n":"testnonce","r":"","s":""}
	validState := "eyJuIjoidGVzdG5vbmNlIiwiciI6IiIsInMiOiIifQ"
	req := httptest.NewRequest("GET", "/auth/callback?state="+validState+"&code=fake", nil)
	// Set matching nonce cookie so we get past CSRF — will fail at token exchange.
	req.AddCookie(&http.Cookie{Name: "oauth_nonce", Value: "testnonce"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Token exchange with Google will fail (no real server) — 500 is expected.
	// The important thing is the handler doesn't panic and reaches that point.
	if w.Code == http.StatusOK {
		t.Error("expected non-200 (token exchange should fail without real Google)")
	}
	if w.Code == 0 {
		t.Error("handler produced no response — possible panic")
	}
}

func TestConfigValidate(t *testing.T) {
	base := auth.Config{
		ClientID:     "id",
		ClientSecret: "secret",
		RedirectURL:  "https://example.com/auth/callback",
		TokenSecret:  []byte("exactly-32-bytes-long-secret-key!"),
	}
	if err := base.Validate(); err != nil {
		t.Errorf("valid config failed: %v", err)
	}

	short := base
	short.TokenSecret = []byte("tooshort")
	if err := short.Validate(); err == nil {
		t.Error("expected error for short TokenSecret")
	}

	noSecret := base
	noSecret.ClientSecret = ""
	if err := noSecret.Validate(); err == nil {
		t.Error("expected error for empty ClientSecret")
	}
}
