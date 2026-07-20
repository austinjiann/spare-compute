package device

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestIdentityFingerprintRoundTripAndCopiesKeys(t *testing.T) {
	identity, err := GenerateIdentity(bytes.NewReader(make([]byte, ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseID(string(identity.ID()))
	if err != nil || parsed != identity.ID() || len(parsed.Short()) != 8 {
		t.Fatalf("ParseID() = %q, %v", parsed, err)
	}
	privateKey := identity.PrivateKey()
	privateKey[0] ^= 0xff
	if err := identity.Validate(); err != nil {
		t.Fatalf("caller mutated identity through key copy: %v", err)
	}
}

func TestParseIDRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", "ABC", "aaaaaaaa"} {
		if _, err := ParseID(value); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("ParseID(%q) error = %v", value, err)
		}
	}
}
