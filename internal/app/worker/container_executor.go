package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobycontainer "github.com/moby/moby/api/types/container"
	mobymount "github.com/moby/moby/api/types/mount"
	mobyclient "github.com/moby/moby/client"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
)

const (
	containerWorkspacePath        = "/workspace"
	defaultEngineOperationTimeout = 5 * time.Second
)

var ErrContainerEngineUnavailable = errors.New("container engine unavailable")

type containerEngine interface {
	Ping(context.Context, mobyclient.PingOptions) (mobyclient.PingResult, error)
	ContainerCreate(context.Context, mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error)
	ContainerAttach(context.Context, string, mobyclient.ContainerAttachOptions) (mobyclient.ContainerAttachResult, error)
	ContainerStart(context.Context, string, mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error)
	ContainerInspect(context.Context, string, mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
	ContainerWait(context.Context, string, mobyclient.ContainerWaitOptions) mobyclient.ContainerWaitResult
	ContainerKill(context.Context, string, mobyclient.ContainerKillOptions) (mobyclient.ContainerKillResult, error)
	ContainerRemove(context.Context, string, mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error)
}

// ContainerExecutorStarter starts jobs through a Docker/Podman-compatible
// Engine API. It deliberately avoids shelling out to docker or podman CLIs.
type ContainerExecutorStarter struct {
	engine           containerEngine
	operationTimeout time.Duration
}

// NewContainerExecutorStarter builds a container executor from an already
// configured Engine API client.
func NewContainerExecutorStarter(engine containerEngine) (*ContainerExecutorStarter, error) {
	if engine == nil {
		return nil, ErrMissingDependency
	}
	return &ContainerExecutorStarter{
		engine:           engine,
		operationTimeout: defaultEngineOperationTimeout,
	}, nil
}

// NewContainerExecutorStarterFromEnv builds and verifies a Docker/Podman Engine
// API client using standard Docker environment variables.
func NewContainerExecutorStarterFromEnv(ctx context.Context) (*ContainerExecutorStarter, error) {
	client, err := mobyclient.New(mobyclient.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("%w: configure Engine API client: %v", ErrContainerEngineUnavailable, err)
	}
	starter, err := NewContainerExecutorStarter(client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	pingContext, cancel := context.WithTimeout(ctx, starter.operationTimeout)
	defer cancel()
	if _, err := client.Ping(pingContext, mobyclient.PingOptions{NegotiateAPIVersion: true}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%w: ping Engine API: %v", ErrContainerEngineUnavailable, err)
	}
	return starter, nil
}

// SupportedExecutors reports that this starter accepts container jobs.
func (*ContainerExecutorStarter) SupportedExecutors() []job.Executor {
	return []job.Executor{job.ExecutorContainer}
}

// Start creates, attaches, and starts a one-job container.
func (starter *ContainerExecutorStarter) Start(
	spec job.Spec,
	stdout io.Writer,
	stderr io.Writer,
) (ManagedProcess, error) {
	if starter == nil || starter.engine == nil {
		return nil, ErrMissingDependency
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Executor != job.ExecutorContainer {
		return nil, fmt.Errorf("%w: executor %q", job.ErrInvalidSpec, spec.Executor)
	}

	createContext, cancelCreate := context.WithTimeout(context.Background(), starter.operationTimeout)
	created, err := starter.engine.ContainerCreate(createContext, containerCreateOptions(spec))
	cancelCreate()
	if err != nil {
		return nil, fmt.Errorf("create container from image %q: %w", spec.ContainerImage, err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("%w: Engine API returned an empty container ID", job.ErrInvalidSpec)
	}

	attached, err := starter.attach(created.ID)
	if err != nil {
		starter.removeCreatedContainer(created.ID)
		return nil, err
	}
	outputDone := make(chan error, 1)
	go copyContainerOutput(attached, stdout, stderr, outputDone)

	startContext, cancelStart := context.WithTimeout(context.Background(), starter.operationTimeout)
	if _, err := starter.engine.ContainerStart(startContext, created.ID, mobyclient.ContainerStartOptions{}); err != nil {
		cancelStart()
		attached.Close()
		<-outputDone
		starter.forceRemoveContainer(created.ID)
		return nil, fmt.Errorf("start container %s: %w", created.ID, err)
	}
	cancelStart()

	pid, err := starter.containerPID(created.ID)
	if err != nil {
		attached.Close()
		<-outputDone
		starter.forceRemoveContainer(created.ID)
		return nil, err
	}
	wait := starter.engine.ContainerWait(context.Background(), created.ID, mobyclient.ContainerWaitOptions{
		Condition: mobycontainer.WaitConditionNotRunning,
	})
	return &ContainerProcess{
		engine:     starter.engine,
		id:         created.ID,
		pid:        pid,
		outputDone: outputDone,
		attach:     attached,
		wait:       wait,
	}, nil
}

func (starter *ContainerExecutorStarter) attach(containerID string) (mobyclient.ContainerAttachResult, error) {
	attachContext, cancelAttach := context.WithTimeout(context.Background(), starter.operationTimeout)
	attached, err := starter.engine.ContainerAttach(attachContext, containerID, mobyclient.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	cancelAttach()
	if err != nil {
		return mobyclient.ContainerAttachResult{}, fmt.Errorf("attach container %s output: %w", containerID, err)
	}
	return attached, nil
}

func (starter *ContainerExecutorStarter) containerPID(containerID string) (int, error) {
	inspectContext, cancelInspect := context.WithTimeout(context.Background(), starter.operationTimeout)
	inspected, err := starter.engine.ContainerInspect(inspectContext, containerID, mobyclient.ContainerInspectOptions{})
	cancelInspect()
	if err != nil {
		return 0, fmt.Errorf("inspect started container %s: %w", containerID, err)
	}
	if inspected.Container.State == nil || inspected.Container.State.Pid <= 0 {
		return 0, fmt.Errorf("inspect started container %s: missing process ID", containerID)
	}
	return inspected.Container.State.Pid, nil
}

func (starter *ContainerExecutorStarter) removeCreatedContainer(containerID string) {
	removeContext, cancelRemove := context.WithTimeout(context.Background(), starter.operationTimeout)
	_, _ = starter.engine.ContainerRemove(removeContext, containerID, mobyclient.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         false,
	})
	cancelRemove()
}

func (starter *ContainerExecutorStarter) forceRemoveContainer(containerID string) {
	removeContext, cancelRemove := context.WithTimeout(context.Background(), starter.operationTimeout)
	_, _ = starter.engine.ContainerRemove(removeContext, containerID, mobyclient.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
	cancelRemove()
}

func containerCreateOptions(spec job.Spec) mobyclient.ContainerCreateOptions {
	command := append([]string{spec.Executable}, spec.Arguments...)
	config := &mobycontainer.Config{
		Image:        spec.ContainerImage,
		Cmd:          command,
		Env:          containerEnvironment(spec.Environment),
		AttachStdout: true,
		AttachStderr: true,
		Labels: map[string]string{
			"com.computehop.executor": string(job.ExecutorContainer),
		},
	}
	hostConfig := &mobycontainer.HostConfig{
		AutoRemove: false,
	}
	if spec.WorkingDirectory != "" {
		config.WorkingDir = containerWorkspacePath
		hostConfig.Mounts = []mobymount.Mount{{
			Type:   mobymount.TypeBind,
			Source: spec.WorkingDirectory,
			Target: containerWorkspacePath,
		}}
	}
	return mobyclient.ContainerCreateOptions{
		Config:     config,
		HostConfig: hostConfig,
		Image:      spec.ContainerImage,
	}
}

func containerEnvironment(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result
}

func copyContainerOutput(
	attached mobyclient.ContainerAttachResult,
	stdout io.Writer,
	stderr io.Writer,
	done chan<- error,
) {
	defer attached.Close()
	if attached.Reader == nil {
		done <- nil
		return
	}
	_, err := stdcopy.StdCopy(stdout, stderr, attached.Reader)
	done <- err
}

// ContainerProcess owns one Engine API container until Wait removes it.
type ContainerProcess struct {
	engine     containerEngine
	id         string
	pid        int
	outputDone <-chan error
	attach     mobyclient.ContainerAttachResult
	wait       mobyclient.ContainerWaitResult
	waitOnce   sync.Once
	waitResult processes.Exit
}

// PID returns the container init process identifier reported by the engine.
func (process *ContainerProcess) PID() int {
	if process == nil {
		return 0
	}
	return process.pid
}

// GracefulStop sends SIGTERM without waiting for the container to exit.
func (process *ContainerProcess) GracefulStop() error {
	if process == nil || process.engine == nil || process.id == "" {
		return nil
	}
	_, err := process.engine.ContainerKill(context.Background(), process.id, mobyclient.ContainerKillOptions{
		Signal: "SIGTERM",
	})
	return err
}

// Kill force-stops the container.
func (process *ContainerProcess) Kill() error {
	if process == nil || process.engine == nil || process.id == "" {
		return nil
	}
	_, err := process.engine.ContainerKill(context.Background(), process.id, mobyclient.ContainerKillOptions{})
	return err
}

// Wait waits for the container to stop, output copying to finish, and cleanup to
// complete.
func (process *ContainerProcess) Wait() processes.Exit {
	process.waitOnce.Do(func() {
		process.waitResult = process.waitContainer()
		process.attach.Close()
		if process.outputDone != nil {
			if err := <-process.outputDone; err != nil && process.waitResult.WaitErr == nil {
				process.waitResult.WaitErr = fmt.Errorf("copy container output: %w", err)
			}
		}
		if err := process.remove(); err != nil && process.waitResult.WaitErr == nil {
			process.waitResult.WaitErr = err
		}
	})
	return process.waitResult
}

func (process *ContainerProcess) waitContainer() processes.Exit {
	if process.wait.Result == nil && process.wait.Error == nil {
		return processes.Exit{Code: -1, WaitErr: errors.New("container wait was not initialized")}
	}
	select {
	case response := <-process.wait.Result:
		exit := processes.Exit{Code: int(response.StatusCode)}
		if response.Error != nil && response.Error.Message != "" {
			exit.WaitErr = fmt.Errorf("wait for container %s: %s", process.id, response.Error.Message)
		}
		return exit
	case err := <-process.wait.Error:
		if err == nil {
			err = errors.New("container wait failed")
		}
		return processes.Exit{Code: -1, WaitErr: fmt.Errorf("wait for container %s: %w", process.id, err)}
	}
}

func (process *ContainerProcess) remove() error {
	if process.engine == nil || process.id == "" {
		return nil
	}
	_, err := process.engine.ContainerRemove(context.Background(), process.id, mobyclient.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         false,
	})
	if err != nil {
		return fmt.Errorf("remove container %s: %w", process.id, err)
	}
	return nil
}
