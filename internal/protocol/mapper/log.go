package mapper

import (
	"fmt"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	joblogging "github.com/austinjiann/spare-compute/internal/logging"
)

// LogRecordsToProto maps durable output records to the local protocol.
func LogRecordsToProto(records []joblogging.Record) ([]*localv1.JobLogRecord, error) {
	messages := make([]*localv1.JobLogRecord, len(records))
	for index, record := range records {
		var stream localv1.JobLogStream
		switch record.Stream {
		case joblogging.StreamStdout:
			stream = localv1.JobLogStream_JOB_LOG_STREAM_STDOUT
		case joblogging.StreamStderr:
			stream = localv1.JobLogStream_JOB_LOG_STREAM_STDERR
		default:
			return nil, fmt.Errorf("%w: unsupported stream %q", joblogging.ErrInvalidRecord, record.Stream)
		}
		if record.Sequence == 0 || len(record.Data) == 0 || record.At.IsZero() {
			return nil, joblogging.ErrInvalidRecord
		}
		messages[index] = &localv1.JobLogRecord{
			Sequence:   record.Sequence,
			Stream:     stream,
			Data:       append([]byte(nil), record.Data...),
			AtUnixNano: record.At.UnixNano(),
		}
	}
	return messages, nil
}
