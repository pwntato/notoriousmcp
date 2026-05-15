package auth_test

import (
	"testing"

	"github.com/pwntato/notoriousmcp/internal/auth"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	secret := []byte("test-secret-key-at-least-32-bytes!!")
	userID := "google-sub-123"

	token, err := auth.IssueAccessToken(secret, userID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := auth.ValidateAccessToken(secret, token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != userID {
		t.Errorf("userID: got %q want %q", got, userID)
	}
}

func TestAccessTokenWrongSecret(t *testing.T) {
	token, _ := auth.IssueAccessToken([]byte("secret-aaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "user-1")
	_, err := auth.ValidateAccessToken([]byte("secret-bbbbbbbbbbbbbbbbbbbbbbbbbbbb"), token)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestAccessTokenTampered(t *testing.T) {
	secret := []byte("test-secret-key-at-least-32-bytes!!")
	token, _ := auth.IssueAccessToken(secret, "user-1")
	tampered := token[:len(token)-4] + "XXXX"
	_, err := auth.ValidateAccessToken(secret, tampered)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for tampered token, got %v", err)
	}
}

func TestAccessTokenExpired(t *testing.T) {
	secret := []byte("test-secret-key-at-least-32-bytes!!")
	token, err := auth.IssueExpiredToken(secret, "user-1")
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	_, err = auth.ValidateAccessToken(secret, token)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestAccessTokenMalformed(t *testing.T) {
	secret := []byte("test-secret-key-at-least-32-bytes!!")
	cases := []string{
		"",                 // empty
		"nodot",            // no separator
		".",                // empty payload and sig
		"validbase64AAAA.", // non-empty payload, empty sig
	}
	for _, bad := range cases {
		_, err := auth.ValidateAccessToken(secret, bad)
		if err != auth.ErrInvalidToken {
			t.Errorf("input %q: expected ErrInvalidToken, got %v", bad, err)
		}
	}
}

func TestValidateRedirectURI(t *testing.T) {
	configured := "https://notoriousmcp.com/auth/callback"
	cases := []struct {
		name    string
		client  string
		wantErr bool
	}{
		// Exact match — always allowed.
		{"exact match", "https://notoriousmcp.com/auth/callback", false},
		// Different host — rejected.
		{"evil domain", "https://evil.com/steal", true},
		// Scheme mismatch — rejected.
		{"scheme mismatch", "http://notoriousmcp.com/auth/callback", true},
		// Path outside configured prefix — rejected.
		{"wrong path", "https://notoriousmcp.com/other", true},
		// Path traversal attempt — rejected.
		{"path traversal", "https://notoriousmcp.com/auth/callback/../../steal", true},
		// Prefix boundary: /auth/callback-extra must not match /auth/callback.
		{"prefix boundary", "https://notoriousmcp.com/auth/callback-extra", true},
		// Sub-paths also rejected — exact match only (RFC 6749 §3.1.2).
		{"sub-path", "https://notoriousmcp.com/auth/callback/sub", true},
		// Trailing slash normalised by path.Clean — treated as equivalent.
		{"trailing slash", "https://notoriousmcp.com/auth/callback/", false},
		// Query string on client URI — rejected (RFC 6749 requires exact match, no query).
		{"query string", "https://notoriousmcp.com/auth/callback?foo=bar", true},

		// Loopback form (RFC 8252 §7.3) — any port and path, must be http://127.0.0.1.
		{"loopback port 54321", "http://127.0.0.1:54321/callback", false},
		{"loopback port 8080", "http://127.0.0.1:8080/callback", false},
		{"loopback port 1", "http://127.0.0.1:1/callback", false},
		// Claude Code uses /oauth/code/callback — allowed.
		{"loopback claude code path", "http://127.0.0.1:54321/oauth/code/callback", false},
		// Any path is allowed for loopback clients (RFC 8252 §7.3).
		{"loopback any path", "http://127.0.0.1:54321/other", false},
		// Loopback without explicit port — rejected.
		{"loopback no port", "http://127.0.0.1/callback", true},
		// Non-127.0.0.1 loopback addresses — rejected (not in Google's allowlist).
		{"loopback ipv6", "http://[::1]:54321/callback", true},
		{"localhost hostname", "http://localhost:54321/callback", true},
		// HTTPS loopback — rejected (Google only registers http://127.0.0.1).
		{"loopback https", "https://127.0.0.1:54321/callback", true},
		// Loopback with query string — rejected.
		{"loopback query string", "http://127.0.0.1:54321/callback?foo=bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidateRedirectURI(configured, tc.client)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateRedirectURI(%q): err=%v wantErr=%v", tc.client, err, tc.wantErr)
			}
		})
	}
}
