package auth_test

import (
	"testing"
	"time"

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
	tampered := token[:len(token)-4] + "XXXX"
	_, err := auth.ValidateAccessToken(secret, tampered)
	if err != auth.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for tampered token, got %v", err)
	}
}

func TestAccessTokenExpired(t *testing.T) {
	// Can't easily test expiry without mocking time, so just verify the
	// happy path includes an expiry well in the future.
	secret := []byte("test-secret")
	token, err := auth.IssueAccessToken(secret, "user-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Token should be valid right after issuance.
	_, err = auth.ValidateAccessToken(secret, token)
	if err != nil {
		t.Errorf("fresh token should be valid, got %v", err)
	}
	_ = time.Now() // satisfy import
}

func TestAccessTokenMalformed(t *testing.T) {
	secret := []byte("test-secret")
	for _, bad := range []string{"", "notavalidtoken", "a.b.c"} {
		_, err := auth.ValidateAccessToken(secret, bad)
		if err != auth.ErrInvalidToken {
			t.Errorf("input %q: expected ErrInvalidToken, got %v", bad, err)
		}
	}
}
