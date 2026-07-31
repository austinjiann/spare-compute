package worker

import (
	"fmt"
	"sort"

	"github.com/austinjiann/spare-compute/internal/job"
)

// NativeExecutorStarter adapts the existing host-process starter to the
// executor-starting interface.
type NativeExecutorStarter struct {
	start ProcessStarter
}

// NewNativeExecutorStarter builds the default native executor starter.
func NewNativeExecutorStarter(start ProcessStarter) (*NativeExecutorStarter, error) {
	if start == nil {
		return nil, ErrMissingDependency
	}
	return &NativeExecutorStarter{start: start}, nil
}

// SupportedExecutors reports the execution modes this starter can accept.
func (*NativeExecutorStarter) SupportedExecutors() []job.Executor {
	return []job.Executor{job.ExecutorNative}
}

// Start launches one native job process.
func (starter *NativeExecutorStarter) Start(request StartRequest) (ManagedProcess, error) {
	if starter == nil || starter.start == nil {
		return nil, ErrMissingDependency
	}
	return starter.start(request.Spec, request.Stdout, request.Stderr)
}

// ExecutorSet routes each job to the starter that owns its requested executor.
type ExecutorSet struct {
	starters  map[job.Executor]ExecutorStarter
	supported []job.Executor
}

// NewExecutorSet validates and combines one or more executor starters.
func NewExecutorSet(starters ...ExecutorStarter) (*ExecutorSet, error) {
	if len(starters) == 0 {
		return nil, ErrMissingDependency
	}
	result := &ExecutorSet{
		starters: make(map[job.Executor]ExecutorStarter),
	}
	for _, starter := range starters {
		if starter == nil {
			return nil, ErrMissingDependency
		}
		for _, executor := range starter.SupportedExecutors() {
			if executor != job.ExecutorNative && executor != job.ExecutorContainer {
				return nil, fmt.Errorf("%w: executor %q", job.ErrInvalidSpec, executor)
			}
			if _, exists := result.starters[executor]; exists {
				return nil, fmt.Errorf("%w: duplicate executor %q", job.ErrInvalidSpec, executor)
			}
			result.starters[executor] = starter
			result.supported = append(result.supported, executor)
		}
	}
	if len(result.supported) == 0 {
		return nil, ErrMissingDependency
	}
	sort.Slice(result.supported, func(left, right int) bool {
		return result.supported[left] < result.supported[right]
	})
	return result, nil
}

// SupportedExecutors returns the sorted set of executors this worker can start.
func (set *ExecutorSet) SupportedExecutors() []job.Executor {
	if set == nil {
		return nil
	}
	return append([]job.Executor(nil), set.supported...)
}

// Start dispatches spec to the starter that supports spec.Executor.
func (set *ExecutorSet) Start(request StartRequest) (ManagedProcess, error) {
	if set == nil {
		return nil, ErrMissingDependency
	}
	starter := set.starters[request.Spec.Executor]
	if starter == nil {
		return nil, fmt.Errorf("%w: executor %q is not available on this worker", job.ErrInvalidSpec, request.Spec.Executor)
	}
	return starter.Start(request)
}
