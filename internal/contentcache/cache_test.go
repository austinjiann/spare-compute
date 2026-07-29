package contentcache

import (
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/snapshot"
)

func TestEntryStatsAndQuotaValidation(t *testing.T) {
	now := time.Unix(1_900_000_000, 0).UTC()
	entry := Entry{Digest: snapshot.Sum([]byte("content")), Size: 7, LastAccessed: now}
	if err := entry.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Entry{}).Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("empty entry error = %v", err)
	}
	if err := (Stats{Entries: 2, Bytes: 20, ProtectedEntries: 1, ProtectedBytes: 8}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Stats{Entries: 1, Bytes: 4, ProtectedEntries: 2}).Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("invalid stats error = %v", err)
	}
	for _, value := range []int64{MinimumMaximumBytes, DefaultMaximumBytes, MaximumMaximumBytes} {
		if err := ValidateMaximumBytes(value); err != nil {
			t.Fatalf("ValidateMaximumBytes(%d) = %v", value, err)
		}
	}
	if err := ValidateMaximumBytes(MinimumMaximumBytes - 1); !errors.Is(err, ErrInvalidQuota) {
		t.Fatalf("small quota error = %v", err)
	}
	if !errors.Is(&QuotaError{}, ErrQuotaExceeded) {
		t.Fatal("QuotaError does not unwrap to ErrQuotaExceeded")
	}
}
