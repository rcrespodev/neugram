package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// NormalizedHash is the dedupe key defined in docs/specs/data-model.md.
func NormalizedHash(content string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range strings.ToLower(content) {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
