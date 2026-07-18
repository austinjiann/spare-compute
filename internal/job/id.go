package job

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ID uniquely identifies a job across every ComputeHop device.
type ID string

var ErrInvalidID = errors.New("invalid job ID")

// NewID returns a random UUIDv4-formatted job ID.
func NewID() (ID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate job ID: %w", err)
	}

	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80

	return formatID(value), nil
}

// ParseID validates and canonicalizes a UUID-formatted job ID.
func ParseID(value string) (ID, error) {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", fmt.Errorf("%w: expected UUID format", ErrInvalidID)
	}

	compact := strings.ReplaceAll(value, "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("%w: expected hexadecimal UUID", ErrInvalidID)
	}

	var bytes [16]byte
	copy(bytes[:], decoded)
	return formatID(bytes), nil
}

// Valid reports whether the ID has the canonical UUID representation.
func (id ID) Valid() bool {
	parsed, err := ParseID(string(id))
	return err == nil && parsed == id
}

func formatID(value [16]byte) ID {
	return ID(fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	))
}
