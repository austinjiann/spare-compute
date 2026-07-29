package main

import "testing"

func TestByteSizeParsesFriendlyBinaryAndDecimalUnits(t *testing.T) {
	tests := map[string]int64{
		"20GiB":   20 << 30,
		"1.5GiB":  3 << 29,
		"512MB":   512_000_000,
		"1048576": 1 << 20,
		"1MiB":    1 << 20,
	}
	for input, want := range tests {
		got, err := parseByteSize(input)
		if err != nil || got != want {
			t.Fatalf("parseByteSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "off", "-1GiB", "0", "1.2B"} {
		if _, err := parseByteSize(input); err == nil {
			t.Fatalf("parseByteSize(%q) error = nil", input)
		}
	}
}

func TestByteSizeValueFormatsDefault(t *testing.T) {
	value := int64(20 << 30)
	flag := byteSizeValue{target: &value}
	if got := flag.String(); got != "20GiB" {
		t.Fatalf("String() = %q", got)
	}
	if err := flag.Set("2GiB"); err != nil || value != 2<<30 {
		t.Fatalf("Set() = %d, %v", value, err)
	}
}
