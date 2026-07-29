// Package portablepath validates paths that cross operating-system boundaries.
package portablepath

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const MaximumBytes = 4_096

var ErrUnsafe = errors.New("unsafe portable path")

// Validate accepts only normalized relative paths that are safe on macOS,
// Windows, and Linux. Paths always use forward slashes on the wire.
func Validate(value string) error {
	if value == "" || len(value) > MaximumBytes || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "\\\x00<>:\"|?*") || strings.HasPrefix(value, "/") ||
		path.Clean(value) != value || value == "." || value == ".." ||
		strings.HasPrefix(value, "../") {
		return ErrUnsafe
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." ||
			strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") ||
			windowsReservedName(segment) {
			return ErrUnsafe
		}
		for _, character := range segment {
			if character < 0x20 {
				return ErrUnsafe
			}
		}
	}
	return nil
}

// Key returns the conservative case-insensitive collision key used for a
// portable path after validation.
func Key(value string) string {
	return strings.ToLower(value)
}

func windowsReservedName(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
