package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/containerd/errdefs"
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
	defaultImagePullTimeout       = 30 * time.Minute
)

var ErrContainerEngineUnavailable = errors.New("container engine unavailable")

type containerEngine interface {
	Ping(context.Context, mobyclient.PingOptions) (mobyclient.PingResult, error)
	ImageExists(context.Context, string) (bool, error)
	PullImage(context.Context, string, pullProgressReporter) error
	ContainerCreate(context.Context, mobyclient.ContainerCreateOptions) (mobyclient.ContainerCreateResult, error)
	ContainerAttach(context.Context, string, mobyclient.ContainerAttachOptions) (mobyclient.ContainerAttachResult, error)
	ContainerStart(context.Context, string, mobyclient.ContainerStartOptions) (mobyclient.ContainerStartResult, error)
	ContainerInspect(context.Context, string, mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
	ContainerWait(context.Context, string, mobyclient.ContainerWaitOptions) mobyclient.ContainerWaitResult
	ContainerKill(context.Context, string, mobyclient.ContainerKillOptions) (mobyclient.ContainerKillResult, error)
	ContainerRemove(context.Context, string, mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error)
}

type pullProgressReporter func(completedBytes, totalBytes int64) error

type mobyEngine struct {
	*mobyclient.Client
}

func (engine *mobyEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	_, err := engine.Client.ImageInspect(ctx, image)
	if err == nil {
		return true, nil
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (engine *mobyEngine) PullImage(ctx context.Context, image string, report pullProgressReporter) error {
	response, err := engine.Client.ImagePull(ctx, image, mobyclient.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer response.Close()
	aggregate := newPullProgressAggregate()
	for message, err := range response.JSONMessages(ctx) {
		if err != nil {
			return err
		}
		if message.Error != nil {
			return message.Error
		}
		if message.Progress == nil || report == nil {
			continue
		}
		completedBytes, totalBytes, ok := aggregate.observe(
			message.ID,
			message.Progress.Current,
			message.Progress.Total,
		)
		if ok {
			if err := report(completedBytes, totalBytes); err != nil {
				return err
			}
		}
	}
	return nil
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
	engine := &mobyEngine{Client: client}
	starter, err := NewContainerExecutorStarter(engine)
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
func (starter *ContainerExecutorStarter) Start(request StartRequest) (ManagedProcess, error) {
	if starter == nil || starter.engine == nil {
		return nil, ErrMissingDependency
	}
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	spec := request.Spec
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Executor != job.ExecutorContainer {
		return nil, fmt.Errorf("%w: executor %q", job.ErrInvalidSpec, spec.Executor)
	}
	if err := starter.ensureImage(ctx, request, spec.ContainerImage); err != nil {
		return nil, err
	}

	createContext, cancelCreate := context.WithTimeout(ctx, starter.operationTimeout)
	created, err := starter.engine.ContainerCreate(createContext, containerCreateOptions(spec))
	cancelCreate()
	if err != nil {
		return nil, fmt.Errorf("create container from image %q: %w", spec.ContainerImage, err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("%w: Engine API returned an empty container ID", job.ErrInvalidSpec)
	}

	attached, err := starter.attach(ctx, created.ID)
	if err != nil {
		starter.removeCreatedContainer(created.ID)
		return nil, err
	}
	outputDone := make(chan error, 1)
	go copyContainerOutput(attached, request.Stdout, request.Stderr, outputDone)

	startContext, cancelStart := context.WithTimeout(ctx, starter.operationTimeout)
	if _, err := starter.engine.ContainerStart(startContext, created.ID, mobyclient.ContainerStartOptions{}); err != nil {
		cancelStart()
		attached.Close()
		<-outputDone
		starter.forceRemoveContainer(created.ID)
		return nil, fmt.Errorf("start container %s: %w", created.ID, err)
	}
	cancelStart()

	pid, err := starter.containerPID(ctx, created.ID)
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

func (starter *ContainerExecutorStarter) ensureImage(ctx context.Context, request StartRequest, image string) error {
	inspectContext, cancelInspect := context.WithTimeout(ctx, starter.operationTimeout)
	exists, err := starter.engine.ImageExists(inspectContext, image)
	cancelInspect()
	if err != nil {
		return fmt.Errorf("inspect container image %q: %w", image, err)
	}
	if exists {
		return nil
	}
	pullContext, cancelPull := context.WithTimeout(ctx, defaultImagePullTimeout)
	defer cancelPull()
	if err := starter.engine.PullImage(pullContext, image, func(completedBytes, totalBytes int64) error {
		return reportPullProgress(pullContext, request, completedBytes, totalBytes)
	}); err != nil {
		return fmt.Errorf("pull container image %q: %w", image, err)
	}
	return nil
}

func reportPullProgress(ctx context.Context, request StartRequest, completedBytes, totalBytes int64) error {
	if request.Progress == nil || !request.JobID.Valid() || totalBytes <= 0 {
		return nil
	}
	if completedBytes < 0 {
		completedBytes = 0
	}
	if completedBytes > totalBytes {
		completedBytes = totalBytes
	}
	return request.Progress.SetProgress(ctx, request.JobID, job.Progress{
		Phase:          job.ProgressPull,
		CompletedBytes: completedBytes,
		TotalBytes:     totalBytes,
		UpdatedAt:      time.Now(),
	})
}

type pullProgressAggregate struct {
	layers map[string]pullProgressLayer
}

type pullProgressLayer struct {
	completedBytes int64
	totalBytes     int64
}

func newPullProgressAggregate() *pullProgressAggregate {
	return &pullProgressAggregate{layers: make(map[string]pullProgressLayer)}
}

func (aggregate *pullProgressAggregate) observe(id string, completedBytes, totalBytes int64) (int64, int64, bool) {
	if aggregate == nil || totalBytes <= 0 {
		return 0, 0, false
	}
	if id == "" {
		id = "image"
	}
	if completedBytes < 0 {
		completedBytes = 0
	}
	if completedBytes > totalBytes {
		completedBytes = totalBytes
	}
	aggregate.layers[id] = pullProgressLayer{completedBytes: completedBytes, totalBytes: totalBytes}
	var aggregateCompleted int64
	var aggregateTotal int64
	for _, layer := range aggregate.layers {
		aggregateCompleted += layer.completedBytes
		aggregateTotal += layer.totalBytes
	}
	return aggregateCompleted, aggregateTotal, aggregateTotal > 0
}

func (starter *ContainerExecutorStarter) attach(ctx context.Context, containerID string) (mobyclient.ContainerAttachResult, error) {
	attachContext, cancelAttach := context.WithTimeout(ctx, starter.operationTimeout)
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

func (starter *ContainerExecutorStarter) containerPID(ctx context.Context, containerID string) (int, error) {
	inspectContext, cancelInspect := context.WithTimeout(ctx, starter.operationTimeout)
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
		if err := process.waitForOutput(); err != nil && process.waitResult.WaitErr == nil {
			process.waitResult.WaitErr = fmt.Errorf("copy container output: %w", err)
		}
		if err := process.remove(); err != nil && process.waitResult.WaitErr == nil {
			process.waitResult.WaitErr = err
		}
	})
	return process.waitResult
}

func (process *ContainerProcess) waitForOutput() error {
	if process.outputDone == nil {
		return nil
	}
	select {
	case err := <-process.outputDone:
		return err
	case <-time.After(defaultEngineOperationTimeout):
		process.attach.Close()
		return <-process.outputDone
	}
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
