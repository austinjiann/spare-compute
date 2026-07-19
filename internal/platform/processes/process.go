// Package processes starts and controls unprivileged native process trees.
package processes

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

var (
	ErrUnsupportedPlatform = errors.New("native process control is unsupported on this platform")
	ErrWait                = errors.New("wait for native process")
)

// Exit is the normalized result returned after the root process and I/O finish.
type Exit struct {
	Code    int
	Signal  string
	WaitErr error
}

// Process owns a started root process and its platform process-tree controller.
type Process struct {
	command    *exec.Cmd
	controller treeController
	waitOnce   sync.Once
	waitResult Exit
}

// Start launches a native command without invoking a shell.
func Start(spec job.Spec, stdout, stderr io.Writer) (*Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Executor != job.ExecutorNative {
		return nil, fmt.Errorf("%w: executor %q", job.ErrInvalidSpec, spec.Executor)
	}
	command := exec.Command(spec.Executable, spec.Arguments...)
	command.Dir = spec.WorkingDirectory
	command.Env = environment(spec.Environment)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	configureCommand(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", spec.Executable, err)
	}
	controller, err := newTreeController(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("control process tree: %w", err)
	}
	return &Process{command: command, controller: controller}, nil
}

// PID returns the root child process identifier.
func (process *Process) PID() int {
	if process == nil || process.command == nil || process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

// GracefulStop asks the complete process tree to terminate cleanly.
func (process *Process) GracefulStop() error {
	if process == nil || process.controller == nil {
		return nil
	}
	return process.controller.gracefulStop()
}

// Kill force-terminates the complete process tree.
func (process *Process) Kill() error {
	if process == nil || process.controller == nil {
		return nil
	}
	return process.controller.kill()
}

// Wait waits once for the root process and all configured I/O copying.
func (process *Process) Wait() Exit {
	process.waitOnce.Do(func() {
		err := process.command.Wait()
		process.waitResult.Code = process.command.ProcessState.ExitCode()
		process.waitResult.Signal = exitSignal(process.command.ProcessState)
		var exitError *exec.ExitError
		if err != nil && !errors.As(err, &exitError) {
			process.waitResult.WaitErr = fmt.Errorf("%w: %v", ErrWait, err)
		}
		// A root command must not escape by leaving descendants behind.
		_ = process.controller.kill()
		_ = process.controller.close()
	})
	return process.waitResult
}

type treeController interface {
	gracefulStop() error
	kill() error
	close() error
}

func environment(values map[string]string) []string {
	merged := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found {
			merged[name] = value
		}
	}
	for name, value := range values {
		merged[name] = value
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+merged[name])
	}
	return result
}
