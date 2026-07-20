package placement

import (
	"errors"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
)

func TestPlacementValidate(t *testing.T) {
	valid := Placement{
		JobID:    job.ID("7a338fa3-7ba4-4c54-bf59-da1161f6b76f"),
		WorkerID: device.ID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		PlacedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalid := []Placement{
		{},
		{JobID: valid.JobID, WorkerID: valid.WorkerID},
		{JobID: "bad", WorkerID: valid.WorkerID, PlacedAt: valid.PlacedAt},
		{JobID: valid.JobID, WorkerID: "bad", PlacedAt: valid.PlacedAt},
	}
	for _, value := range invalid {
		if err := value.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Validate(%#v) error = %v, want ErrInvalid", value, err)
		}
	}
}
