package worker_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/app/worker"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

func TestJobServiceSubmissionSurvivesDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computehop.db")
	database, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}

	id, err := job.ParseID("019abcdf-0123-4567-89ab-0123456789ab")
	if err != nil {
		t.Fatalf("job.ParseID() error = %v", err)
	}
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	service, err := worker.NewJobService(worker.Dependencies{
		Jobs:       database.Jobs(),
		Executions: database.Executions(),
		Logs:       mustLogStore(t, filepath.Dir(path), database),
		GenerateID: func() (job.ID, error) { return id, nil },
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	})
	if err != nil {
		t.Fatalf("worker.NewJobService() error = %v", err)
	}

	want, err := service.Submit(ctx, job.Spec{
		Executable: "echo",
		Arguments:  []string{"hello"},
		Executor:   job.ExecutorNative,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	got, err := reopened.Jobs().Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get() after reopen error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("job after reopen = %#v, want %#v", got, want)
	}
}

func mustLogStore(t *testing.T, stateDir string, database *sqlite.Database) *joblogging.Store {
	t.Helper()
	store, err := joblogging.NewStore(stateDir, database.Executions(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
