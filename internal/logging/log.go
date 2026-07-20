// Package logging owns durable stdout and stderr records for ComputeHop jobs.
package logging

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
)

// Stream identifies the child-process output stream for a record.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

const (
	DefaultPageLimit  = 32
	MaximumPageLimit  = 32
	MaximumChunkBytes = 16 * 1024
)

var (
	ErrInvalidRecord = errors.New("invalid job log record")
	ErrInvalidPage   = errors.New("invalid job log page")
	ErrNotFound      = errors.New("job log not found")
	ErrConflict      = errors.New("job log changed concurrently")
	ErrCorrupt       = errors.New("job log data is corrupt")
)

// Metadata points to one committed range in the append-only log data file.
type Metadata struct {
	JobID      job.ID
	Sequence   uint64
	Stream     Stream
	DataOffset int64
	DataLength int
	At         time.Time
}

// Validate checks metadata reconstructed from persistence.
func (metadata Metadata) Validate() error {
	if !metadata.JobID.Valid() || metadata.Sequence == 0 || metadata.Sequence > math.MaxInt64 {
		return fmt.Errorf("%w: invalid identity or sequence", ErrInvalidRecord)
	}
	if metadata.Stream != StreamStdout && metadata.Stream != StreamStderr {
		return fmt.Errorf("%w: unsupported stream %q", ErrInvalidRecord, metadata.Stream)
	}
	if metadata.DataOffset < 0 || metadata.DataLength <= 0 || metadata.DataLength > MaximumChunkBytes {
		return fmt.Errorf("%w: invalid data range", ErrInvalidRecord)
	}
	if metadata.At.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidRecord)
	}
	return nil
}

// Record is one globally sequenced stdout or stderr chunk.
type Record struct {
	Sequence uint64
	Stream   Stream
	Data     []byte
	At       time.Time
}

// Page is one resumable read after a caller-owned sequence offset.
type Page struct {
	Records []Record
	HasMore bool
}

// Cursor is the next externally committed file and sequence position.
type Cursor struct {
	DataOffset   int64
	NextSequence uint64
}

// Repository persists log indexes while bytes remain in an external file.
type Repository interface {
	Cursor(context.Context, job.ID) (Cursor, error)
	Commit(context.Context, job.ID, int64, Stream, int, time.Time) (Metadata, error)
	List(context.Context, job.ID, uint64, int) ([]Metadata, bool, error)
}
