// Package quic implements ComputeHop's authenticated device transport over QUIC.
package quic

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"sync"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"

	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	pairingALPN            = "computehop-pairing/1"
	pairingProtocolVersion = 1
	pairingNonceBytes      = 32
	maximumPairingFrame    = 64 << 10
	maximumEndpointClients = 64
	pairingHandshakeLimit  = 10 * time.Second
	pairingIdleLimit       = 6 * time.Minute
	pairingExporterLabel   = "EXPORTER-ComputeHop-Pairing-v1"
)

const (
	closeNormal   quicgo.ApplicationErrorCode = 0
	closeProtocol quicgo.ApplicationErrorCode = 1
	closeRejected quicgo.ApplicationErrorCode = 2
)

// Endpoint owns the ready QUIC listener shared by pairing and remote job control.
type Endpoint struct {
	local       session.LocalDevice
	certificate tls.Certificate
	trust       trust.Repository
	listener    *quicgo.Listener

	closeOnce sync.Once
	closed    chan struct{}
}

var _ session.Endpoint = (*Endpoint)(nil)

// Listen creates the authenticated endpoint before it is advertised by mDNS.
func Listen(address string, local session.LocalDevice, repository trust.Repository) (*Endpoint, error) {
	if address == "" || local.Validate() != nil || repository == nil {
		return nil, session.ErrInvalidEndpoint
	}
	certificate, err := identityCertificate(local.Identity)
	if err != nil {
		return nil, err
	}
	listener, err := quicgo.ListenAddr(address, serverTLSConfig(certificate), quicConfig())
	if err != nil {
		return nil, fmt.Errorf("listen for ComputeHop pairing: %w", err)
	}
	return &Endpoint{
		local: local, certificate: certificate, trust: repository,
		listener: listener, closed: make(chan struct{}),
	}, nil
}

// Port returns the bound UDP port advertised through discovery.
func (endpoint *Endpoint) Port() uint16 {
	if endpoint == nil || endpoint.listener == nil {
		return 0
	}
	address, ok := endpoint.listener.Addr().(*net.UDPAddr)
	if !ok || address.Port <= 0 || address.Port > 65535 {
		return 0
	}
	return uint16(address.Port)
}

// Run accepts bounded pairing and trusted-control connections until shutdown.
func (endpoint *Endpoint) Run(
	ctx context.Context,
	handlePairing func(session.PairingChannel),
	remoteHandler remoteprotocol.Handler,
) error {
	if endpoint == nil || endpoint.listener == nil || handlePairing == nil || remoteHandler == nil {
		return session.ErrInvalidEndpoint
	}
	semaphore := make(chan struct{}, maximumEndpointClients)
	var wait sync.WaitGroup
	defer wait.Wait()
	for {
		connection, err := endpoint.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil || endpoint.isClosed() {
				return nil
			}
			return fmt.Errorf("accept pairing connection: %w", err)
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			_ = connection.CloseWithError(closeNormal, "daemon stopping")
			continue
		default:
			_ = connection.CloseWithError(closeRejected, "too many pairing attempts")
			continue
		}
		wait.Add(1)
		go func() {
			defer func() { <-semaphore; wait.Done() }()
			switch connection.ConnectionState().TLS.NegotiatedProtocol {
			case pairingALPN:
				channel, err := endpoint.acceptPairing(ctx, connection)
				if err != nil {
					_ = connection.CloseWithError(closeProtocol, "invalid pairing handshake")
					return
				}
				handlePairing(channel)
			case controlALPN:
				endpoint.serveRemoteConnection(ctx, connection, remoteHandler)
			default:
				_ = connection.CloseWithError(closeProtocol, "unsupported application protocol")
			}
		}()
	}
}

// DialPairing connects only to addresses supplied by the selected mDNS observation.
func (endpoint *Endpoint) DialPairing(ctx context.Context, target device.NearbyDevice) (session.PairingChannel, error) {
	if endpoint == nil || endpoint.listener == nil || target.Observation.Validate() != nil ||
		!target.Announcement.EndpointReady || target.Announcement.Role != device.RoleWorker {
		return nil, session.ErrInvalidEndpoint
	}
	var failures []error
	for _, address := range target.Addresses {
		if !address.IsValid() {
			continue
		}
		remote := netip.AddrPortFrom(address, target.Announcement.Port).String()
		channel, err := endpoint.dialPairingOne(ctx, remote, target.Announcement.PresenceID)
		if err == nil {
			return channel, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", remote, err))
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("%w: target has no usable address", session.ErrInvalidEndpoint)
	}
	return nil, fmt.Errorf("connect to selected device: %w", errors.Join(failures...))
}

func (endpoint *Endpoint) dialPairingOne(
	ctx context.Context,
	address string,
	expectedPresence device.PresenceID,
) (session.PairingChannel, error) {
	connection, err := quicgo.DialAddr(ctx, address, pairingClientTLSConfig(endpoint.certificate), quicConfig())
	if err != nil {
		return nil, err
	}
	fail := func(handshakeErr error) (session.PairingChannel, error) {
		_ = connection.CloseWithError(closeProtocol, "pairing handshake failed")
		return nil, handshakeErr
	}
	streamContext, cancel := context.WithTimeout(ctx, pairingHandshakeLimit)
	defer cancel()
	stream, err := connection.OpenStreamSync(streamContext)
	if err != nil {
		return fail(fmt.Errorf("open pairing stream: %w", err))
	}
	clientNonce, err := randomNonce()
	if err != nil {
		return fail(err)
	}
	request := helloFrame(endpoint.local, clientNonce, expectedPresence)
	if err := writeFrame(stream, request); err != nil {
		return fail(err)
	}
	response, err := readFrame(stream)
	if err != nil {
		return fail(err)
	}
	hello := response.GetHello()
	if hello == nil || hello.GetExpectedPresenceId() != "" {
		return fail(session.ErrProtocol)
	}
	peer, serverNonce, err := parseHello(hello, device.RoleWorker, expectedPresence)
	if err != nil {
		return fail(err)
	}
	certificatePeer, err := peerFromTLS(connection.ConnectionState().TLS, pairingALPN)
	if err != nil {
		return fail(err)
	}
	peer.ID, peer.PublicKey = certificatePeer.ID, certificatePeer.PublicKey
	if err := peer.Validate(); err != nil {
		return fail(err)
	}
	binding, err := exportBinding(connection, clientNonce, serverNonce)
	if err != nil {
		return fail(err)
	}
	return &pairingChannel{connection: connection, stream: stream, peer: peer, binding: binding}, nil
}

func (endpoint *Endpoint) acceptPairing(ctx context.Context, connection *quicgo.Conn) (session.PairingChannel, error) {
	streamContext, cancel := context.WithTimeout(ctx, pairingHandshakeLimit)
	defer cancel()
	stream, err := connection.AcceptStream(streamContext)
	if err != nil {
		return nil, fmt.Errorf("accept pairing stream: %w", err)
	}
	request, err := readFrame(stream)
	if err != nil {
		return nil, err
	}
	hello := request.GetHello()
	if hello == nil || hello.GetExpectedPresenceId() != string(endpoint.local.PresenceID) ||
		endpoint.local.Role != device.RoleWorker {
		return nil, session.ErrProtocol
	}
	peer, clientNonce, err := parseHello(hello, device.RoleOrchestrator, "")
	if err != nil {
		return nil, err
	}
	certificatePeer, err := peerFromTLS(connection.ConnectionState().TLS, pairingALPN)
	if err != nil {
		return nil, err
	}
	peer.ID, peer.PublicKey = certificatePeer.ID, certificatePeer.PublicKey
	if err := peer.Validate(); err != nil {
		return nil, err
	}
	serverNonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	if err := writeFrame(stream, helloFrame(endpoint.local, serverNonce, "")); err != nil {
		return nil, err
	}
	binding, err := exportBinding(connection, clientNonce, serverNonce)
	if err != nil {
		return nil, err
	}
	return &pairingChannel{connection: connection, stream: stream, peer: peer, binding: binding}, nil
}

// Close stops new handshakes and interrupts accepted connections.
func (endpoint *Endpoint) Close() error {
	if endpoint == nil || endpoint.listener == nil {
		return nil
	}
	var err error
	endpoint.closeOnce.Do(func() {
		close(endpoint.closed)
		err = endpoint.listener.Close()
	})
	return err
}

func (endpoint *Endpoint) isClosed() bool {
	select {
	case <-endpoint.closed:
		return true
	default:
		return false
	}
}

type pairingChannel struct {
	connection *quicgo.Conn
	stream     *quicgo.Stream
	peer       session.Peer
	binding    []byte
	sendMu     sync.Mutex
}

func (channel *pairingChannel) Peer() session.Peer {
	peer := channel.peer
	peer.PublicKey = append(ed25519.PublicKey(nil), peer.PublicKey...)
	return peer
}

func (channel *pairingChannel) Binding() []byte {
	return append([]byte(nil), channel.binding...)
}

func (channel *pairingChannel) SendDecision(ctx context.Context, decision session.PairingDecision) error {
	if !decision.PairingID.Valid() {
		return trust.ErrInvalidPairID
	}
	channel.sendMu.Lock()
	defer channel.sendMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = channel.stream.SetWriteDeadline(deadline)
	}
	return writeFrame(channel.stream, &computehopv1.PairingFrame{
		ProtocolVersion: pairingProtocolVersion,
		Payload: &computehopv1.PairingFrame_Decision{Decision: &computehopv1.PairingDecision{
			PairingId: string(decision.PairingID), Confirmed: decision.Confirmed, Committed: decision.Committed,
		}},
	})
}

func (channel *pairingChannel) ReceiveDecision(ctx context.Context) (session.PairingDecision, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = channel.stream.SetReadDeadline(deadline)
	}
	frame, err := readFrame(channel.stream)
	if err != nil {
		return session.PairingDecision{}, err
	}
	decision := frame.GetDecision()
	if decision == nil {
		return session.PairingDecision{}, session.ErrProtocol
	}
	id, err := trust.ParsePairID(decision.GetPairingId())
	if err != nil {
		return session.PairingDecision{}, err
	}
	if decision.GetCommitted() && !decision.GetConfirmed() {
		return session.PairingDecision{}, session.ErrProtocol
	}
	return session.PairingDecision{
		PairingID: id, Confirmed: decision.GetConfirmed(), Committed: decision.GetCommitted(),
	}, nil
}

func (channel *pairingChannel) Close() error {
	_ = channel.stream.Close()
	return channel.connection.CloseWithError(closeNormal, "pairing complete")
}

func helloFrame(local session.LocalDevice, nonce []byte, expected device.PresenceID) *computehopv1.PairingFrame {
	return &computehopv1.PairingFrame{
		ProtocolVersion: pairingProtocolVersion,
		Payload: &computehopv1.PairingFrame_Hello{Hello: &computehopv1.PairingHello{
			Nonce: append([]byte(nil), nonce...), DeviceName: local.Name, Role: string(local.Role),
			PresenceId: string(local.PresenceID), ExpectedPresenceId: string(expected),
		}},
	}
}

func parseHello(
	hello *computehopv1.PairingHello,
	expectedRole device.Role,
	expectedPresence device.PresenceID,
) (session.Peer, []byte, error) {
	if hello == nil || len(hello.GetNonce()) != pairingNonceBytes ||
		device.ValidateName(hello.GetDeviceName()) != nil || device.Role(hello.GetRole()) != expectedRole {
		return session.Peer{}, nil, session.ErrProtocol
	}
	presence, err := device.ParsePresenceID(hello.GetPresenceId())
	if err != nil || (expectedPresence != "" && presence != expectedPresence) {
		return session.Peer{}, nil, session.ErrProtocol
	}
	peer := session.Peer{Name: hello.GetDeviceName(), Role: expectedRole, PresenceID: presence}
	return peer, append([]byte(nil), hello.GetNonce()...), nil
}

func randomNonce() ([]byte, error) {
	nonce := make([]byte, pairingNonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate pairing nonce: %w", err)
	}
	return nonce, nil
}

func exportBinding(connection *quicgo.Conn, clientNonce, serverNonce []byte) ([]byte, error) {
	contextBytes := append(append([]byte(nil), clientNonce...), serverNonce...)
	state := connection.ConnectionState().TLS
	binding, err := state.ExportKeyingMaterial(
		pairingExporterLabel, contextBytes, trust.PairingBindingBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("export pairing key material: %w", err)
	}
	return binding, nil
}

func identityCertificate(identity device.Identity) (tls.Certificate, error) {
	if err := identity.Validate(); err != nil {
		return tls.Certificate{}, err
	}
	serialBytes := []byte(identity.ID())[:20]
	template := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serialBytes),
		Subject:               pkix.Name{CommonName: "ComputeHop " + identity.ID().Short()},
		NotBefore:             time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, identity.PublicKey(), identity.PrivateKey())
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create identity certificate: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse identity certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der}, PrivateKey: identity.PrivateKey(), Leaf: parsed,
	}, nil
}

func serverTLSConfig(certificate tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		ClientAuth: tls.RequireAnyClientCert, NextProtos: []string{controlALPN, pairingALPN},
		VerifyPeerCertificate: verifySelfSignedIdentity,
	}
}

func pairingClientTLSConfig(certificate tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		NextProtos: []string{pairingALPN}, InsecureSkipVerify: true, // Replaced by the strict callback below.
		VerifyPeerCertificate: verifySelfSignedIdentity,
	}
}

func verifySelfSignedIdentity(rawCertificates [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCertificates) != 1 || len(rawCertificates[0]) > maximumPairingFrame {
		return session.ErrInvalidPeer
	}
	certificate, err := x509.ParseCertificate(rawCertificates[0])
	if err != nil {
		return session.ErrInvalidPeer
	}
	publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize ||
		certificate.SignatureAlgorithm != x509.PureEd25519 ||
		certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 ||
		certificate.NotBefore.After(time.Now().UTC()) || certificate.NotAfter.Before(time.Now().UTC()) ||
		certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature) != nil {
		return session.ErrInvalidPeer
	}
	_, err = device.IDFromPublicKey(publicKey)
	return err
}

func peerFromTLS(state tls.ConnectionState, expectedProtocol string) (session.Peer, error) {
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != expectedProtocol ||
		len(state.PeerCertificates) != 1 {
		return session.Peer{}, session.ErrInvalidPeer
	}
	publicKey, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return session.Peer{}, session.ErrInvalidPeer
	}
	id, err := device.IDFromPublicKey(publicKey)
	if err != nil {
		return session.Peer{}, err
	}
	return session.Peer{ID: id, PublicKey: append(ed25519.PublicKey(nil), publicKey...)}, nil
}

func quicConfig() *quicgo.Config {
	return &quicgo.Config{
		HandshakeIdleTimeout:  pairingHandshakeLimit,
		MaxIdleTimeout:        pairingIdleLimit,
		KeepAlivePeriod:       15 * time.Second,
		MaxIncomingStreams:    64,
		MaxIncomingUniStreams: -1,
		Allow0RTT:             false,
	}
}

func writeFrame(writer io.Writer, message *computehopv1.PairingFrame) error {
	if message == nil || message.GetProtocolVersion() != pairingProtocolVersion || message.GetPayload() == nil {
		return session.ErrProtocol
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal pairing frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximumPairingFrame {
		return session.ErrProtocol
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	if err := writeAll(writer, length[:]); err != nil {
		return fmt.Errorf("write pairing frame length: %w", err)
	}
	if err := writeAll(writer, payload); err != nil {
		return fmt.Errorf("write pairing frame: %w", err)
	}
	return nil
}

func readFrame(reader io.Reader) (*computehopv1.PairingFrame, error) {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		return nil, fmt.Errorf("read pairing frame length: %w", err)
	}
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 || length > maximumPairingFrame {
		return nil, session.ErrProtocol
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read pairing frame: %w", err)
	}
	message := new(computehopv1.PairingFrame)
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, message); err != nil ||
		message.GetProtocolVersion() != pairingProtocolVersion || message.GetPayload() == nil ||
		hasUnknownPairingFields(message) {
		return nil, session.ErrProtocol
	}
	return message, nil
}

func hasUnknownPairingFields(message *computehopv1.PairingFrame) bool {
	if len(message.ProtoReflect().GetUnknown()) != 0 {
		return true
	}
	switch payload := message.GetPayload().(type) {
	case *computehopv1.PairingFrame_Hello:
		return payload.Hello == nil || len(payload.Hello.ProtoReflect().GetUnknown()) != 0
	case *computehopv1.PairingFrame_Decision:
		return payload.Decision == nil || len(payload.Decision.ProtoReflect().GetUnknown()) != 0
	case *computehopv1.PairingFrame_Error:
		return payload.Error == nil || len(payload.Error.ProtoReflect().GetUnknown()) != 0
	default:
		return true
	}
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
