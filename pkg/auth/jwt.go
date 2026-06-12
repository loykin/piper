package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwtClaims struct {
	UserID      string `json:"uid"`
	Email       string `json:"email"`
	SystemAdmin bool   `json:"sa,omitempty"`
	jwt.RegisteredClaims
}

// issueAccessToken signs a short-lived JWT for the given user.
func issueAccessToken(u *User, signingKey []byte, issuer string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := jwtClaims{
		UserID:      u.ID,
		Email:       u.Email,
		SystemAdmin: u.SystemAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   u.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(signingKey)
}

// parseAccessToken validates a JWT and returns the embedded claims.
func parseAccessToken(tokenStr string, signingKey []byte) (*jwtClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwtClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return signingKey, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}
