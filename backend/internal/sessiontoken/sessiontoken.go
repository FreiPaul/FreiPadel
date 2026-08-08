package sessiontoken

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the stable representation used for persisted session tokens.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
