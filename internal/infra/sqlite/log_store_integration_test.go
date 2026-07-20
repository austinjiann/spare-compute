package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	joblogging "github.com/austinjiann/spare-compute/internal/logging"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
)

func TestLogStorePersistsOrderedExternalDataAndRepairsTail(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	database, err := Open(ctx, filepath.Join(stateDir, "computehop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	value := createQueuedJob(t, ctx, database, 20)
	if _, err := database.Executions().Claim(ctx, value.ID, 2000, value.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	now := testTime(21)
	store, err := joblogging.NewStore(stateDir, database.Executions(), func() time.Time {
		now = now.Add(time.Nanosecond)
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.OpenWriter(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Stream(ctx, joblogging.StreamStdout).Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Stream(ctx, joblogging.StreamStderr).Write([]byte("oops")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	page, err := store.Read(ctx, value.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || string(page.Records[0].Data) != "hello" || !page.HasMore {
		t.Fatalf("first page = %#v", page)
	}
	page, err = store.Read(ctx, value.ID, page.Records[0].Sequence, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].Stream != joblogging.StreamStderr ||
		string(page.Records[0].Data) != "oops" || page.HasMore {
		t.Fatalf("second page = %#v", page)
	}

	logPath, err := paths.JobLogPath(stateDir, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("uncommitted"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenWriter(ctx, value.ID)
	if err != nil {
		t.Fatalf("OpenWriter(repair) error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 9 {
		t.Fatalf("repaired log size = %d, want 9", info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLogStoreDetectsMissingCommittedBytes(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	database, err := Open(ctx, filepath.Join(stateDir, "computehop.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	value := createQueuedJob(t, ctx, database, 21)
	if _, err := database.Executions().Claim(ctx, value.ID, 2100, value.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	store, err := joblogging.NewStore(stateDir, database.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.OpenWriter(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Stream(ctx, joblogging.StreamStdout).Write([]byte("durable")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	logPath, _ := paths.JobLogPath(stateDir, value.ID)
	if err := os.Truncate(logPath, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, value.ID, 0, 10); !errors.Is(err, joblogging.ErrCorrupt) {
		t.Fatalf("Read(corrupt) error = %v, want ErrCorrupt", err)
	}
}
