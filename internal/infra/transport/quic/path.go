package quic

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	quicgo "github.com/quic-go/quic-go"

	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

// RemotePath carries the existing identity-pinned control protocol over one
// already-selected ICE packet connection. The path provider supplies
// reachability only; endpoint certificates remain the trust boundary.
type RemotePath struct {
	endpoint   *Endpoint
	connection net.PacketConn
	remote     net.Addr
	transport  *quicgo.Transport

	closeOnce sync.Once
}

// NewRemotePath binds one selected packet path to the endpoint identity.
// Ownership of connection transfers to the returned path.
func (endpoint *Endpoint) NewRemotePath(connection net.PacketConn, remote net.Addr) (*RemotePath, error) {
	if endpoint == nil || endpoint.listener == nil || connection == nil || remote == nil {
		return nil, session.ErrInvalidEndpoint
	}
	return &RemotePath{
		endpoint: endpoint, connection: connection, remote: remote,
		transport: &quicgo.Transport{Conn: connection},
	}, nil
}

// Dial opens one orchestrator control connection over this selected path and
// requires the worker certificate to match the durable active pin.
func (path *RemotePath) Dial(
	ctx context.Context,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	if path == nil || path.endpoint == nil || path.transport == nil || path.remote == nil ||
		path.endpoint.local.Role != device.RoleOrchestrator || peer.Validate() != nil ||
		peer.State != trust.StateActive || peer.Role != device.RoleWorker {
		return nil, session.ErrInvalidEndpoint
	}
	connection, err := path.transport.Dial(
		ctx,
		path.remote,
		controlClientTLSConfig(path.endpoint.certificate, peer),
		controlQUICConfig(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial paired worker over selected path: %w", err)
	}
	return &controlClient{connection: connection}, nil
}

// Serve accepts identity-pinned orchestrator control connections on a worker.
func (path *RemotePath) Serve(ctx context.Context, handler remoteprotocol.Handler) error {
	if path == nil || path.endpoint == nil || path.transport == nil ||
		path.endpoint.local.Role != device.RoleWorker || handler == nil {
		return session.ErrInvalidEndpoint
	}
	listener, err := path.transport.Listen(
		serverTLSConfig(path.endpoint.certificate), controlQUICConfig(),
	)
	if err != nil {
		return fmt.Errorf("listen for remote control over selected path: %w", err)
	}
	defer listener.Close()

	semaphore := make(chan struct{}, maximumEndpointClients)
	var wait sync.WaitGroup
	defer wait.Wait()
	for {
		connection, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept remote control over selected path: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = connection.CloseWithError(closeNormal, "daemon stopping")
			continue
		default:
			_ = connection.CloseWithError(closeRejected, "too many remote connections")
			continue
		}
		wait.Add(1)
		go func() {
			defer func() { <-semaphore; wait.Done() }()
			path.endpoint.serveRemoteConnection(ctx, connection, handler)
		}()
	}
}

// Close stops all QUIC activity and releases the selected packet path.
func (path *RemotePath) Close() error {
	if path == nil {
		return nil
	}
	var closeErr error
	path.closeOnce.Do(func() {
		if path.transport != nil {
			closeErr = path.transport.Close()
		}
		if path.connection != nil {
			closeErr = errors.Join(closeErr, path.connection.Close())
		}
	})
	return closeErr
}
