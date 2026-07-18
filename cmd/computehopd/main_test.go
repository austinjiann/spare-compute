package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
)

func TestRunCheckInitializesDurableState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout, stderr bytes.Buffer

	if err := run(
		context.Background(),
		[]string{"--check", "--state-dir", stateDir},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, stderr.String())
	}
	if got, want := stdout.String(), "computehopd ready\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	databasePath, err := paths.DatabasePath(stateDir)
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
	database, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen daemon database: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close daemon database: %v", err)
	}
}

func TestRunVersionDoesNotCreateState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout, stderr bytes.Buffer

	if err := run(
		context.Background(),
		[]string{"--version", "--state-dir", stateDir},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), version+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("version command created state directory: error = %v", err)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout bytes.Buffer
	stderr := newSignalBuffer("computehopd started")
	result := make(chan error, 1)
	go func() {
		result <- run(ctx, []string{"--state-dir", stateDir}, &stdout, stderr)
	}()

	select {
	case <-stderr.matched:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not start; logs = %q", stderr.String())
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not stop after cancellation")
	}
	if logs := stderr.String(); !strings.Contains(logs, "computehopd started") || !strings.Contains(logs, "computehopd stopped") {
		t.Fatalf("daemon lifecycle logs = %q", logs)
	}
}

type signalBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	pattern string
	matched chan struct{}
	once    sync.Once
}

func newSignalBuffer(pattern string) *signalBuffer {
	return &signalBuffer{
		pattern: pattern,
		matched: make(chan struct{}),
	}
}

func (buffer *signalBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written, err := buffer.buffer.Write(contents)
	if strings.Contains(buffer.buffer.String(), buffer.pattern) {
		buffer.once.Do(func() { close(buffer.matched) })
	}
	return written, err
}

func (buffer *signalBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"unexpected"}, &stdout, &stderr); err == nil {
		t.Fatalf("run() error = nil")
	}
}

func TestRunRejectsBlankStateDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"--check", "--state-dir", " "},
		&stdout,
		&stderr,
	); err == nil {
		t.Fatalf("run() error = nil")
	}
}
