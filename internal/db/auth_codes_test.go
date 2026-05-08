package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pwntato/notoriousmcp/internal/db"
)

func TestAuthCodeRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	code := "testcode-" + uid()
	userID := "user-" + uid()

	if err := c.SaveAuthCode(ctx, code, userID, 60*time.Second); err != nil {
		t.Fatalf("save auth code: %v", err)
	}

	got, err := c.RedeemAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("redeem auth code: %v", err)
	}
	if got != userID {
		t.Errorf("userID: got %q want %q", got, userID)
	}
}

func TestAuthCodeSingleUse(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	code := "testcode-" + uid()
	userID := "user-" + uid()

	if err := c.SaveAuthCode(ctx, code, userID, 60*time.Second); err != nil {
		t.Fatalf("save auth code: %v", err)
	}
	if _, err := c.RedeemAuthCode(ctx, code); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	// Second redeem must fail — code was deleted on first redemption.
	_, err := c.RedeemAuthCode(ctx, code)
	if !errors.Is(err, db.ErrAuthCodeNotFound) {
		t.Errorf("second redeem: got %v want ErrAuthCodeNotFound", err)
	}
}

func TestAuthCodeNotFound(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	_, err := c.RedeemAuthCode(ctx, "no-such-code-"+uid())
	if !errors.Is(err, db.ErrAuthCodeNotFound) {
		t.Errorf("missing code: got %v want ErrAuthCodeNotFound", err)
	}
}

func TestAuthCodeExpired(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	code := "testcode-" + uid()
	userID := "user-" + uid()

	// Save with a TTL already in the past.
	if err := c.SaveAuthCode(ctx, code, userID, -1*time.Second); err != nil {
		t.Fatalf("save expired auth code: %v", err)
	}

	_, err := c.RedeemAuthCode(ctx, code)
	if !errors.Is(err, db.ErrAuthCodeNotFound) {
		t.Errorf("expired code: got %v want ErrAuthCodeNotFound", err)
	}
}
