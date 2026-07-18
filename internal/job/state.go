package job

import (
	"errors"
	"fmt"
)

// State is the durable lifecycle phase of a job.
type State string

const (
	StateCreated      State = "created"
	StateValidating   State = "validating"
	StateQueued       State = "queued"
	StateSnapshotting State = "snapshotting"
	StateTransferring State = "transferring"
	StateStarting     State = "starting"
	StateRunning      State = "running"
	StateCollecting   State = "collecting"
	StateRestoring    State = "restoring"
	StateSucceeded    State = "succeeded"
	StateFailed       State = "failed"
	StateCancelled    State = "cancelled"
	StateRejected     State = "rejected"
	StateLost         State = "lost"
)

var (
	ErrInvalidState      = errors.New("invalid job state")
	ErrInvalidTransition = errors.New("invalid job state transition")
)

var validStates = map[State]struct{}{
	StateCreated:      {},
	StateValidating:   {},
	StateQueued:       {},
	StateSnapshotting: {},
	StateTransferring: {},
	StateStarting:     {},
	StateRunning:      {},
	StateCollecting:   {},
	StateRestoring:    {},
	StateSucceeded:    {},
	StateFailed:       {},
	StateCancelled:    {},
	StateRejected:     {},
	StateLost:         {},
}

var terminalStates = map[State]struct{}{
	StateSucceeded: {},
	StateFailed:    {},
	StateCancelled: {},
	StateRejected:  {},
	StateLost:      {},
}

var allowedTransitions = map[State]map[State]struct{}{
	StateCreated: transitionsTo(
		StateValidating,
		StateCancelled,
	),
	StateValidating: transitionsTo(
		StateQueued,
		StateRejected,
		StateFailed,
		StateCancelled,
	),
	StateQueued: transitionsTo(
		StateSnapshotting,
		StateStarting,
		StateFailed,
		StateCancelled,
	),
	StateSnapshotting: transitionsTo(
		StateTransferring,
		StateStarting,
		StateFailed,
		StateCancelled,
	),
	StateTransferring: transitionsTo(
		StateStarting,
		StateFailed,
		StateCancelled,
		StateLost,
	),
	StateStarting: transitionsTo(
		StateRunning,
		StateFailed,
		StateCancelled,
		StateLost,
	),
	StateRunning: transitionsTo(
		StateCollecting,
		StateSucceeded,
		StateFailed,
		StateCancelled,
		StateLost,
	),
	StateCollecting: transitionsTo(
		StateRestoring,
		StateSucceeded,
		StateFailed,
		StateCancelled,
		StateLost,
	),
	StateRestoring: transitionsTo(
		StateSucceeded,
		StateFailed,
		StateCancelled,
	),
}

// ParseState validates a state received from an external boundary.
func ParseState(value string) (State, error) {
	state := State(value)
	if !state.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidState, value)
	}
	return state, nil
}

// Valid reports whether state is part of the versioned job lifecycle.
func (state State) Valid() bool {
	_, ok := validStates[state]
	return ok
}

// Terminal reports whether no further state transition is permitted.
func (state State) Terminal() bool {
	_, ok := terminalStates[state]
	return ok
}

// CanTransition reports whether a direct transition is legal.
func CanTransition(from, to State) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	_, ok := allowedTransitions[from][to]
	return ok
}

// ValidateTransition returns a descriptive error when a transition is illegal.
func ValidateTransition(from, to State) error {
	if !from.Valid() {
		return fmt.Errorf("%w: source %q", ErrInvalidState, from)
	}
	if !to.Valid() {
		return fmt.Errorf("%w: destination %q", ErrInvalidState, to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, to)
	}
	return nil
}

func transitionsTo(states ...State) map[State]struct{} {
	result := make(map[State]struct{}, len(states))
	for _, state := range states {
		result[state] = struct{}{}
	}
	return result
}
