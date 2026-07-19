package execution

import (
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

func TestAttemptValidation(t *testing.T) {
	claimed := time.Unix(1_700_000_000, 0).UTC()
	started := claimed.Add(time.Second)
	finished := started.Add(time.Second)
	exitCode := 0
	valid := Attempt{
		JobID:       "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		Status:      StatusCompleted,
		RunnerPID:   100,
		ProcessPID:  101,
		ClaimedAt:   claimed,
		StartedAt:   &started,
		HeartbeatAt: finished,
		FinishedAt:  &finished,
		ExitCode:    &exitCode,
		Completion:  CompletionSucceeded,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := valid
	invalid.ExitCode = nil
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("Validate() error = %v, want ErrInvalidAttempt", err)
	}
}

func TestCompletionValidationAndKind(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	zero := 0
	for _, test := range []struct {
		name  string
		value Completion
		kind  CompletionKind
	}{
		{name: "success", value: Completion{At: now, ExitCode: &zero}, kind: CompletionSucceeded},
		{name: "cancelled", value: Completion{At: now, Cancelled: true}, kind: CompletionCancelled},
		{
			name:  "failed",
			value: Completion{At: now, Failure: &job.Failure{Code: "process_exit", Message: "exit 2"}},
			kind:  CompletionFailed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.value.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if got := test.value.Kind(); got != test.kind {
				t.Fatalf("Kind() = %q, want %q", got, test.kind)
			}
		})
	}

	invalid := Completion{At: now}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidCompletion) {
		t.Fatalf("Validate() error = %v, want ErrInvalidCompletion", err)
	}
}
