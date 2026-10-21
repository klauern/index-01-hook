package application

import (
	"encoding/hex"
	"strings"
)

// IdentityField identifies invalid application input without retaining its value.
type IdentityField string

const (
	IdentityFingerprint IdentityField = "fingerprint"
	IdentityIndex       IdentityField = "index"
)

// IdentityError reports safe validation metadata without provider or payload details.
type IdentityError struct{ Field IdentityField }

// Error returns the existing safe diagnostic for the invalid identity field.
func (e *IdentityError) Error() string {
	switch e.Field {
	case IdentityFingerprint:
		return "recording hash is invalid"
	case IdentityIndex:
		return "task index is invalid"
	default:
		return "item identity is invalid"
	}
}

// NewItemIdentity canonicalizes a payload fingerprint and validates its frozen zero-based index.
// Identity never depends on routing, remote project IDs, editable content, or item kind.
func NewItemIdentity(fingerprint string, index int) (ItemIdentity, error) {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != 32 {
		return ItemIdentity{}, &IdentityError{Field: IdentityFingerprint}
	}
	if index < 0 {
		return ItemIdentity{}, &IdentityError{Field: IdentityIndex}
	}
	return ItemIdentity{Fingerprint: Fingerprint(fingerprint), Index: index}, nil
}
