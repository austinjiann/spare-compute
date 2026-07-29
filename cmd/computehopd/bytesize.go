package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errInvalidByteSize = errors.New("invalid byte size")

type byteSizeValue struct {
	target *int64
}

func (value *byteSizeValue) String() string {
	if value == nil || value.target == nil {
		return ""
	}
	return formatByteSize(*value.target)
}

func (value *byteSizeValue) Set(encoded string) error {
	if value == nil || value.target == nil {
		return errInvalidByteSize
	}
	parsed, err := parseByteSize(encoded)
	if err != nil {
		return err
	}
	*value.target = parsed
	return nil
}

func parseByteSize(encoded string) (int64, error) {
	encoded = strings.ToUpper(strings.TrimSpace(encoded))
	if encoded == "" {
		return 0, errInvalidByteSize
	}
	type unit struct {
		suffix     string
		multiplier float64
	}
	units := []unit{
		{suffix: "TIB", multiplier: 1 << 40},
		{suffix: "GIB", multiplier: 1 << 30},
		{suffix: "MIB", multiplier: 1 << 20},
		{suffix: "KIB", multiplier: 1 << 10},
		{suffix: "TB", multiplier: 1_000_000_000_000},
		{suffix: "GB", multiplier: 1_000_000_000},
		{suffix: "MB", multiplier: 1_000_000},
		{suffix: "KB", multiplier: 1_000},
		{suffix: "B", multiplier: 1},
	}
	multiplier := float64(1)
	number := encoded
	for _, candidate := range units {
		if strings.HasSuffix(encoded, candidate.suffix) {
			multiplier = candidate.multiplier
			number = strings.TrimSpace(strings.TrimSuffix(encoded, candidate.suffix))
			break
		}
	}
	numeric, err := strconv.ParseFloat(number, 64)
	bytes := numeric * multiplier
	if err != nil || numeric <= 0 || math.IsInf(bytes, 0) || math.IsNaN(bytes) ||
		bytes > math.MaxInt64 || bytes != math.Trunc(bytes) {
		return 0, fmt.Errorf("%w %q: use a value such as 20GiB or 512MB", errInvalidByteSize, encoded)
	}
	return int64(bytes), nil
}

func formatByteSize(value int64) string {
	for _, unit := range []struct {
		suffix string
		bytes  int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if value >= unit.bytes && value%unit.bytes == 0 {
			return fmt.Sprintf("%d%s", value/unit.bytes, unit.suffix)
		}
	}
	return fmt.Sprintf("%dB", value)
}
