package worker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

func TestSubmitDurablyQueuesJob(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)

	queued, err := service.Submit(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if queued.State != job.StateQueued {
		t.Fatalf("Submit() state = %s, want %s", queued.State, job.StateQueued)
	}

	stored, err := repository.Get(context.Background(), queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(stored, queued) {
		t.Fatalf("stored job = %#v, want %#v", stored, queued)
	}

	if got, want := transitionStates(repository.transitions), []job.State{job.StateValidating, job.StateQueued}; !reflect.DeepEqual(got, want) {
		t.Fatalf("transition states = %v, want %v", got, want)
	}
}

func TestSubmitRejectsInvalidSpecBeforePersistence(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)

	_, err := service.Submit(context.Background(), job.Spec{Executor: job.ExecutorNative})
	if !errors.Is(err, job.ErrInvalidSpec) {
		t.Fatalf("Submit() error = %v, want job.ErrInvalidSpec", err)
	}
	if len(repository.jobs) != 0 {
		t.Fatalf("Submit() persisted an invalid job")
	}
}

func TestSubmitPropagatesIDGenerationFailure(t *testing.T) {
	service, err := NewJobService(Dependencies{
		Jobs: newMemoryRepository(),
		GenerateID: func() (job.ID, error) {
			return "", errors.New("entropy unavailable")
		},
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewJobService() error = %v", err)
	}

	if _, err := service.Submit(context.Background(), validSpec()); err == nil {
		t.Fatalf("Submit() error = nil, want ID generation failure")
	}
}

func TestAdvanceAndIdempotentCancel(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)
	current, err := service.Submit(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	for _, state := range []job.State{job.StateStarting, job.StateRunning} {
		current, err = service.Advance(context.Background(), current.ID, state, nil)
		if err != nil {
			t.Fatalf("Advance(%s) error = %v", state, err)
		}
	}
	cancelled, err := service.Cancel(context.Background(), current.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.State != job.StateCancelled {
		t.Fatalf("Cancel() state = %s, want %s", cancelled.State, job.StateCancelled)
	}

	transitionCount := len(repository.transitions)
	again, err := service.Cancel(context.Background(), current.ID)
	if err != nil {
		t.Fatalf("second Cancel() error = %v", err)
	}
	if !reflect.DeepEqual(again, cancelled) {
		t.Fatalf("second Cancel() = %#v, want %#v", again, cancelled)
	}
	if len(repository.transitions) != transitionCount {
		t.Fatalf("second Cancel() added another transition")
	}
}

func TestCancelRejectsOtherTerminalStates(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)
	current, err := service.Submit(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	for _, state := range []job.State{job.StateStarting, job.StateRunning, job.StateSucceeded} {
		current, err = service.Advance(context.Background(), current.ID, state, nil)
		if err != nil {
			t.Fatalf("Advance(%s) error = %v", state, err)
		}
	}

	if _, err := service.Cancel(context.Background(), current.ID); !errors.Is(err, ErrJobTerminal) {
		t.Fatalf("Cancel() error = %v, want ErrJobTerminal", err)
	}
}

func TestGetAndListUseRepository(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository)
	queued, err := service.Submit(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	loaded, err := service.Get(context.Background(), queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, queued) {
		t.Fatalf("Get() = %#v, want %#v", loaded, queued)
	}

	listed, err := service.List(context.Background(), job.ListOptions{States: []job.State{job.StateQueued}})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != queued.ID {
		t.Fatalf("List() = %#v, want queued job", listed)
	}
}

func TestNewJobServiceRequiresDependencies(t *testing.T) {
	valid := Dependencies{
		Jobs:       newMemoryRepository(),
		GenerateID: job.NewID,
		Now:        time.Now,
	}

	for _, test := range []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{"jobs", func(value *Dependencies) { value.Jobs = nil }},
		{"ID generator", func(value *Dependencies) { value.GenerateID = nil }},
		{"clock", func(value *Dependencies) { value.Now = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependencies := valid
			test.mutate(&dependencies)
			if _, err := NewJobService(dependencies); !errors.Is(err, ErrMissingDependency) {
				t.Fatalf("NewJobService() error = %v, want ErrMissingDependency", err)
			}
		})
	}
}

func newTestService(t *testing.T, repository job.Repository) *JobService {
	t.Helper()
	id := mustJobID(t, 1)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	service, err := NewJobService(Dependencies{
		Jobs:       repository,
		GenerateID: func() (job.ID, error) { return id, nil },
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	})
	if err != nil {
		t.Fatalf("NewJobService() error = %v", err)
	}
	return service
}

func validSpec() job.Spec {
	return job.Spec{
		Executable: "echo",
		Arguments:  []string{"hello"},
		Executor:   job.ExecutorNative,
	}
}

func mustJobID(t *testing.T, sequence int) job.ID {
	t.Helper()
	value := fmt.Sprintf("019abcdf-0123-4567-89ab-%012x", sequence)
	id, err := job.ParseID(value)
	if err != nil {
		t.Fatalf("job.ParseID(%q) error = %v", value, err)
	}
	return id
}

func transitionStates(transitions []job.Transition) []job.State {
	states := make([]job.State, len(transitions))
	for index, transition := range transitions {
		states[index] = transition.To
	}
	return states
}

type memoryRepository struct {
	mu          sync.Mutex
	jobs        map[job.ID]job.Job
	transitions []job.Transition
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{jobs: make(map[job.ID]job.Job)}
}

func (repository *memoryRepository) Create(_ context.Context, value job.Job) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.jobs[value.ID]; exists {
		return job.ErrConflict
	}
	repository.jobs[value.ID] = value
	return nil
}

func (repository *memoryRepository) Get(_ context.Context, id job.ID) (job.Job, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, exists := repository.jobs[id]
	if !exists {
		return job.Job{}, job.ErrNotFound
	}
	return value, nil
}

func (repository *memoryRepository) List(_ context.Context, options job.ListOptions) ([]job.Job, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	states := make(map[job.State]struct{}, len(options.States))
	for _, state := range options.States {
		states[state] = struct{}{}
	}
	result := make([]job.Job, 0, len(repository.jobs))
	for _, value := range repository.jobs {
		if len(states) > 0 {
			if _, included := states[value.State]; !included {
				continue
			}
		}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].UpdatedAt.After(result[right].UpdatedAt)
	})
	if options.Limit > 0 && len(result) > options.Limit {
		result = result[:options.Limit]
	}
	return result, nil
}

func (repository *memoryRepository) ApplyTransition(_ context.Context, transition job.Transition) (job.Job, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.jobs[transition.JobID]
	if !exists {
		return job.Job{}, job.ErrNotFound
	}
	if current.State != transition.From {
		return job.Job{}, job.ErrConflict
	}
	next, accepted, err := current.Apply(transition.To, transition.At, transition.Failure)
	if err != nil {
		return job.Job{}, err
	}
	repository.jobs[next.ID] = next
	repository.transitions = append(repository.transitions, accepted)
	return next, nil
}
