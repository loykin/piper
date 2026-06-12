package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// hashPassword hashes a password with Argon2id and returns a string
// encoding the salt and hash in the format "argon2id$salt$hash".
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(hash)), nil
}

// verifyPassword checks password against the stored hash string.
func verifyPassword(password, encoded string) bool {
	var saltHex, hashHex string
	if _, err := fmt.Sscanf(encoded, "argon2id$%s", &saltHex); err != nil {
		return false
	}
	// Parse "argon2id$<salt>$<hash>"
	const prefix = "argon2id$"
	if len(encoded) <= len(prefix) {
		return false
	}
	rest := encoded[len(prefix):]
	dollar := -1
	for i, c := range rest {
		if c == '$' {
			dollar = i
			break
		}
	}
	if dollar < 0 {
		return false
	}
	saltHex = rest[:dollar]
	hashHex = rest[dollar+1:]

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	if len(actual) != len(expected) {
		return false
	}
	// Constant-time comparison.
	diff := byte(0)
	for i := range actual {
		diff |= actual[i] ^ expected[i]
	}
	return diff == 0
}

// hashToken returns a SHA-256 hex digest of the token — used for storing refresh tokens.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// randomToken generates a cryptographically random hex string of length bytes*2.
func randomToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
