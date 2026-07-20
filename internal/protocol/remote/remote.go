// Package remote implements the bounded worker job-control protocol carried by
// an already authenticated QUIC connection.
package remote

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
)

const (
	ProtocolVersion    uint32 = 1
	maximumFrameBytes         = 1 << 20
	defaultCallTimeout        = 15 * time.Second
)

var (
	ErrInvalidMessage     = errors.New("invalid remote job message")
	ErrFrameTooLarge      = errors.New("remote job frame is too large")
	ErrMismatchedResponse = errors.New("remote job response does not match request")
)

// Stream is the deadline-aware bidirectional boundary supplied by QUIC.
type Stream interface {
	io.Reader
	io.Writer
	io.Closer
	SetDeadline(time.Time) error
}

// Caller sends authenticated job-control requests to one worker.
type Caller interface {
	Call(context.Context, *computehopv1.RemoteRequest) (*computehopv1.RemoteResponse, error)
	Close() error
}

// Handler executes one request after the transport has authenticated the peer.
type Handler interface {
	Handle(context.Context, *computehopv1.RemoteRequest) *computehopv1.RemoteResponse
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, *computehopv1.RemoteRequest) *computehopv1.RemoteResponse

func (function HandlerFunc) Handle(
	ctx context.Context,
	request *computehopv1.RemoteRequest,
) *computehopv1.RemoteResponse {
	return function(ctx, request)
}

// Error is a structured failure returned by an authenticated worker.
type Error struct {
	Code    computehopv1.RemoteErrorCode
	Message string
}

func (failure *Error) Error() string {
	if failure == nil {
		return ""
	}
	return failure.Message
}

// Call exchanges one correlated request and response on a fresh stream.
func Call(
	ctx context.Context,
	stream Stream,
	request *computehopv1.RemoteRequest,
) (*computehopv1.RemoteResponse, error) {
	if stream == nil || request == nil || request.GetOperation() == nil {
		return nil, ErrInvalidMessage
	}
	requestID, err := newRequestID()
	if err != nil {
		return nil, err
	}
	request.ProtocolVersion = ProtocolVersion
	request.RequestId = requestID

	callContext := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		callContext, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}
	if deadline, ok := callContext.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-callContext.Done():
			_ = stream.Close()
		case <-done:
		}
	}()

	if err := writeMessage(stream, request); err != nil {
		if callContext.Err() != nil {
			return nil, callContext.Err()
		}
		return nil, err
	}
	response := new(computehopv1.RemoteResponse)
	if err := readMessage(stream, response); err != nil {
		if callContext.Err() != nil {
			return nil, callContext.Err()
		}
		return nil, err
	}
	if response.GetProtocolVersion() != ProtocolVersion || response.GetRequestId() != requestID ||
		hasUnknownResponseFields(response) {
		return nil, ErrMismatchedResponse
	}
	if failure := response.GetError(); failure != nil {
		if failure.GetCode() == computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNSPECIFIED ||
			failure.GetMessage() == "" {
			return nil, ErrInvalidMessage
		}
		return nil, &Error{Code: failure.GetCode(), Message: failure.GetMessage()}
	}
	if response.GetResult() == nil {
		return nil, ErrInvalidMessage
	}
	return response, nil
}

// Serve reads and handles one request on stream. Peer authentication and role
// enforcement must have completed before this function is called.
func Serve(ctx context.Context, stream Stream, handler Handler) {
	if stream == nil || handler == nil {
		return
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	}
	request := new(computehopv1.RemoteRequest)
	if err := readMessage(stream, request); err != nil {
		return
	}
	response := &computehopv1.RemoteResponse{
		ProtocolVersion: ProtocolVersion,
		RequestId:       request.GetRequestId(),
	}
	switch {
	case request.GetProtocolVersion() != ProtocolVersion:
		response.Error = protocolError(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_UNSUPPORTED_VERSION,
			fmt.Sprintf("unsupported remote protocol version %d", request.GetProtocolVersion()),
		)
	case request.GetRequestId() == "":
		response.Error = protocolError(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			"request ID is required",
		)
	case request.GetOperation() == nil || hasUnknownRequestFields(request):
		response.Error = protocolError(
			computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INVALID_ARGUMENT,
			"request operation is invalid",
		)
	default:
		response = safelyHandle(ctx, handler, request)
		if response == nil {
			response = &computehopv1.RemoteResponse{Error: protocolError(
				computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL,
				"internal worker error",
			)}
		}
		response.ProtocolVersion = ProtocolVersion
		response.RequestId = request.GetRequestId()
	}
	_ = writeMessage(stream, response)
}

func safelyHandle(
	ctx context.Context,
	handler Handler,
	request *computehopv1.RemoteRequest,
) (response *computehopv1.RemoteResponse) {
	defer func() {
		if recover() != nil {
			response = &computehopv1.RemoteResponse{Error: protocolError(
				computehopv1.RemoteErrorCode_REMOTE_ERROR_CODE_INTERNAL,
				"internal worker error",
			)}
		}
	}()
	return handler.Handle(ctx, request)
}

func protocolError(code computehopv1.RemoteErrorCode, message string) *computehopv1.RemoteError {
	return &computehopv1.RemoteError{Code: code, Message: message}
}

func writeMessage(writer io.Writer, message proto.Message) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal remote job message: %w", err)
	}
	if len(payload) == 0 {
		return ErrInvalidMessage
	}
	if len(payload) > maximumFrameBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, len(payload), maximumFrameBytes)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	if err := writeAll(writer, length[:]); err != nil {
		return fmt.Errorf("write remote job frame length: %w", err)
	}
	if err := writeAll(writer, payload); err != nil {
		return fmt.Errorf("write remote job frame payload: %w", err)
	}
	return nil
}

func readMessage(reader io.Reader, message proto.Message) error {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		return fmt.Errorf("read remote job frame length: %w", err)
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 {
		return ErrInvalidMessage
	}
	if length > maximumFrameBytes {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrFrameTooLarge, length, maximumFrameBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("read remote job frame payload: %w", err)
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, message); err != nil {
		return fmt.Errorf("unmarshal remote job message: %w", err)
	}
	return nil
}

func hasUnknownRequestFields(message *computehopv1.RemoteRequest) bool {
	if len(message.ProtoReflect().GetUnknown()) != 0 {
		return true
	}
	switch operation := message.GetOperation().(type) {
	case *computehopv1.RemoteRequest_SubmitJob:
		return operation.SubmitJob == nil || hasUnknown(operation.SubmitJob) ||
			operation.SubmitJob.GetSpec() == nil || hasUnknown(operation.SubmitJob.GetSpec())
	case *computehopv1.RemoteRequest_GetJob:
		return operation.GetJob == nil || hasUnknown(operation.GetJob)
	case *computehopv1.RemoteRequest_ListJobs:
		return operation.ListJobs == nil || hasUnknown(operation.ListJobs)
	case *computehopv1.RemoteRequest_CancelJob:
		return operation.CancelJob == nil || hasUnknown(operation.CancelJob)
	case *computehopv1.RemoteRequest_ReadJobLogs:
		return operation.ReadJobLogs == nil || hasUnknown(operation.ReadJobLogs)
	default:
		return true
	}
}

func hasUnknownResponseFields(message *computehopv1.RemoteResponse) bool {
	if len(message.ProtoReflect().GetUnknown()) != 0 ||
		(message.GetError() != nil && (hasUnknown(message.GetError()) || message.GetResult() != nil)) {
		return true
	}
	switch result := message.GetResult().(type) {
	case *computehopv1.RemoteResponse_SubmitJob:
		return result.SubmitJob == nil || hasUnknown(result.SubmitJob) || hasUnknownJob(result.SubmitJob.GetJob())
	case *computehopv1.RemoteResponse_GetJob:
		return result.GetJob == nil || hasUnknown(result.GetJob) || hasUnknownJob(result.GetJob.GetJob())
	case *computehopv1.RemoteResponse_ListJobs:
		if result.ListJobs == nil || hasUnknown(result.ListJobs) {
			return true
		}
		for _, value := range result.ListJobs.GetJobs() {
			if hasUnknownJob(value) {
				return true
			}
		}
		return false
	case *computehopv1.RemoteResponse_CancelJob:
		return result.CancelJob == nil || hasUnknown(result.CancelJob) || hasUnknownJob(result.CancelJob.GetJob())
	case *computehopv1.RemoteResponse_ReadJobLogs:
		if result.ReadJobLogs == nil || hasUnknown(result.ReadJobLogs) || hasUnknownJob(result.ReadJobLogs.GetJob()) {
			return true
		}
		for _, record := range result.ReadJobLogs.GetRecords() {
			if hasUnknown(record) {
				return true
			}
		}
		return false
	case nil:
		return message.GetError() == nil
	default:
		return true
	}
}

func hasUnknownJob(message *computehopv1.Job) bool {
	return message == nil || hasUnknown(message) || message.GetSpec() == nil || hasUnknown(message.GetSpec()) ||
		(message.GetFailure() != nil && hasUnknown(message.GetFailure()))
}

func hasUnknown(message proto.Message) bool {
	return message == nil || len(message.ProtoReflect().GetUnknown()) != 0
}

func writeAll(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate remote request ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
