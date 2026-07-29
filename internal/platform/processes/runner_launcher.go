package processes

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/job"
)

// RunnerLauncher starts detached, single-job modes of the daemon executable.
type RunnerLauncher struct {
	executable string
	stateDir   string
	cacheBytes int64
}

// NewRunnerLauncher constructs the durable runner-process launcher.
func NewRunnerLauncher(executable, stateDir string, cacheBytes int64) (*RunnerLauncher, error) {
	if executable == "" || stateDir == "" {
		return nil, errors.New("runner executable and state directory are required")
	}
	if err := contentcache.ValidateMaximumBytes(cacheBytes); err != nil {
		return nil, err
	}
	return &RunnerLauncher{executable: executable, stateDir: stateDir, cacheBytes: cacheBytes}, nil
}

// Launch starts a runner that can outlive the orchestrator daemon.
func (launcher *RunnerLauncher) Launch(id job.ID) error {
	if !id.Valid() {
		return job.ErrInvalidID
	}
	command := exec.Command(
		launcher.executable,
		"--runner-job", string(id),
		"--state-dir", launcher.stateDir,
		"--cache-size", strconv.FormatInt(launcher.cacheBytes, 10)+"B",
	)
	Detach(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start runner for %s: %w", id, err)
	}
	// Reap the runner while this daemon is alive. Waiting does not couple their
	// lifetimes; an exiting daemon leaves the detached runner to the OS parent.
	go func() { _ = command.Wait() }()
	return nil
}
