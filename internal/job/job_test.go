package job

import (
	"errors"
	"testing"
	"time"
)

func TestNewAndApplyTransition(t *testing.T) {
	id := mustParseID(t, "019abcdf-0123-4567-89ab-0123456789ab")
	createdAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.FixedZone("test", -4*60*60))
	job, err := New(id, Spec{Executable: "echo", Arguments: []string{"hello"}, Executor: ExecutorNative}, createdAt)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if job.State != StateCreated {
		t.Fatalf("New() state = %s, want %s", job.State, StateCreated)
	}
	if job.CreatedAt.Location() != time.UTC || job.UpdatedAt.Location() != time.UTC {
		t.Fatalf("New() did not normalize timestamps to UTC")
	}

	transitionAt := createdAt.Add(time.Second)
	next, transition, err := job.Apply(StateValidating, transitionAt, nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if next.State != StateValidating || transition.From != StateCreated || transition.To != StateValidating {
		t.Fatalf("Apply() returned incorrect state or transition: next=%s transition=%s->%s", next.State, transition.From, transition.To)
	}
	if job.State != StateCreated {
		t.Fatalf("Apply() mutated receiver state to %s", job.State)
	}
}

func TestApplyFailureCopiesFailure(t *testing.T) {
	job := mustJobInState(t, StateRunning)
	failure := &Failure{Code: "process_exit", Message: "process exited with status 1", Retryable: false}
	next, transition, err := job.Apply(StateFailed, job.UpdatedAt.Add(time.Second), failure)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	failure.Message = "changed by caller"
	if next.Failure.Message != "process exited with status 1" {
		t.Fatalf("next job shared failure with caller")
	}
	if transition.Failure.Message != "process exited with status 1" {
		t.Fatalf("transition shared failure with caller")
	}
}

func TestApplyRequiresFailureForFailureStates(t *testing.T) {
	job := mustJobInState(t, StateRunning)
	for _, state := range []State{StateFailed, StateLost} {
		_, _, err := job.Apply(state, job.UpdatedAt.Add(time.Second), nil)
		if !errors.Is(err, ErrInvalidFailure) {
			t.Fatalf("Apply(%s) error = %v, want ErrInvalidFailure", state, err)
		}
	}
}

func TestApplyRejectsFailureForSuccessfulState(t *testing.T) {
	job := mustJobInState(t, StateRunning)
	_, _, err := job.Apply(
		StateSucceeded,
		job.UpdatedAt.Add(time.Second),
		&Failure{Code: "unexpected", Message: "should not be accepted"},
	)
	if !errors.Is(err, ErrFailureUnexpected) {
		t.Fatalf("Apply() error = %v, want ErrFailureUnexpected", err)
	}
}

func TestApplyRejectsTimeBeforeCurrentState(t *testing.T) {
	job := mustJobInState(t, StateRunning)
	_, _, err := job.Apply(StateSucceeded, job.UpdatedAt.Add(-time.Second), nil)
	if !errors.Is(err, ErrTransitionTime) {
		t.Fatalf("Apply() error = %v, want ErrTransitionTime", err)
	}
}

func TestValidateRejectsInvalidRehydratedJob(t *testing.T) {
	valid := mustJobInState(t, StateRunning)

	for _, test := range []struct {
		name   string
		mutate func(*Job)
	}{
		{
			name: "invalid ID",
			mutate: func(value *Job) {
				value.ID = "not-an-id"
			},
		},
		{
			name: "invalid state",
			mutate: func(value *Job) {
				value.State = "paused"
			},
		},
		{
			name: "backwards timestamps",
			mutate: func(value *Job) {
				value.UpdatedAt = value.CreatedAt.Add(-time.Second)
			},
		},
		{
			name: "unexpected failure",
			mutate: func(value *Job) {
				value.Failure = &Failure{Code: "unexpected", Message: "unexpected"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidJob) {
				t.Fatalf("Validate() error = %v, want ErrInvalidJob", err)
			}
		})
	}
}

func mustJobInState(t *testing.T, target State) Job {
	t.Helper()
	id := mustParseID(t, "019abcdf-0123-4567-89ab-0123456789ab")
	now := time.Date(2026, time.July, 18, 16, 0, 0, 0, time.UTC)
	current, err := New(id, Spec{Executable: "echo", Executor: ExecutorNative}, now)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	path := []State{StateValidating, StateQueued, StateStarting, StateRunning}
	for _, state := range path {
		current, _, err = current.Apply(state, current.UpdatedAt.Add(time.Second), nil)
		if err != nil {
			t.Fatalf("Apply(%s) error = %v", state, err)
		}
		if state == target {
			return current
		}
	}

	t.Fatalf("test helper does not support target state %s", target)
	return Job{}
}

func mustParseID(t *testing.T, value string) ID {
	t.Helper()
	id, err := ParseID(value)
	if err != nil {
		t.Fatalf("ParseID(%q) error = %v", value, err)
	}
	return id
}
