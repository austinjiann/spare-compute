package job

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/austinjiann/spare-compute/internal/portablepath"
)

// Executor identifies the execution environment required by a job.
type Executor string

const (
	ExecutorNative    Executor = "native"
	ExecutorContainer Executor = "container"
)

var ErrInvalidSpec = errors.New("invalid job specification")

// Spec describes a command without invoking a shell. Arguments remain separate
// so callers cannot accidentally change command meaning through shell quoting.
type Spec struct {
	Executable       string
	Arguments        []string
	WorkingDirectory string
	Environment      map[string]string
	Executor         Executor
	ContainerImage   string
	Outputs          []string
	RequiredToolIDs  []string
}

const (
	MaximumOutputs         = 64
	MaximumRequiredToolIDs = 128
)

// Validate checks the technology-independent parts of a job specification.
func (spec Spec) Validate() error {
	if strings.TrimSpace(spec.Executable) == "" {
		return fmt.Errorf("%w: executable is required", ErrInvalidSpec)
	}
	if containsNUL(spec.Executable) {
		return fmt.Errorf("%w: executable contains a NUL byte", ErrInvalidSpec)
	}
	if containsNUL(spec.WorkingDirectory) {
		return fmt.Errorf("%w: working directory contains a NUL byte", ErrInvalidSpec)
	}

	for index, argument := range spec.Arguments {
		if containsNUL(argument) {
			return fmt.Errorf("%w: argument %d contains a NUL byte", ErrInvalidSpec, index)
		}
	}

	for name, value := range spec.Environment {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("%w: invalid environment variable name %q", ErrInvalidSpec, name)
		}
		if containsNUL(value) {
			return fmt.Errorf("%w: environment variable %q contains a NUL byte", ErrInvalidSpec, name)
		}
	}

	if len(spec.Outputs) > MaximumOutputs {
		return fmt.Errorf("%w: outputs cannot exceed %d", ErrInvalidSpec, MaximumOutputs)
	}
	outputs := make(map[string]struct{}, len(spec.Outputs))
	for index, output := range spec.Outputs {
		if portablepath.Validate(output) != nil {
			return fmt.Errorf("%w: output %d is not a portable relative path", ErrInvalidSpec, index)
		}
		key := portablepath.Key(output)
		if reservedOutputPath(key) {
			return fmt.Errorf("%w: output %q uses a reserved path", ErrInvalidSpec, output)
		}
		if _, exists := outputs[key]; exists {
			return fmt.Errorf("%w: duplicate output %q", ErrInvalidSpec, output)
		}
		outputs[key] = struct{}{}
	}

	if len(spec.RequiredToolIDs) > MaximumRequiredToolIDs {
		return fmt.Errorf("%w: required tools cannot exceed %d", ErrInvalidSpec, MaximumRequiredToolIDs)
	}
	if !slices.IsSorted(spec.RequiredToolIDs) {
		return fmt.Errorf("%w: required tools must be sorted", ErrInvalidSpec)
	}
	previousToolID := ""
	for index, toolID := range spec.RequiredToolIDs {
		if invalidToolID(toolID) {
			return fmt.Errorf("%w: invalid required tool %d", ErrInvalidSpec, index)
		}
		if toolID == previousToolID {
			return fmt.Errorf("%w: duplicate required tool %q", ErrInvalidSpec, toolID)
		}
		previousToolID = toolID
	}

	switch spec.Executor {
	case ExecutorNative:
		if spec.ContainerImage != "" {
			return fmt.Errorf("%w: native executor cannot use a container image", ErrInvalidSpec)
		}
	case ExecutorContainer:
		if strings.TrimSpace(spec.ContainerImage) == "" {
			return fmt.Errorf("%w: container image is required", ErrInvalidSpec)
		}
		if containsNUL(spec.ContainerImage) {
			return fmt.Errorf("%w: container image contains a NUL byte", ErrInvalidSpec)
		}
	default:
		return fmt.Errorf("%w: unsupported executor %q", ErrInvalidSpec, spec.Executor)
	}

	return nil
}

// Clone returns a copy that does not share mutable slices or maps with spec.
func (spec Spec) Clone() Spec {
	clone := spec
	clone.Arguments = append([]string(nil), spec.Arguments...)
	clone.Outputs = append([]string(nil), spec.Outputs...)
	clone.RequiredToolIDs = append([]string(nil), spec.RequiredToolIDs...)
	if spec.Environment != nil {
		clone.Environment = make(map[string]string, len(spec.Environment))
		for name, value := range spec.Environment {
			clone.Environment[name] = value
		}
	}
	return clone
}

func invalidToolID(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.ToLower(value) != value {
		return true
	}
	for _, character := range value {
		if character <= 0x20 || character == 0x7f || character == '=' {
			return true
		}
	}
	return false
}

func reservedOutputPath(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == ".git" || segment == ".computehop-results" || segment == ".computehop-conflicts" {
			return true
		}
	}
	return false
}

func containsNUL(value string) bool {
	return strings.IndexByte(value, 0) >= 0
}
