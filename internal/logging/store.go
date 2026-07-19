package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
)

// Store coordinates external log bytes with a durable metadata repository.
type Store struct {
	stateDir string
	records  Repository
	now      func() time.Time
}

// NewStore constructs a durable log store.
func NewStore(stateDir string, records Repository, now func() time.Time) (*Store, error) {
	if stateDir == "" || records == nil || now == nil {
		return nil, errors.New("log store state directory, repository, and clock are required")
	}
	return &Store{stateDir: stateDir, records: records, now: now}, nil
}

// OpenWriter repairs any unindexed tail and opens the one writer for id.
func (store *Store) OpenWriter(ctx context.Context, id job.ID) (*Writer, error) {
	cursor, err := store.records.Cursor(ctx, id)
	if err != nil {
		return nil, err
	}
	directory, err := paths.JobDataDir(store.stateDir, id)
	if err != nil {
		return nil, err
	}
	if err := permissions.EnsurePrivateDirectory(directory); err != nil {
		return nil, fmt.Errorf("secure job data directory: %w", err)
	}
	path, err := paths.JobLogPath(store.stateDir, id)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		unsafePermissions := runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || unsafePermissions {
			return nil, fmt.Errorf("%w: unsafe log file", ErrCorrupt)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect log data: %w", statErr)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log data: %w", err)
	}
	closeOnError := func(openErr error) (*Writer, error) {
		_ = file.Close()
		return nil, openErr
	}
	info, err := file.Stat()
	if err != nil {
		return closeOnError(fmt.Errorf("stat log data: %w", err))
	}
	if info.Size() < cursor.DataOffset {
		return closeOnError(fmt.Errorf(
			"%w: file has %d bytes but index requires %d",
			ErrCorrupt,
			info.Size(),
			cursor.DataOffset,
		))
	}
	if info.Size() > cursor.DataOffset {
		if err := file.Truncate(cursor.DataOffset); err != nil {
			return closeOnError(fmt.Errorf("remove uncommitted log tail: %w", err))
		}
		if err := file.Sync(); err != nil {
			return closeOnError(fmt.Errorf("sync repaired log data: %w", err))
		}
	}
	return &Writer{
		file:    file,
		jobID:   id,
		offset:  cursor.DataOffset,
		records: store.records,
		now:     store.now,
	}, nil
}

// Read returns a bounded page after sequence and verifies every external range.
func (store *Store) Read(ctx context.Context, id job.ID, after uint64, limit int) (Page, error) {
	if limit == 0 {
		limit = DefaultPageLimit
	}
	if limit < 0 || limit > MaximumPageLimit {
		return Page{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidPage, MaximumPageLimit)
	}
	metadata, hasMore, err := store.records.List(ctx, id, after, limit)
	if err != nil {
		return Page{}, err
	}
	if len(metadata) == 0 {
		return Page{HasMore: hasMore}, nil
	}
	path, err := paths.JobLogPath(store.stateDir, id)
	if err != nil {
		return Page{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Page{}, fmt.Errorf("open job log data: %w", err)
	}
	defer file.Close()

	records := make([]Record, len(metadata))
	for index, item := range metadata {
		if err := item.Validate(); err != nil {
			return Page{}, err
		}
		data := make([]byte, item.DataLength)
		if _, err := file.ReadAt(data, item.DataOffset); err != nil {
			return Page{}, fmt.Errorf("%w: read sequence %d: %v", ErrCorrupt, item.Sequence, err)
		}
		records[index] = Record{
			Sequence: item.Sequence,
			Stream:   item.Stream,
			Data:     data,
			At:       item.At,
		}
	}
	return Page{Records: records, HasMore: hasMore}, nil
}

// Writer serializes stdout and stderr into one globally ordered file.
type Writer struct {
	mu      sync.Mutex
	file    *os.File
	jobID   job.ID
	offset  int64
	records Repository
	now     func() time.Time
	closed  bool
}

// Stream returns an io.Writer that tags all bytes with stream.
func (writer *Writer) Stream(ctx context.Context, stream Stream) io.Writer {
	return &streamWriter{ctx: ctx, parent: writer, stream: stream}
}

// Close durably flushes and closes the log data file.
func (writer *Writer) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	return errors.Join(writer.file.Sync(), writer.file.Close())
}

func (writer *Writer) append(ctx context.Context, stream Stream, contents []byte) (int, error) {
	if stream != StreamStdout && stream != StreamStderr {
		return 0, fmt.Errorf("%w: unsupported stream %q", ErrInvalidRecord, stream)
	}
	writtenTotal := 0
	for len(contents) > 0 {
		chunkLength := min(len(contents), MaximumChunkBytes)
		chunk := contents[:chunkLength]
		writer.mu.Lock()
		if writer.closed {
			writer.mu.Unlock()
			return writtenTotal, os.ErrClosed
		}
		expectedOffset := writer.offset
		if err := writeAtAll(writer.file, chunk, expectedOffset); err != nil {
			writer.mu.Unlock()
			return writtenTotal, fmt.Errorf("append log data: %w", err)
		}
		if err := writer.file.Sync(); err != nil {
			_ = writer.file.Truncate(expectedOffset)
			writer.mu.Unlock()
			return writtenTotal, fmt.Errorf("sync log data: %w", err)
		}
		metadata, err := writer.records.Commit(
			ctx,
			writer.jobID,
			expectedOffset,
			stream,
			len(chunk),
			writer.now().UTC(),
		)
		if err != nil {
			repairErr := errors.Join(writer.file.Truncate(expectedOffset), writer.file.Sync())
			writer.mu.Unlock()
			return writtenTotal, errors.Join(err, repairErr)
		}
		writer.offset = metadata.DataOffset + int64(metadata.DataLength)
		writer.mu.Unlock()
		writtenTotal += len(chunk)
		contents = contents[len(chunk):]
	}
	return writtenTotal, nil
}

type streamWriter struct {
	ctx    context.Context
	parent *Writer
	stream Stream
}

func (writer *streamWriter) Write(contents []byte) (int, error) {
	return writer.parent.append(writer.ctx, writer.stream, contents)
}

func writeAtAll(file *os.File, contents []byte, offset int64) error {
	for len(contents) > 0 {
		written, err := file.WriteAt(contents, offset)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		offset += int64(written)
		contents = contents[written:]
	}
	return nil
}
