// Package execution models durable ownership of one native job attempt.
package execution

import (
	"errors"
	"fmt"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

// Status describes the runner-owned lifecycle of one execution attempt.
type Status string

const (
	StatusStarting  Status = "starting"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
)

// CompletionKind records the authoritative terminal outcome of an attempt.
type CompletionKind string

const (
	CompletionSucceeded CompletionKind = "succeeded"
	CompletionFailed    CompletionKind = "failed"
	CompletionCancelled CompletionKind = "cancelled"
)

var (
	ErrInvalidAttempt    = errors.New("invalid execution attempt")
	ErrInvalidCompletion = errors.New("invalid execution completion")
	ErrNotFound          = errors.New("execution attempt not found")
	ErrNotClaimable      = errors.New("job is not claimable for execution")
	ErrOwnerMismatch     = errors.New("execution runner does not own attempt")
	ErrAttemptCompleted  = errors.New("execution attempt is completed")
)

// Attempt is the durable runner and process metadata for one job.
type Attempt struct {
	JobID             job.ID
	Status            Status
	RunnerPID         int
	ProcessPID        int
	ClaimedAt         time.Time
	StartedAt         *time.Time
	HeartbeatAt       time.Time
	CancelRequestedAt *time.Time
	FinishedAt        *time.Time
	ExitCode          *int
	TerminationSignal string
	Completion        CompletionKind
}

// Validate checks an attempt reconstructed from persistence or protocol data.
func (attempt Attempt) Validate() error {
	if !attempt.JobID.Valid() {
		return fmt.Errorf("%w: %w", ErrInvalidAttempt, job.ErrInvalidID)
	}
	if attempt.RunnerPID <= 0 {
		return fmt.Errorf("%w: runner PID must be positive", ErrInvalidAttempt)
	}
	if attempt.ClaimedAt.IsZero() || attempt.HeartbeatAt.IsZero() {
		return fmt.Errorf("%w: claim and heartbeat times are required", ErrInvalidAttempt)
	}
	if attempt.HeartbeatAt.Before(attempt.ClaimedAt) {
		return fmt.Errorf("%w: heartbeat precedes claim", ErrInvalidAttempt)
	}
	if attempt.CancelRequestedAt != nil && attempt.CancelRequestedAt.Before(attempt.ClaimedAt) {
		return fmt.Errorf("%w: cancellation precedes claim", ErrInvalidAttempt)
	}

	switch attempt.Status {
	case StatusStarting:
		if attempt.ProcessPID != 0 || attempt.StartedAt != nil || attempt.FinishedAt != nil ||
			attempt.ExitCode != nil || attempt.TerminationSignal != "" || attempt.Completion != "" {
			return fmt.Errorf("%w: starting attempt has process or completion fields", ErrInvalidAttempt)
		}
	case StatusRunning:
		if attempt.ProcessPID <= 0 || attempt.StartedAt == nil {
			return fmt.Errorf("%w: running attempt requires process metadata", ErrInvalidAttempt)
		}
		if attempt.StartedAt.Before(attempt.ClaimedAt) {
			return fmt.Errorf("%w: process start precedes claim", ErrInvalidAttempt)
		}
		if attempt.FinishedAt != nil || attempt.ExitCode != nil || attempt.Completion != "" {
			return fmt.Errorf("%w: running attempt has completion fields", ErrInvalidAttempt)
		}
	case StatusCompleted:
		if attempt.FinishedAt == nil || attempt.Completion == "" {
			return fmt.Errorf("%w: completed attempt requires terminal metadata", ErrInvalidAttempt)
		}
		if attempt.FinishedAt.Before(attempt.ClaimedAt) {
			return fmt.Errorf("%w: completion precedes claim", ErrInvalidAttempt)
		}
		if attempt.ProcessPID > 0 && attempt.StartedAt == nil {
			return fmt.Errorf("%w: completed process requires start time", ErrInvalidAttempt)
		}
		switch attempt.Completion {
		case CompletionSucceeded:
			if attempt.ExitCode == nil || *attempt.ExitCode != 0 || attempt.TerminationSignal != "" {
				return fmt.Errorf("%w: successful attempt requires exit code zero", ErrInvalidAttempt)
			}
		case CompletionFailed, CompletionCancelled:
		default:
			return fmt.Errorf("%w: unsupported completion %q", ErrInvalidAttempt, attempt.Completion)
		}
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidAttempt, attempt.Status)
	}
	return nil
}

// Completion describes the terminal information committed with a job state.
type Completion struct {
	At                time.Time
	ExitCode          *int
	TerminationSignal string
	Failure           *job.Failure
	Cancelled         bool
}

// Validate checks that completion represents exactly one terminal outcome.
func (completion Completion) Validate() error {
	if completion.At.IsZero() {
		return fmt.Errorf("%w: completion time is required", ErrInvalidCompletion)
	}
	if completion.Failure != nil {
		if completion.Cancelled {
			return fmt.Errorf("%w: cancellation cannot also be a failure", ErrInvalidCompletion)
		}
		if err := completion.Failure.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidCompletion, err)
		}
		return nil
	}
	if completion.Cancelled {
		return nil
	}
	if completion.ExitCode == nil || *completion.ExitCode != 0 || completion.TerminationSignal != "" {
		return fmt.Errorf("%w: success requires exit code zero", ErrInvalidCompletion)
	}
	return nil
}

// Kind returns the terminal attempt and job outcome.
func (completion Completion) Kind() CompletionKind {
	switch {
	case completion.Cancelled:
		return CompletionCancelled
	case completion.Failure != nil:
		return CompletionFailed
	default:
		return CompletionSucceeded
	}
}
