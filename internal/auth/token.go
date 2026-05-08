package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const accessTokenTTL = 1 * time.Hour

var ErrInvalidToken = errors.New("invalid token")

type tokenClaims struct {
	UserID    string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
}

// IssueAccessToken creates a signed access token for the given user.
func IssueAccessToken(secret []byte, userID string) (string, error) {
	claims := tokenClaims{
		UserID:    userID,
		ExpiresAt: time.Now().Add(accessTokenTTL).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := sign(secret, encoded)
	return encoded + "." + sig, nil
}

// ValidateAccessToken validates the token and returns the user ID if valid.
func ValidateAccessToken(secret []byte, token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ErrInvalidToken
	}
	encoded, sig := parts[0], parts[1]

	// Use hmac.Equal for timing-safe comparison to resist timing attacks.
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
	if time.Now().Unix() > claims.ExpiresAt {
		return "", ErrInvalidToken
	}
	return claims.UserID, nil
}

func sign(secret []byte, data string) string {
	mac := hmac.New(sha256.New, secret)
	// hmac.Hash.Write never returns an error for in-memory operations.
	_, _ = mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// generateRandomCode returns a cryptographically random URL-safe string.
func generateRandomCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
