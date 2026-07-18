//go:build !windows

package localipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
)

func TestAuthenticatedClientServerRoundTrip(t *testing.T) {
	token := []byte("test-capability-token")
	server, socketPath, cancel, result := startServerForTest(t, token)
	defer stopServerForTest(t, server, cancel, result)

	client, err := NewClient(socketPath, token)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got := response.GetPing().GetDaemonVersion(); got != "test" {
		t.Fatalf("daemon version = %q, want test", got)
	}
}

func TestServerRejectsWrongCapabilityToken(t *testing.T) {
	server, socketPath, cancel, result := startServerForTest(t, []byte("correct-token"))
	defer stopServerForTest(t, server, cancel, result)

	client, err := NewClient(socketPath, []byte("wrong-token"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != localv1.ErrorCode_ERROR_CODE_UNAUTHENTICATED {
		t.Fatalf("Call() error = %#v, want unauthenticated RemoteError", err)
	}
}

func TestClientCancellationInterruptsBlockedResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := HandlerFunc(func(context.Context, *localv1.Request) *localv1.Response {
		close(started)
		<-release
		return &localv1.Response{Result: &localv1.Response_Ping{Ping: &localv1.PingResponse{}}}
	})
	token := []byte("correct-token")
	server, socketPath, cancelServer, result := startServerWithHandlerForTest(t, token, handler)
	defer stopServerForTest(t, server, cancelServer, result)

	client, err := NewClient(socketPath, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelCall := context.WithCancel(context.Background())
	callResult := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, &localv1.Request{
			Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
		})
		callResult <- err
	}()
	<-started
	cancelCall()
	select {
	case err := <-callResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call() did not stop after context cancellation")
	}
	close(release)
}

func TestServerContainsHandlerPanic(t *testing.T) {
	token := []byte("correct-token")
	handler := HandlerFunc(func(context.Context, *localv1.Request) *localv1.Response {
		panic("boom")
	})
	server, socketPath, cancel, result := startServerWithHandlerForTest(t, token, handler)
	defer stopServerForTest(t, server, cancel, result)

	client, err := NewClient(socketPath, token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != localv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("Call() error = %#v, want internal RemoteError", err)
	}
}

func TestServerRejectsUnsupportedProtocolVersion(t *testing.T) {
	token := []byte("correct-token")
	server, socketPath, cancel, result := startServerForTest(t, token)
	defer stopServerForTest(t, server, cancel, result)

	connection, err := dial(context.Background(), socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request := &localv1.Request{
		ProtocolVersion: 999,
		RequestId:       "request",
		CapabilityToken: token,
		Operation:       &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
	}
	if err := writeMessage(connection, request); err != nil {
		t.Fatal(err)
	}
	response := new(localv1.Response)
	if err := readMessage(connection, response); err != nil {
		t.Fatal(err)
	}
	if got := response.GetError().GetCode(); got != localv1.ErrorCode_ERROR_CODE_UNSUPPORTED_VERSION {
		t.Fatalf("error code = %v, want unsupported version", got)
	}
}

func TestListenerProtectsSocketPath(t *testing.T) {
	directory := shortSocketDirectory(t)
	path := filepath.Join(directory, "computehop.sock")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewServer(path, []byte("token"), pingHandler())
	if err == nil {
		t.Fatal("NewServer() error = nil for ordinary file")
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || string(contents) != "keep me" {
		t.Fatalf("ordinary file was changed: contents = %q, error = %v", contents, readErr)
	}
}

func TestListenerRejectsAnotherDaemon(t *testing.T) {
	directory := shortSocketDirectory(t)
	path := filepath.Join(directory, "computehop.sock")
	first, err := NewServer(path, []byte("token"), pingHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewServer(path, []byte("token"), pingHandler())
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("second NewServer() error = %v, want ErrDaemonAlreadyRunning", err)
	}
}

func TestSocketIsOwnerOnly(t *testing.T) {
	directory := shortSocketDirectory(t)
	path := filepath.Join(directory, "computehop.sock")
	server, err := NewServer(path, []byte("token"), pingHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", got)
	}
}

func startServerForTest(
	t *testing.T,
	token []byte,
) (*Server, string, context.CancelFunc, <-chan error) {
	t.Helper()
	return startServerWithHandlerForTest(t, token, pingHandler())
}

func startServerWithHandlerForTest(
	t *testing.T,
	token []byte,
	handler Handler,
) (*Server, string, context.CancelFunc, <-chan error) {
	t.Helper()
	path := filepath.Join(shortSocketDirectory(t), "computehop.sock")
	server, err := NewServer(path, token, handler)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- server.Serve(ctx) }()
	return server, path, cancel, result
}

func stopServerForTest(
	t *testing.T,
	server *Server,
	cancel context.CancelFunc,
	result <-chan error,
) {
	t.Helper()
	cancel()
	_ = server.Close()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop")
	}
}

func pingHandler() Handler {
	return HandlerFunc(func(context.Context, *localv1.Request) *localv1.Response {
		return &localv1.Response{Result: &localv1.Response_Ping{Ping: &localv1.PingResponse{
			DaemonVersion: "test",
		}}}
	})
}

func shortSocketDirectory(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "aix" {
		base = os.TempDir()
	}
	directory, err := os.MkdirTemp(base, "ch-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}
