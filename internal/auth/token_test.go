package auth_test

import (
	"testing"

	"github.com/pwntato/notoriousmcp/internal/auth"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	secret := []byte("test-secret-key")
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
	token, _ := auth.IssueAccessToken([]byte("secret-a"), "user-1")
	_, err := auth.ValidateAccessToken([]byte("secret-b"), token)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestAccessTokenTampered(t *testing.T) {
	secret := []byte("test-secret")
	token, _ := auth.IssueAccessToken(secret, "user-1")
	// Replace last 4 chars of signature with garbage.
	tampered := token[:len(token)-4] + "XXXX"
	_, err := auth.ValidateAccessToken(secret, tampered)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for tampered token, got %v", err)
	}
}

func TestAccessTokenMalformed(t *testing.T) {
	secret := []byte("test-secret")
	cases := []string{
		"",          // empty
		"nodot",     // no separator
		".",         // empty payload and sig
		"validbase64AAAA.", // valid base64 payload, empty sig
	}
	for _, bad := range cases {
		_, err := auth.ValidateAccessToken(secret, bad)
		if err != auth.ErrInvalidToken {
			t.Errorf("input %q: expected ErrInvalidToken, got %v", bad, err)
		}
	}
}

func TestValidateRedirectURI(t *testing.T) {
	cases := []struct {
		configured string
		client     string
		wantErr    bool
	}{
		{"https://notoriousmcp.com/auth/callback", "https://notoriousmcp.com/auth/callback", false},
		{"https://notoriousmcp.com/auth/callback", "https://notoriousmcp.com/other", false}, // same origin, different path
		{"https://notoriousmcp.com/auth/callback", "https://evil.com/steal", true},
		{"https://notoriousmcp.com/auth/callback", "http://notoriousmcp.com/auth/callback", true}, // scheme mismatch
	}
	for _, tc := range cases {
		err := auth.ValidateRedirectURI(tc.configured, tc.client)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateRedirectURI(%q, %q): err=%v wantErr=%v", tc.configured, tc.client, err, tc.wantErr)
		}
	}
}
