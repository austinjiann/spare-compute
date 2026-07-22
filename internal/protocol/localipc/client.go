package localipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
)

const (
	defaultRequestTimeout = 15 * time.Second
	submitRequestTimeout  = 6 * time.Hour
)

var (
	ErrInvalidClient      = errors.New("invalid local IPC client")
	ErrMismatchedResponse = errors.New("local IPC response does not match request")
)

// RemoteError is a structured error returned by computehopd.
type RemoteError struct {
	Code    localv1.ErrorCode
	Message string
}

func (failure *RemoteError) Error() string {
	return failure.Message
}

// Client sends one request per connection to the local daemon.
type Client struct {
	socketPath string
	token      []byte
	timeout    time.Duration
}

// NewClient constructs a client for a user-owned local daemon socket.
func NewClient(socketPath string, token []byte) (*Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("%w: socket path is required", ErrInvalidClient)
	}
	if len(token) == 0 {
		return nil, fmt.Errorf("%w: capability token is required", ErrInvalidClient)
	}
	return &Client{
		socketPath: socketPath,
		token:      append([]byte(nil), token...),
		timeout:    defaultRequestTimeout,
	}, nil
}

// Call sends request and validates the correlated response envelope.
func (client *Client) Call(ctx context.Context, request *localv1.Request) (*localv1.Response, error) {
	if request == nil || request.GetOperation() == nil {
		return nil, fmt.Errorf("%w: request operation is required", ErrInvalidClient)
	}
	requestID, err := newRequestID()
	if err != nil {
		return nil, err
	}
	request.ProtocolVersion = ProtocolVersion
	request.RequestId = requestID
	request.CapabilityToken = append([]byte(nil), client.token...)

	callContext := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		timeout := client.timeout
		if request.GetSubmitJob() != nil && timeout == defaultRequestTimeout {
			timeout = submitRequestTimeout
		}
		callContext, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	connection, err := dial(callContext, client.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to computehopd: %w", err)
	}
	defer connection.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-callContext.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	if deadline, ok := callContext.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}

	if err := writeMessage(connection, request); err != nil {
		if callContext.Err() != nil {
			return nil, callContext.Err()
		}
		return nil, err
	}
	response := new(localv1.Response)
	if err := readMessage(connection, response); err != nil {
		if callContext.Err() != nil {
			return nil, callContext.Err()
		}
		return nil, err
	}
	if response.GetProtocolVersion() != ProtocolVersion || response.GetRequestId() != requestID {
		return nil, ErrMismatchedResponse
	}
	if failure := response.GetError(); failure != nil {
		return nil, &RemoteError{Code: failure.GetCode(), Message: failure.GetMessage()}
	}
	return response, nil
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate local request ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
