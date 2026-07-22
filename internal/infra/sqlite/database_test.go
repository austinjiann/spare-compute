package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenConfiguresAndMigratesDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "computehop.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	var journalMode string
	if err := database.sql.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := database.sql.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var actualApplicationID int
	if err := database.sql.QueryRow("PRAGMA application_id").Scan(&actualApplicationID); err != nil {
		t.Fatalf("read application_id: %v", err)
	}
	if actualApplicationID != applicationID {
		t.Fatalf("application_id = %d, want %d", actualApplicationID, applicationID)
	}

	var migrationCount int
	if err := database.sql.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 8 {
		t.Fatalf("migration count = %d, want 8", migrationCount)
	}

	if runtime.GOOS != "windows" {
		fileInfo, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat database: %v", err)
		}
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Fatalf("database permissions = %o, want 600", got)
		}
	}
}

func TestOpenCanReapplyMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computehop.db")

	first, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer second.Close()

	var count int
	if err := second.sql.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 8 {
		t.Fatalf("migration count after reopen = %d, want 8", count)
	}
}

func TestOpenRejectsDatabaseOwnedByAnotherApplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	foreign, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := foreign.Exec("PRAGMA application_id = 1234"); err != nil {
		t.Fatalf("set foreign application ID: %v", err)
	}
	if err := foreign.Close(); err != nil {
		t.Fatalf("close foreign database: %v", err)
	}

	if database, err := Open(context.Background(), path); err == nil {
		_ = database.Close()
		t.Fatalf("Open() accepted a foreign database")
	}
}
