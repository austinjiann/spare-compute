package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	driverName    = "sqlite"
	applicationID = 0x43484f50 // "CHOP"
)

// Database owns the single local SQLite connection used by a ComputeHop daemon.
type Database struct {
	sql        *sql.DB
	jobs       *JobRepository
	executions *ExecutionRepository
	trust      *TrustRepository
	placements *PlacementRepository
	artifacts  *ArtifactRepository
}

// Open creates or opens a local ComputeHop database and applies all migrations.
func Open(ctx context.Context, path string) (*Database, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create SQLite directory: %w", err)
		}
	}

	database, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	closeOnError := func(openErr error) (*Database, error) {
		_ = database.Close()
		return nil, openErr
	}

	if err := database.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("connect to SQLite database: %w", err))
	}
	if err := configure(ctx, database, path != ":memory:"); err != nil {
		return closeOnError(err)
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			return closeOnError(fmt.Errorf("restrict SQLite permissions: %w", err))
		}
	}
	if err := applyMigrations(ctx, database); err != nil {
		return closeOnError(err)
	}

	result := &Database{sql: database}
	result.jobs = &JobRepository{database: database}
	result.executions = &ExecutionRepository{database: database}
	result.trust = &TrustRepository{database: database}
	result.placements = &PlacementRepository{database: database}
	result.artifacts = &ArtifactRepository{database: database}
	return result, nil
}

// Artifacts returns the worker's durable collected-output repository.
func (database *Database) Artifacts() *ArtifactRepository {
	return database.artifacts
}

// Placements returns the orchestrator's durable remote job routing repository.
func (database *Database) Placements() *PlacementRepository {
	return database.placements
}

// Trust returns the durable paired-device repository owned by this database.
func (database *Database) Trust() *TrustRepository {
	return database.trust
}

// Executions returns the runner and job-log repository owned by this database.
func (database *Database) Executions() *ExecutionRepository {
	return database.executions
}

// Jobs returns the durable job repository owned by this database.
func (database *Database) Jobs() *JobRepository {
	return database.jobs
}

// Close closes the database connection.
func (database *Database) Close() error {
	if database == nil || database.sql == nil {
		return nil
	}
	return database.sql.Close()
}

func configure(ctx context.Context, database *sql.DB, expectWAL bool) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA trusted_schema = OFF",
	}
	for _, pragma := range pragmas {
		if _, err := database.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure SQLite with %q: %w", pragma, err)
		}
	}

	var existingApplicationID int
	if err := database.QueryRowContext(ctx, "PRAGMA application_id").Scan(&existingApplicationID); err != nil {
		return fmt.Errorf("read SQLite application ID: %w", err)
	}
	if existingApplicationID != 0 && existingApplicationID != applicationID {
		return fmt.Errorf("open SQLite database: application ID %d does not belong to ComputeHop", existingApplicationID)
	}
	if existingApplicationID == 0 {
		if _, err := database.ExecContext(
			ctx,
			fmt.Sprintf("PRAGMA application_id = %d", applicationID),
		); err != nil {
			return fmt.Errorf("set SQLite application ID: %w", err)
		}
	}

	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("enable SQLite WAL mode: %w", err)
	}
	if expectWAL && !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("enable SQLite WAL mode: SQLite returned %q", journalMode)
	}
	return nil
}
