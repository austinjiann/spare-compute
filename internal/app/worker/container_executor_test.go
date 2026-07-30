package worker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"github.com/austinjiann/spare-compute/internal/job"
)

func TestContainerExecutorStarterCreatesAttachedContainer(t *testing.T) {
	engine := &fakeContainerEngine{
		createID:     "container-1",
		inspectPID:   4242,
		attachStream: multiplexContainerOutput(stdcopy.Stdout, "ok\n", stdcopy.Stderr, "warn\n"),
		waitResponses: bufferedWaitResponses(mobycontainer.WaitResponse{
			StatusCode: 7,
		}),
		waitErrors: make(chan error),
	}
	starter, err := NewContainerExecutorStarter(engine)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	process, err := starter.Start(job.Spec{
		Executable:       "go",
		Arguments:        []string{"test", "./..."},
		WorkingDirectory: "/host/project",
		Environment: map[string]string{
			"ZED": "last",
			"GO":  "1",
		},
		Executor:       job.ExecutorContainer,
		ContainerImage: "golang:latest",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() != 4242 {
		t.Fatalf("PID() = %d, want 4242", process.PID())
	}
	exit := process.Wait()
	if exit.Code != 7 || exit.WaitErr != nil {
		t.Fatalf("Wait() = %#v", exit)
	}
	if got := stdout.String(); got != "ok\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "warn\n" {
		t.Fatalf("stderr = %q", got)
	}

	if engine.createOptions.Config.Image != "golang:latest" ||
		!reflect.DeepEqual([]string(engine.createOptions.Config.Cmd), []string{"go", "test", "./..."}) ||
		engine.createOptions.Config.WorkingDir != containerWorkspacePath {
		t.Fatalf("container config = %#v", engine.createOptions.Config)
	}
	if got, want := engine.createOptions.Config.Env, []string{"GO=1", "ZED=last"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Env = %#v, want %#v", got, want)
	}
	if len(engine.createOptions.HostConfig.Mounts) != 1 ||
		engine.createOptions.HostConfig.Mounts[0].Source != "/host/project" ||
		engine.createOptions.HostConfig.Mounts[0].Target != containerWorkspacePath {
		t.Fatalf("mounts = %#v", engine.createOptions.HostConfig.Mounts)
	}
	if engine.startedID != "container-1" || engine.waitedID != "container-1" ||
		!reflect.DeepEqual(engine.removedIDs, []string{"container-1"}) {
		t.Fatalf("lifecycle: started=%q waited=%q removed=%#v", engine.startedID, engine.waitedID, engine.removedIDs)
	}
}

func TestContainerExecutorStarterStopSignalsContainer(t *testing.T) {
	engine := &fakeContainerEngine{
		createID:       "container-2",
		inspectPID:     777,
		waitResponses:  make(chan mobycontainer.WaitResponse),
		waitErrors:     bufferedWaitErrors(errors.New("stopped by test")),
		attachStream:   nil,
		attachWaitDone: make(chan struct{}),
	}
	starter, err := NewContainerExecutorStarter(engine)
	if err != nil {
		t.Fatal(err)
	}
	process, err := starter.Start(job.Spec{
		Executable: "sleep", Arguments: []string{"60"}, Executor: job.ExecutorContainer, ContainerImage: "alpine:latest",
	}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.GracefulStop(); err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.killSignals, []string{"SIGTERM", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kill signals = %#v, want %#v", got, want)
	}
	exit := process.Wait()
	if exit.WaitErr == nil {
		t.Fatalf("Wait() = %#v, want error after closed wait channel", exit)
	}
}

func TestContainerExecutorStarterCleansUpWhenStartFails(t *testing.T) {
	engine := &fakeContainerEngine{
		createID:      "container-3",
		startErr:      errors.New("engine failed"),
		waitResponses: make(chan mobycontainer.WaitResponse),
		waitErrors:    make(chan error),
	}
	starter, err := NewContainerExecutorStarter(engine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = starter.Start(job.Spec{
		Executable: "echo", Executor: job.ExecutorContainer, ContainerImage: "alpine:latest",
	}, io.Discard, io.Discard)
	if err == nil || !stringsContain(err.Error(), "start container container-3") {
		t.Fatalf("Start() error = %v", err)
	}
	if got, want := engine.removedIDs, []string{"container-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed IDs = %#v, want %#v", got, want)
	}
	if !engine.removedForce[0] {
		t.Fatalf("remove Force = false, want true")
	}
}

func multiplexContainerOutput(values ...any) []byte {
	var buffer bytes.Buffer
	for index := 0; index < len(values); index += 2 {
		stream := values[index].(stdcopy.StdType)
		payload := []byte(values[index+1].(string))
		header := make([]byte, 8)
		header[0] = byte(stream)
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		buffer.Write(header)
		buffer.Write(payload)
	}
	return buffer.Bytes()
}

func bufferedWaitResponses(values ...mobycontainer.WaitResponse) chan mobycontainer.WaitResponse {
	responses := make(chan mobycontainer.WaitResponse, len(values))
	for _, value := range values {
		responses <- value
	}
	return responses
}

func bufferedWaitErrors(values ...error) chan error {
	errors := make(chan error, len(values))
	for _, value := range values {
		errors <- value
	}
	return errors
}

func stringsContain(value string, pattern string) bool {
	return bytes.Contains([]byte(value), []byte(pattern))
}

type fakeContainerEngine struct {
	mutex sync.Mutex

	createID      string
	createOptions mobyclient.ContainerCreateOptions
	attachStream  []byte
	startedID     string
	startErr      error
	inspectPID    int
	waitedID      string
	waitResponses chan mobycontainer.WaitResponse
	waitErrors    chan error
	removedIDs    []string
	removedForce  []bool
	killSignals   []string

	attachWaitDone chan struct{}
}

func (engine *fakeContainerEngine) Ping(context.Context, mobyclient.PingOptions) (mobyclient.PingResult, error) {
	return mobyclient.PingResult{}, nil
}

func (engine *fakeContainerEngine) ContainerCreate(
	_ context.Context,
	options mobyclient.ContainerCreateOptions,
) (mobyclient.ContainerCreateResult, error) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	engine.createOptions = options
	return mobyclient.ContainerCreateResult{ID: engine.createID}, nil
}

func (engine *fakeContainerEngine) ContainerAttach(
	context.Context,
	string,
	mobyclient.ContainerAttachOptions,
) (mobyclient.ContainerAttachResult, error) {
	clientSide, serverSide := net.Pipe()
	go func() {
		if len(engine.attachStream) > 0 {
			_, _ = serverSide.Write(engine.attachStream)
		}
		_ = serverSide.Close()
		if engine.attachWaitDone != nil {
			close(engine.attachWaitDone)
		}
	}()
	return mobyclient.ContainerAttachResult{
		HijackedResponse: mobyclient.NewHijackedResponse(clientSide, "application/vnd.docker.raw-stream"),
	}, nil
}

func (engine *fakeContainerEngine) ContainerStart(
	_ context.Context,
	containerID string,
	_ mobyclient.ContainerStartOptions,
) (mobyclient.ContainerStartResult, error) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	engine.startedID = containerID
	return mobyclient.ContainerStartResult{}, engine.startErr
}

func (engine *fakeContainerEngine) ContainerInspect(
	context.Context,
	string,
	mobyclient.ContainerInspectOptions,
) (mobyclient.ContainerInspectResult, error) {
	return mobyclient.ContainerInspectResult{
		Container: mobycontainer.InspectResponse{State: &mobycontainer.State{Pid: engine.inspectPID}},
	}, nil
}

func (engine *fakeContainerEngine) ContainerWait(
	_ context.Context,
	containerID string,
	_ mobyclient.ContainerWaitOptions,
) mobyclient.ContainerWaitResult {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	engine.waitedID = containerID
	return mobyclient.ContainerWaitResult{Result: engine.waitResponses, Error: engine.waitErrors}
}

func (engine *fakeContainerEngine) ContainerKill(
	_ context.Context,
	_ string,
	options mobyclient.ContainerKillOptions,
) (mobyclient.ContainerKillResult, error) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	engine.killSignals = append(engine.killSignals, options.Signal)
	return mobyclient.ContainerKillResult{}, nil
}

func (engine *fakeContainerEngine) ContainerRemove(
	_ context.Context,
	containerID string,
	options mobyclient.ContainerRemoveOptions,
) (mobyclient.ContainerRemoveResult, error) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	engine.removedIDs = append(engine.removedIDs, containerID)
	engine.removedForce = append(engine.removedForce, options.Force)
	return mobyclient.ContainerRemoveResult{}, nil
}
