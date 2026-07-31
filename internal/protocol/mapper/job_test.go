package mapper

import (
	"errors"
	"reflect"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/job"
)

func TestJobProtocolRoundTrip(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 123).UTC()
	updatedAt := createdAt.Add(time.Second)
	want := job.Job{
		ID: "7a338fa3-7ba4-4c54-bf59-da1161f6b76f",
		Spec: job.Spec{
			Executable:       "cargo",
			Arguments:        []string{"build", "--release"},
			WorkingDirectory: "/project",
			Environment:      map[string]string{"RUST_BACKTRACE": "1"},
			Executor:         job.ExecutorNative,
			RequiredToolIDs:  []string{"cargo", "rustc"},
		},
		State:     job.StateFailed,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Failure: &job.Failure{
			Code:      "exit_nonzero",
			Message:   "command exited with status 1",
			Retryable: false,
		},
	}

	message, err := JobToProto(want)
	if err != nil {
		t.Fatalf("JobToProto() error = %v", err)
	}
	got, err := JobFromProto(message)
	if err != nil {
		t.Fatalf("JobFromProto() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestProtocolMappersRejectUnknownEnums(t *testing.T) {
	_, err := SpecFromProto(&localv1.JobSpec{
		Executable: "echo",
		Executor:   localv1.Executor(99),
	})
	if !errors.Is(err, job.ErrInvalidSpec) {
		t.Fatalf("SpecFromProto() error = %v, want ErrInvalidSpec", err)
	}

	_, err = StatesFromProto([]localv1.JobState{localv1.JobState(99)})
	if !errors.Is(err, job.ErrInvalidState) {
		t.Fatalf("StatesFromProto() error = %v, want ErrInvalidState", err)
	}
}

func TestSpecFromProtoCopiesCollections(t *testing.T) {
	message := &localv1.JobSpec{
		Executable:      "echo",
		Arguments:       []string{"hello"},
		Environment:     map[string]string{"NAME": "value"},
		Executor:        localv1.Executor_EXECUTOR_NATIVE,
		RequiredToolIds: []string{"echo"},
	}
	spec, err := SpecFromProto(message)
	if err != nil {
		t.Fatalf("SpecFromProto() error = %v", err)
	}
	message.Arguments[0] = "changed"
	message.Environment["NAME"] = "changed"
	message.RequiredToolIds[0] = "changed"
	if spec.Arguments[0] != "hello" ||
		spec.Environment["NAME"] != "value" ||
		spec.RequiredToolIDs[0] != "echo" {
		t.Fatal("SpecFromProto() retained mutable protocol collections")
	}
}
