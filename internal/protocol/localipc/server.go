package localipc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
)

const (
	maximumConcurrentClients = 64
	connectionTimeout        = 30 * time.Second
	submitConnectionTimeout  = 6 * time.Hour
)

var (
	ErrInvalidServer        = errors.New("invalid local IPC server")
	ErrDaemonAlreadyRunning = errors.New("computehopd is already listening")
)

// Handler processes authenticated, version-compatible local requests.
type Handler interface {
	Handle(context.Context, *localv1.Request) *localv1.Response
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, *localv1.Request) *localv1.Response

func (function HandlerFunc) Handle(ctx context.Context, request *localv1.Request) *localv1.Response {
	return function(ctx, request)
}

// Server owns a protected local listener and bounds concurrent clients.
type Server struct {
	listener net.Listener
	token    []byte
	handler  Handler

	connectionsMu sync.Mutex
	connections   map[net.Conn]struct{}
	wait          sync.WaitGroup
}

// NewServer opens the platform local transport at socketPath.
func NewServer(socketPath string, token []byte, handler Handler) (*Server, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("%w: socket path is required", ErrInvalidServer)
	}
	if len(token) == 0 {
		return nil, fmt.Errorf("%w: capability token is required", ErrInvalidServer)
	}
	if handler == nil {
		return nil, fmt.Errorf("%w: handler is required", ErrInvalidServer)
	}
	listener, err := listen(socketPath)
	if err != nil {
		return nil, err
	}
	return &Server{
		listener:    listener,
		token:       append([]byte(nil), token...),
		handler:     handler,
		connections: make(map[net.Conn]struct{}),
	}, nil
}

// Serve accepts connections until ctx is cancelled or the listener fails.
func (server *Server) Serve(ctx context.Context) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.listener.Close()
			server.closeConnections()
		case <-done:
		}
	}()

	semaphore := make(chan struct{}, maximumConcurrentClients)
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				server.wait.Wait()
				return nil
			}
			server.closeConnections()
			server.wait.Wait()
			return fmt.Errorf("accept local IPC connection: %w", err)
		}

		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = connection.Close()
			continue
		}
		server.track(connection)
		server.wait.Add(1)
		go func() {
			defer func() {
				<-semaphore
				server.untrack(connection)
				_ = connection.Close()
				server.wait.Done()
			}()
			server.serveConnection(ctx, connection)
		}()
	}
}

// Close stops accepting new clients and interrupts active requests.
func (server *Server) Close() error {
	server.closeConnections()
	return server.listener.Close()
}

func (server *Server) serveConnection(ctx context.Context, connection net.Conn) {
	_ = connection.SetDeadline(time.Now().Add(connectionTimeout))
	request := new(localv1.Request)
	if err := readMessage(connection, request); err != nil {
		return
	}
	if isLongOperation(request) {
		_ = connection.SetDeadline(time.Now().Add(submitConnectionTimeout))
	}
	requestTimeout := connectionTimeout
	if isLongOperation(request) {
		requestTimeout = submitConnectionTimeout
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	// A request owns the rest of this one-shot connection. Watching for an EOF
	// propagates CLI cancellation into snapshotting and transfer work instead of
	// leaving it running invisibly in the daemon.
	go func() {
		var unexpected [1]byte
		_, _ = connection.Read(unexpected[:])
		cancel()
	}()

	response := &localv1.Response{
		ProtocolVersion: ProtocolVersion,
		RequestId:       request.GetRequestId(),
	}
	if !tokensEqual(request.GetCapabilityToken(), server.token) {
		response.Error = protocolError(localv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED, "local authentication failed")
	} else if request.GetProtocolVersion() != ProtocolVersion {
		response.Error = protocolError(
			localv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VERSION,
			fmt.Sprintf("unsupported local protocol version %d", request.GetProtocolVersion()),
		)
	} else if request.GetRequestId() == "" {
		response.Error = protocolError(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "request ID is required")
	} else if request.GetOperation() == nil {
		response.Error = protocolError(localv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "request operation is required")
	} else {
		response = server.handle(requestContext, request)
		if response == nil {
			response = &localv1.Response{
				Error: protocolError(localv1.ErrorCode_ERROR_CODE_INTERNAL, "internal daemon error"),
			}
		}
		response.ProtocolVersion = ProtocolVersion
		response.RequestId = request.GetRequestId()
	}
	_ = writeMessage(connection, response)
}

func (server *Server) handle(ctx context.Context, request *localv1.Request) (response *localv1.Response) {
	defer func() {
		if recover() != nil {
			response = &localv1.Response{
				Error: protocolError(localv1.ErrorCode_ERROR_CODE_INTERNAL, "internal daemon error"),
			}
		}
	}()
	return server.handler.Handle(ctx, request)
}

func (server *Server) track(connection net.Conn) {
	server.connectionsMu.Lock()
	defer server.connectionsMu.Unlock()
	server.connections[connection] = struct{}{}
}

func (server *Server) untrack(connection net.Conn) {
	server.connectionsMu.Lock()
	defer server.connectionsMu.Unlock()
	delete(server.connections, connection)
}

func (server *Server) closeConnections() {
	server.connectionsMu.Lock()
	defer server.connectionsMu.Unlock()
	for connection := range server.connections {
		_ = connection.Close()
	}
}

func tokensEqual(received, expected []byte) bool {
	return len(received) == len(expected) && subtle.ConstantTimeCompare(received, expected) == 1
}

func protocolError(code localv1.ErrorCode, message string) *localv1.Error {
	return &localv1.Error{Code: code, Message: message}
}
