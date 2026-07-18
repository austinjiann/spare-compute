package job

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidJob        = errors.New("invalid job")
	ErrInvalidFailure    = errors.New("invalid job failure")
	ErrTransitionTime    = errors.New("job transition time precedes current state")
	ErrFailureUnexpected = errors.New("failure is only valid for failed, rejected, or lost jobs")
)

// Failure is a structured terminal explanation suitable for logs and UI.
type Failure struct {
	Code      string
	Message   string
	Retryable bool
}

// Validate checks that a failure is safe to persist and present.
func (failure Failure) Validate() error {
	if failure.Code == "" {
		return fmt.Errorf("%w: code is required", ErrInvalidFailure)
	}
	if failure.Message == "" {
		return fmt.Errorf("%w: message is required", ErrInvalidFailure)
	}
	return nil
}

// Job is the current durable view of one job.
type Job struct {
	ID        ID
	Spec      Spec
	State     State
	CreatedAt time.Time
	UpdatedAt time.Time
	Failure   *Failure
}

// Transition is an immutable record of one accepted state change.
type Transition struct {
	JobID   ID
	From    State
	To      State
	At      time.Time
	Failure *Failure
}

// New creates a job in the created state.
func New(id ID, spec Spec, now time.Time) (Job, error) {
	if !id.Valid() {
		return Job{}, fmt.Errorf("%w: %w", ErrInvalidJob, ErrInvalidID)
	}
	if err := spec.Validate(); err != nil {
		return Job{}, fmt.Errorf("%w: %w", ErrInvalidJob, err)
	}
	if now.IsZero() {
		return Job{}, fmt.Errorf("%w: creation time is required", ErrInvalidJob)
	}

	now = now.UTC()
	return Job{
		ID:        id,
		Spec:      spec.Clone(),
		State:     StateCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Apply validates and returns the next job value plus its transition record.
// The receiver is not mutated.
func (current Job) Apply(to State, at time.Time, failure *Failure) (Job, Transition, error) {
	if err := ValidateTransition(current.State, to); err != nil {
		return Job{}, Transition{}, err
	}
	if at.IsZero() || at.Before(current.UpdatedAt) {
		return Job{}, Transition{}, ErrTransitionTime
	}

	storedFailure, err := validateFailureForState(to, failure)
	if err != nil {
		return Job{}, Transition{}, err
	}

	at = at.UTC()
	next := current
	next.Spec = current.Spec.Clone()
	next.State = to
	next.UpdatedAt = at
	next.Failure = storedFailure

	transition := Transition{
		JobID:   current.ID,
		From:    current.State,
		To:      to,
		At:      at,
		Failure: cloneFailure(storedFailure),
	}
	return next, transition, nil
}

func validateFailureForState(state State, failure *Failure) (*Failure, error) {
	requiresFailure := state == StateFailed || state == StateRejected || state == StateLost
	if requiresFailure {
		if failure == nil {
			return nil, fmt.Errorf("%w: %s requires a failure", ErrInvalidFailure, state)
		}
		if err := failure.Validate(); err != nil {
			return nil, err
		}
		return cloneFailure(failure), nil
	}

	if failure != nil {
		return nil, ErrFailureUnexpected
	}
	return nil, nil
}

func cloneFailure(failure *Failure) *Failure {
	if failure == nil {
		return nil
	}
	clone := *failure
	return &clone
}
