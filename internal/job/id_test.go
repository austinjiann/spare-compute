package job

import (
	"errors"
	"testing"
)

func TestNewIDProducesCanonicalUniqueIDs(t *testing.T) {
	first, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	second, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}

	if !first.Valid() {
		t.Fatalf("first ID %q is not canonical", first)
	}
	if !second.Valid() {
		t.Fatalf("second ID %q is not canonical", second)
	}
	if first == second {
		t.Fatalf("NewID() returned duplicate ID %q", first)
	}
}

func TestParseIDCanonicalizesHexCase(t *testing.T) {
	id, err := ParseID("019ABCDF-0123-4567-89AB-0123456789AB")
	if err != nil {
		t.Fatalf("ParseID() error = %v", err)
	}
	if got, want := string(id), "019abcdf-0123-4567-89ab-0123456789ab"; got != want {
		t.Fatalf("ParseID() = %q, want %q", got, want)
	}
}

func TestParseIDRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"",
		"not-a-uuid",
		"019abcdf0123456789ab0123456789ab",
		"019abcdf-0123-4567-89ab-0123456789ag",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseID(value)
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("ParseID(%q) error = %v, want ErrInvalidID", value, err)
			}
		})
	}
}
