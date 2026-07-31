package worker

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/processes"
)

func TestExecutorSetRoutesByRequestedExecutor(t *testing.T) {
	native := &executorStarterStub{
		executors: []job.Executor{job.ExecutorNative},
		start: func(spec job.Spec, _ io.Writer, _ io.Writer) (ManagedProcess, error) {
			if spec.Executor != job.ExecutorNative {
				t.Fatalf("native starter received %#v", spec)
			}
			return fakeManagedProcess{pid: 10}, nil
		},
	}
	container := &executorStarterStub{
		executors: []job.Executor{job.ExecutorContainer},
		start: func(spec job.Spec, _ io.Writer, _ io.Writer) (ManagedProcess, error) {
			if spec.Executor != job.ExecutorContainer || spec.ContainerImage != "alpine:latest" {
				t.Fatalf("container starter received %#v", spec)
			}
			return fakeManagedProcess{pid: 20}, nil
		},
	}
	set, err := NewExecutorSet(container, native)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.SupportedExecutors(), []job.Executor{job.ExecutorContainer, job.ExecutorNative}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedExecutors() = %#v, want %#v", got, want)
	}

	started, err := set.Start(StartRequest{
		Spec: job.Spec{
			Executable: "echo", Executor: job.ExecutorContainer, ContainerImage: "alpine:latest",
		},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.PID() != 20 {
		t.Fatalf("PID() = %d, want 20", started.PID())
	}
}

func TestExecutorSetRejectsUnavailableOrDuplicateExecutors(t *testing.T) {
	native := &executorStarterStub{executors: []job.Executor{job.ExecutorNative}}
	if _, err := NewExecutorSet(native, native); !errors.Is(err, job.ErrInvalidSpec) {
		t.Fatalf("duplicate executor error = %v", err)
	}
	set, err := NewExecutorSet(native)
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.Start(StartRequest{
		Spec: job.Spec{
			Executable: "echo", Executor: job.ExecutorContainer, ContainerImage: "alpine:latest",
		},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if !errors.Is(err, job.ErrInvalidSpec) {
		t.Fatalf("unavailable executor error = %v", err)
	}
}

func TestNativeExecutorStarterReportsNativeAndDelegates(t *testing.T) {
	var got job.Spec
	starter, err := NewNativeExecutorStarter(func(spec job.Spec, _ io.Writer, _ io.Writer) (ManagedProcess, error) {
		got = spec
		return fakeManagedProcess{pid: 42}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotExecutors := starter.SupportedExecutors(); !reflect.DeepEqual(gotExecutors, []job.Executor{job.ExecutorNative}) {
		t.Fatalf("SupportedExecutors() = %#v", gotExecutors)
	}
	started, err := starter.Start(StartRequest{
		Spec: job.Spec{Executable: "echo", Executor: job.ExecutorNative}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Executor != job.ExecutorNative || started.PID() != 42 {
		t.Fatalf("spec = %#v; process PID = %d", got, started.PID())
	}
}

type executorStarterStub struct {
	executors []job.Executor
	start     func(job.Spec, io.Writer, io.Writer) (ManagedProcess, error)
}

func (starter *executorStarterStub) SupportedExecutors() []job.Executor {
	return append([]job.Executor(nil), starter.executors...)
}

func (starter *executorStarterStub) Start(request StartRequest) (ManagedProcess, error) {
	if starter.start != nil {
		return starter.start(request.Spec, request.Stdout, request.Stderr)
	}
	return fakeManagedProcess{pid: 1}, nil
}

type fakeManagedProcess struct {
	pid int
}

func (process fakeManagedProcess) PID() int {
	return process.pid
}

func (fakeManagedProcess) GracefulStop() error {
	return nil
}

func (fakeManagedProcess) Kill() error {
	return nil
}

func (fakeManagedProcess) Wait() processes.Exit {
	return processes.Exit{Code: 0}
}
