package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// tokenBytes is the entropy of an opaque registration token.
const tokenBytes = 32

// NewOpaqueToken returns a random token safe to hand to a human. It is hex
// encoded, so it survives query strings, headers and copy and paste.
//
// It stays 32 bytes of crypto/rand even though every other identifier in this
// package is a ULID, because this value is a credential rather than a label:
// it authorises exactly one account creation, and a ULID is a timestamp plus
// monotonic entropy, which is a shape an attacker could walk forwards from the
// clock alone. Only its hash is stored, and only the record it names carries a
// ULID.
func NewOpaqueToken() (string, error) {
	return randomHex(tokenBytes)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashToken returns the hex encoded SHA-256 of a token. Only hashes are
// persisted, so a stolen database cannot be replayed against the gateway.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
