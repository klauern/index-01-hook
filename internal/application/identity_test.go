package application_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestNewItemIdentity validates canonical values, rejection precedence, and safe failure metadata.
func TestNewItemIdentity(t *testing.T) {
	hash := strings.Repeat("a", 64)
	cases := []struct {
		name, hash string
		index      int
		field      application.IdentityField
	}{
		{name: "canonical", hash: hash, index: 0},
		{name: "uppercase and whitespace", hash: " \t" + strings.ToUpper(hash) + "\n", index: 9},
		{name: "legacy index above extraction bound", hash: hash, index: 100},
		{name: "empty fingerprint", field: application.IdentityFingerprint},
		{name: "short fingerprint", hash: hash[:62], field: application.IdentityFingerprint},
		{name: "long fingerprint", hash: hash + "aa", field: application.IdentityFingerprint},
		{name: "nonhex", hash: "private-synthetic-payload", field: application.IdentityFingerprint},
		{name: "negative index", hash: hash, index: -1, field: application.IdentityIndex},
		{name: "fingerprint first", hash: "private-synthetic-payload", index: -1, field: application.IdentityFingerprint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := application.NewItemIdentity(tc.hash, tc.index)
			if tc.field == "" {
				if err != nil || identity.Fingerprint != application.Fingerprint(hash) || identity.Index != tc.index {
					t.Fatalf("identity=%+v err=%v", identity, err)
				}
				return
			}
			var validation *application.IdentityError
			if !errors.As(err, &validation) || validation.Field != tc.field || identity != (application.ItemIdentity{}) {
				t.Fatalf("failure=%+v identity=%+v", err, identity)
			}
			if strings.Contains(err.Error(), "private-synthetic-payload") {
				t.Fatal("diagnostic exposed input")
			}
		})
	}
}
