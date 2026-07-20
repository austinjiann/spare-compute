//go:build darwin || linux

package processes

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

func TestStartUsesDeclaredDirectoryEnvironmentAndStreams(t *testing.T) {
	directory := t.TempDir()
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	process, err := Start(job.Spec{
		Executable:       "/bin/sh",
		Arguments:        []string{"-c", `printf '%s:%s' "$VALUE" "$PWD"; printf 'problem' >&2`},
		WorkingDirectory: directory,
		Environment:      map[string]string{"VALUE": "declared"},
		Executor:         job.ExecutorNative,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	exit := process.Wait()
	if exit.Code != 0 || exit.WaitErr != nil {
		t.Fatalf("Wait() = %#v", exit)
	}
	if got := stdout.String(); got != "declared:"+resolvedDirectory {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "problem" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestGracefulStopTerminatesNestedProcessTree(t *testing.T) {
	directory := t.TempDir()
	childPIDPath := filepath.Join(directory, "child.pid")
	process, err := Start(job.Spec{
		Executable:       "/bin/sh",
		Arguments:        []string{"-c", "sleep 30 & child=$!; echo $child > child.pid; wait $child"},
		WorkingDirectory: directory,
		Environment:      map[string]string{"PATH": "/usr/bin:/bin"},
		Executor:         job.ExecutorNative,
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	childPID := waitForChildPID(t, childPIDPath)
	waitResult := make(chan Exit, 1)
	go func() { waitResult <- process.Wait() }()
	if err := process.GracefulStop(); err != nil {
		t.Fatalf("GracefulStop() error = %v", err)
	}
	select {
	case exit := <-waitResult:
		if exit.Code == 0 {
			t.Fatalf("terminated process exit = %#v", exit)
		}
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		t.Fatal("process tree did not stop")
	}
	deadline := time.Now().Add(2 * time.Second)
	for Alive(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if Alive(childPID) {
		t.Fatalf("nested child PID %d remained alive", childPID)
	}
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child PID file was not created")
	return 0
}
