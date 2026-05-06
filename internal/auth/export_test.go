package auth

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// IssueExpiredToken issues a token that is already expired, for testing only.
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
