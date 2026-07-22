package quic

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	quicgo "github.com/quic-go/quic-go"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	controlALPN                = "computehop-control/1"
	maximumControlStreams      = 16
	controlConnectionIdleLimit = 45 * time.Second
)

// DialRemote connects to a current LAN observation while requiring the server
// certificate to match the selected active worker pin.
func (endpoint *Endpoint) DialRemote(
	ctx context.Context,
	target device.NearbyDevice,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	if endpoint == nil || endpoint.listener == nil || endpoint.local.Role != device.RoleOrchestrator ||
		target.Observation.Validate() != nil || !target.Announcement.EndpointReady ||
		target.Announcement.Role != device.RoleWorker || peer.Validate() != nil ||
		peer.State != trust.StateActive || peer.Role != device.RoleWorker {
		return nil, session.ErrInvalidEndpoint
	}
	var failures []error
	for _, address := range target.Addresses {
		if !address.IsValid() {
			continue
		}
		remoteAddress := netip.AddrPortFrom(address, target.Announcement.Port).String()
		client, err := endpoint.dialRemoteOne(ctx, remoteAddress, peer)
		if err == nil {
			return client, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", remoteAddress, err))
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("%w: target has no usable address", session.ErrInvalidEndpoint)
	}
	return nil, fmt.Errorf("connect to paired worker: %w", errors.Join(failures...))
}

func (endpoint *Endpoint) dialRemoteOne(
	ctx context.Context,
	address string,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	connection, err := quicgo.DialAddr(
		ctx,
		address,
		controlClientTLSConfig(endpoint.certificate, peer),
		controlQUICConfig(),
	)
	if err != nil {
		return nil, err
	}
	return &controlClient{connection: connection}, nil
}

type controlClient struct {
	connection *quicgo.Conn
	closeOnce  sync.Once
}

func (client *controlClient) Call(
	ctx context.Context,
	request *computehopv1.RemoteRequest,
) (*computehopv1.RemoteResponse, error) {
	if client == nil || client.connection == nil {
		return nil, session.ErrInvalidEndpoint
	}
	stream, err := client.connection.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open remote job stream: %w", err)
	}
	defer stream.Close()
	return remoteprotocol.Call(ctx, stream, request)
}

func (client *controlClient) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	var err error
	client.closeOnce.Do(func() {
		err = client.connection.CloseWithError(closeNormal, "remote operation complete")
	})
	return err
}

func (endpoint *Endpoint) serveRemoteConnection(
	ctx context.Context,
	connection *quicgo.Conn,
	handler remoteprotocol.Handler,
) {
	if endpoint.local.Role != device.RoleWorker || handler == nil {
		_ = connection.CloseWithError(closeRejected, "remote job control is unavailable")
		return
	}
	identity, err := peerFromTLS(connection.ConnectionState().TLS, controlALPN)
	if err != nil || !endpoint.activeOrchestrator(ctx, identity) {
		_ = connection.CloseWithError(closeRejected, "paired orchestrator authentication failed")
		return
	}

	semaphore := make(chan struct{}, maximumControlStreams)
	var wait sync.WaitGroup
	defer func() {
		_ = connection.CloseWithError(closeNormal, "remote session complete")
		wait.Wait()
	}()
	for {
		stream, err := connection.AcceptStream(ctx)
		if err != nil {
			return
		}
		// Re-check the durable pin for every operation so revocation takes effect
		// even if a peer deliberately keeps an authenticated QUIC connection open.
		if !endpoint.activeOrchestrator(ctx, identity) {
			_ = stream.Close()
			_ = connection.CloseWithError(closeRejected, "paired orchestrator was revoked")
			return
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = stream.Close()
			return
		default:
			stream.CancelRead(quicgo.StreamErrorCode(closeRejected))
			stream.CancelWrite(quicgo.StreamErrorCode(closeRejected))
			continue
		}
		wait.Add(1)
		go func() {
			defer func() {
				<-semaphore
				_ = stream.Close()
				wait.Done()
			}()
			remoteprotocol.Serve(ctx, stream, handler)
		}()
	}
}

func (endpoint *Endpoint) activeOrchestrator(ctx context.Context, identity session.Peer) bool {
	if identity.ID == "" || len(identity.PublicKey) != ed25519.PublicKeySize {
		return false
	}
	peer, err := endpoint.trust.Get(ctx, identity.ID)
	return err == nil && peer.State == trust.StateActive && peer.Role == device.RoleOrchestrator &&
		bytes.Equal(peer.PublicKey, identity.PublicKey)
}

func controlClientTLSConfig(certificate tls.Certificate, peer trust.Peer) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		NextProtos: []string{controlALPN}, InsecureSkipVerify: true, // Replaced by the pinned-key callback below.
		VerifyPeerCertificate: func(rawCertificates [][]byte, verifiedChains [][]*x509.Certificate) error {
			if err := verifySelfSignedIdentity(rawCertificates, verifiedChains); err != nil {
				return err
			}
			certificate, err := x509.ParseCertificate(rawCertificates[0])
			if err != nil {
				return session.ErrInvalidPeer
			}
			publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
			if !ok || !bytes.Equal(publicKey, peer.PublicKey) {
				return session.ErrInvalidPeer
			}
			id, err := device.IDFromPublicKey(publicKey)
			if err != nil || id != peer.DeviceID {
				return session.ErrInvalidPeer
			}
			return nil
		},
	}
}

func controlQUICConfig() *quicgo.Config {
	configuration := quicConfig()
	configuration.MaxIdleTimeout = controlConnectionIdleLimit
	configuration.MaxIncomingStreams = maximumControlStreams
	return configuration
}
