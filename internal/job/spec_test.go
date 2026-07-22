package job

import (
	"errors"
	"testing"
)

func TestSpecValidate(t *testing.T) {
	valid := Spec{
		Executable:       "cargo",
		Arguments:        []string{"build", "--release"},
		WorkingDirectory: "project",
		Environment:      map[string]string{"CARGO_TERM_COLOR": "always"},
		Executor:         ExecutorNative,
		Outputs:          []string{"target/release/app"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSpecValidateRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct {
		name string
		spec Spec
	}{
		{
			name: "missing executable",
			spec: Spec{Executor: ExecutorNative},
		},
		{
			name: "argument NUL",
			spec: Spec{Executable: "echo", Arguments: []string{"bad\x00arg"}, Executor: ExecutorNative},
		},
		{
			name: "environment equals",
			spec: Spec{Executable: "echo", Environment: map[string]string{"BAD=NAME": "value"}, Executor: ExecutorNative},
		},
		{
			name: "native image",
			spec: Spec{Executable: "echo", Executor: ExecutorNative, ContainerImage: "example/image"},
		},
		{
			name: "container missing image",
			spec: Spec{Executable: "echo", Executor: ExecutorContainer},
		},
		{
			name: "unknown executor",
			spec: Spec{Executable: "echo", Executor: "remote-shell"},
		},
		{
			name: "output traversal",
			spec: Spec{Executable: "echo", Executor: ExecutorNative, Outputs: []string{"../secret"}},
		},
		{
			name: "output portable collision",
			spec: Spec{Executable: "echo", Executor: ExecutorNative, Outputs: []string{"Build/app", "build/app"}},
		},
		{
			name: "reserved result output",
			spec: Spec{Executable: "echo", Executor: ExecutorNative, Outputs: []string{".computehop-results/file"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.spec.Validate()
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Validate() error = %v, want ErrInvalidSpec", err)
			}
		})
	}
}

func TestSpecCloneDoesNotShareMutableFields(t *testing.T) {
	original := Spec{
		Executable:  "echo",
		Arguments:   []string{"hello"},
		Environment: map[string]string{"NAME": "original"},
		Executor:    ExecutorNative,
		Outputs:     []string{"result.txt"},
	}
	clone := original.Clone()
	clone.Arguments[0] = "changed"
	clone.Environment["NAME"] = "changed"
	clone.Outputs[0] = "changed.txt"

	if original.Arguments[0] != "hello" {
		t.Fatalf("Clone() shared argument storage")
	}
	if original.Environment["NAME"] != "original" {
		t.Fatalf("Clone() shared environment storage")
	}
	if original.Outputs[0] != "result.txt" {
		t.Fatalf("Clone() shared output storage")
	}
}
