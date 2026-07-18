package job

import (
	"errors"
	"testing"
)

func TestPrimaryJobLifecycle(t *testing.T) {
	states := []State{
		StateCreated,
		StateValidating,
		StateQueued,
		StateSnapshotting,
		StateTransferring,
		StateStarting,
		StateRunning,
		StateCollecting,
		StateRestoring,
		StateSucceeded,
	}

	for index := 0; index < len(states)-1; index++ {
		if err := ValidateTransition(states[index], states[index+1]); err != nil {
			t.Fatalf("ValidateTransition(%s, %s) error = %v", states[index], states[index+1], err)
		}
	}
}

func TestTerminalStatesRejectEveryTransition(t *testing.T) {
	terminal := []State{
		StateSucceeded,
		StateFailed,
		StateCancelled,
		StateRejected,
		StateLost,
	}

	for _, from := range terminal {
		if !from.Terminal() {
			t.Fatalf("%s.Terminal() = false", from)
		}
		for to := range validStates {
			if CanTransition(from, to) {
				t.Fatalf("CanTransition(%s, %s) = true for terminal state", from, to)
			}
		}
	}
}

func TestValidateTransitionRejectsSkipsAndSelfTransitions(t *testing.T) {
	for _, test := range []struct {
		from State
		to   State
	}{
		{StateCreated, StateRunning},
		{StateQueued, StateQueued},
		{StateRunning, StateStarting},
	} {
		err := ValidateTransition(test.from, test.to)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("ValidateTransition(%s, %s) error = %v, want ErrInvalidTransition", test.from, test.to, err)
		}
	}
}

func TestParseStateRejectsUnknownValue(t *testing.T) {
	_, err := ParseState("paused")
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ParseState() error = %v, want ErrInvalidState", err)
	}
}
