package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

// IssueExpiredToken issues a token that is already expired, for testing only.
// Must stay in sync with IssueAccessToken in token.go — if the token format
// or sign() function changes, update this helper to match.
func IssueExpiredToken(secret []byte, userID string) (string, error) {
	claims := tokenClaims{
		UserID:    userID,
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + sign(secret, encoded), nil
}

// ValidSignatureUserID exposes validSignatureUserID for testing.
var ValidSignatureUserID = validSignatureUserID

// ProviderEndpoint exposes Config.providerEndpoint for testing.
// Returns (authURL, tokenURL).
func (c Config) ProviderEndpoint() (string, string) {
	ep := c.providerEndpoint()
	return ep.AuthURL, ep.TokenURL
}

// UserInfoURL exposes Config.userInfoURL for testing.
func (c Config) UserInfoURL() string { return c.userInfoURL() }

// UpsertUser exposes upsertUser for whitebox unit tests.
func (h *Handler) UpsertUser(ctx context.Context, sub, email, name, refreshToken string) error {
	return h.upsertUser(ctx, &userInfo{Sub: sub, Email: email, Name: name}, refreshToken)
}
