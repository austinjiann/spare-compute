// Package icepath gathers and selects direct or relayed UDP paths between
// already-paired ComputeHop devices.
package icepath

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"

	"github.com/austinjiann/spare-compute/internal/device"
)

const (
	maximumCandidates      = 64
	maximumCandidateLength = 2048
	minimumUfragLength     = 4
	maximumUfragLength     = 256
	minimumPasswordLength  = 22
	maximumPasswordLength  = 256
	maximumServers         = 8
	maximumServerSecret    = 512
)

var (
	ErrInvalidConfiguration = errors.New("invalid ICE path configuration")
	ErrInvalidDescription   = errors.New("invalid ICE path description")
	ErrInvalidSessionState  = errors.New("invalid ICE session state")
	ErrNoCandidates         = errors.New("ICE did not gather any candidates")
)

// Server configures one STUN or TURN endpoint. TURN credentials are kept
// separate from the URI so they do not appear in routine URL diagnostics.
type Server struct {
	URI      string
	Username string
	Password string
}

// Config controls candidate gathering. IPv4 UDP is always enabled; IPv6 is
// opt-in until its cross-platform behavior has physical coverage.
type Config struct {
	Servers    []Server
	EnableIPv6 bool
	RelayOnly  bool
}

// Description is the encrypted signaling document exchanged by paired peers.
type Description struct {
	Ufrag      string
	Password   string
	Candidates []string
}

// Clone returns a description with independent candidate storage.
func (description Description) Clone() Description {
	description.Candidates = append([]string(nil), description.Candidates...)
	return description
}

// Validate bounds credentials and parses every candidate before it is handed
// to Pion ICE.
func (description Description) Validate() error {
	if len(description.Ufrag) < minimumUfragLength || len(description.Ufrag) > maximumUfragLength ||
		len(description.Password) < minimumPasswordLength || len(description.Password) > maximumPasswordLength ||
		strings.ContainsRune(description.Ufrag, '\x00') || strings.ContainsRune(description.Password, '\x00') ||
		len(description.Candidates) == 0 || len(description.Candidates) > maximumCandidates {
		return ErrInvalidDescription
	}
	seen := make(map[string]struct{}, len(description.Candidates))
	for _, encoded := range description.Candidates {
		if len(encoded) == 0 || len(encoded) > maximumCandidateLength {
			return ErrInvalidDescription
		}
		if _, exists := seen[encoded]; exists {
			return ErrInvalidDescription
		}
		seen[encoded] = struct{}{}
		candidate, err := ice.UnmarshalCandidate(encoded)
		if err != nil || candidate.Component() != ice.ComponentRTP || !candidate.NetworkType().IsUDP() {
			return ErrInvalidDescription
		}
	}
	return nil
}

type sessionState uint8

const (
	stateNew sessionState = iota
	stateGathering
	stateGathered
	stateConnecting
	stateConnected
	stateClosed
)

// Session owns one Pion ICE agent and can establish one selected path.
type Session struct {
	agent *ice.Agent

	mu         sync.Mutex
	state      sessionState
	candidates []string
	gatherErr  error
	gatherDone chan struct{}
	doneOnce   sync.Once
	closeOnce  sync.Once
}

// NewSession creates an ICE agent without starting candidate gathering.
func NewSession(config Config) (*Session, error) {
	urls, hasTURN, err := parseServers(config.Servers)
	if err != nil || (config.RelayOnly && !hasTURN) {
		return nil, ErrInvalidConfiguration
	}
	networkTypes := []ice.NetworkType{ice.NetworkTypeUDP4}
	if config.EnableIPv6 {
		networkTypes = append(networkTypes, ice.NetworkTypeUDP6)
	}
	options := []ice.AgentOption{
		ice.WithUrls(urls),
		ice.WithNetworkTypes(networkTypes),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
	}
	if config.RelayOnly {
		options = append(options, ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeRelay}))
	}
	agent, err := ice.NewAgentWithOptions(options...)
	if err != nil {
		return nil, fmt.Errorf("%w: create agent: %v", ErrInvalidConfiguration, err)
	}
	session := &Session{agent: agent, state: stateNew, gatherDone: make(chan struct{})}
	if err := agent.OnCandidate(session.onCandidate); err != nil {
		_ = agent.Close()
		return nil, fmt.Errorf("%w: register candidate callback: %v", ErrInvalidConfiguration, err)
	}
	return session, nil
}

// Gather waits for a complete bounded, non-trickle local description.
func (session *Session) Gather(ctx context.Context) (Description, error) {
	if session == nil || session.agent == nil {
		return Description{}, ErrInvalidSessionState
	}
	session.mu.Lock()
	if session.state != stateNew {
		session.mu.Unlock()
		return Description{}, ErrInvalidSessionState
	}
	session.state = stateGathering
	session.mu.Unlock()
	if err := session.agent.GatherCandidates(); err != nil {
		_ = session.Close()
		return Description{}, fmt.Errorf("gather ICE candidates: %w", err)
	}
	select {
	case <-ctx.Done():
		_ = session.Close()
		return Description{}, ctx.Err()
	case <-session.gatherDone:
	}

	session.mu.Lock()
	gatherErr := session.gatherErr
	candidates := append([]string(nil), session.candidates...)
	session.mu.Unlock()
	if gatherErr != nil {
		_ = session.Close()
		return Description{}, gatherErr
	}
	if len(candidates) == 0 {
		_ = session.Close()
		return Description{}, ErrNoCandidates
	}
	ufrag, password, err := session.agent.GetLocalUserCredentials()
	if err != nil {
		return Description{}, fmt.Errorf("read local ICE credentials: %w", err)
	}
	description := Description{
		Ufrag: ufrag, Password: password, Candidates: candidates,
	}
	if err := description.Validate(); err != nil {
		return Description{}, err
	}
	session.mu.Lock()
	if session.state != stateGathering {
		session.mu.Unlock()
		return Description{}, ErrInvalidSessionState
	}
	session.state = stateGathered
	session.mu.Unlock()
	return description, nil
}

// Connect adds the authenticated peer description and performs ICE checks.
// The orchestrator is always controlling, avoiding role conflicts.
func (session *Session) Connect(
	ctx context.Context,
	localRole device.Role,
	remote Description,
) (*PacketConn, error) {
	if session == nil || session.agent == nil {
		return nil, ErrInvalidSessionState
	}
	if remote.Validate() != nil {
		return nil, ErrInvalidDescription
	}
	if localRole != device.RoleOrchestrator && localRole != device.RoleWorker {
		return nil, ErrInvalidConfiguration
	}
	session.mu.Lock()
	if session.state != stateGathered {
		session.mu.Unlock()
		return nil, ErrInvalidSessionState
	}
	session.state = stateConnecting
	session.mu.Unlock()

	for _, encoded := range remote.Candidates {
		candidate, err := ice.UnmarshalCandidate(encoded)
		if err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("%w: parse remote candidate", ErrInvalidDescription)
		}
		if err := session.agent.AddRemoteCandidate(candidate); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("add remote ICE candidate: %w", err)
		}
	}
	var (
		connection *ice.Conn
		err        error
	)
	if localRole == device.RoleOrchestrator {
		connection, err = session.agent.Dial(ctx, remote.Ufrag, remote.Password)
	} else {
		connection, err = session.agent.Accept(ctx, remote.Ufrag, remote.Password)
	}
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("select ICE path: %w", err)
	}
	session.mu.Lock()
	if session.state == stateClosed {
		session.mu.Unlock()
		_ = connection.Close()
		return nil, ErrInvalidSessionState
	}
	session.state = stateConnected
	session.mu.Unlock()
	return newPacketConn(connection, session.agent), nil
}

// Close stops gathering, connectivity checks, and keepalives.
func (session *Session) Close() error {
	if session == nil || session.agent == nil {
		return nil
	}
	var closeErr error
	session.closeOnce.Do(func() {
		session.mu.Lock()
		session.state = stateClosed
		session.mu.Unlock()
		closeErr = session.agent.Close()
		session.doneOnce.Do(func() { close(session.gatherDone) })
	})
	return closeErr
}

func (session *Session) onCandidate(candidate ice.Candidate) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state == stateClosed {
		return
	}
	if candidate == nil {
		session.doneOnce.Do(func() { close(session.gatherDone) })
		return
	}
	if len(session.candidates) >= maximumCandidates {
		session.gatherErr = fmt.Errorf("%w: more than %d candidates", ErrInvalidDescription, maximumCandidates)
		return
	}
	encoded := candidate.Marshal()
	if len(encoded) == 0 || len(encoded) > maximumCandidateLength ||
		candidate.Component() != ice.ComponentRTP || !candidate.NetworkType().IsUDP() {
		session.gatherErr = ErrInvalidDescription
		return
	}
	session.candidates = append(session.candidates, encoded)
}

func parseServers(servers []Server) ([]*stun.URI, bool, error) {
	if len(servers) > maximumServers {
		return nil, false, ErrInvalidConfiguration
	}
	urls := make([]*stun.URI, 0, len(servers))
	hasTURN := false
	for _, server := range servers {
		uri, err := stun.ParseURI(server.URI)
		if err != nil {
			return nil, false, err
		}
		switch uri.Scheme {
		case stun.SchemeTypeTURN, stun.SchemeTypeTURNS:
			if server.Username == "" || server.Password == "" ||
				len(server.Username) > maximumServerSecret || len(server.Password) > maximumServerSecret ||
				strings.ContainsRune(server.Username, '\x00') || strings.ContainsRune(server.Password, '\x00') {
				return nil, false, ErrInvalidConfiguration
			}
			uri.Username, uri.Password = server.Username, server.Password
			hasTURN = true
		case stun.SchemeTypeSTUN, stun.SchemeTypeSTUNS:
			if server.Username != "" || server.Password != "" {
				return nil, false, ErrInvalidConfiguration
			}
		default:
			return nil, false, ErrInvalidConfiguration
		}
		urls = append(urls, uri)
	}
	return urls, hasTURN, nil
}

// Path describes the selected candidate pair without exposing credentials.
type Path struct {
	Kind       string
	LocalType  string
	RemoteType string
	LocalAddr  string
	RemoteAddr string
}

// PacketConn adapts Pion's connected datagram path to net.PacketConn for QUIC.
type PacketConn struct {
	connection *ice.Conn
	agent      *ice.Agent
	closeOnce  sync.Once
}

var _ net.PacketConn = (*PacketConn)(nil)

func newPacketConn(connection *ice.Conn, agent *ice.Agent) *PacketConn {
	return &PacketConn{connection: connection, agent: agent}
}

func (connection *PacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	if connection == nil || connection.connection == nil {
		return 0, nil, net.ErrClosed
	}
	read, err := connection.connection.Read(buffer)
	return read, connection.connection.RemoteAddr(), err
}

func (connection *PacketConn) WriteTo(buffer []byte, _ net.Addr) (int, error) {
	if connection == nil || connection.connection == nil {
		return 0, net.ErrClosed
	}
	return connection.connection.Write(buffer)
}

func (connection *PacketConn) Close() error {
	if connection == nil || connection.connection == nil {
		return nil
	}
	var closeErr error
	connection.closeOnce.Do(func() { closeErr = connection.connection.Close() })
	return closeErr
}

func (connection *PacketConn) LocalAddr() net.Addr {
	if connection == nil || connection.connection == nil {
		return nil
	}
	return connection.connection.LocalAddr()
}

// RemoteAddr returns the currently selected peer address.
func (connection *PacketConn) RemoteAddr() net.Addr {
	if connection == nil || connection.connection == nil {
		return nil
	}
	return connection.connection.RemoteAddr()
}

func (connection *PacketConn) SetDeadline(deadline time.Time) error {
	if connection == nil || connection.connection == nil {
		return net.ErrClosed
	}
	return connection.connection.SetDeadline(deadline)
}

func (connection *PacketConn) SetReadDeadline(deadline time.Time) error {
	if connection == nil || connection.connection == nil {
		return net.ErrClosed
	}
	return connection.connection.SetReadDeadline(deadline)
}

func (connection *PacketConn) SetWriteDeadline(deadline time.Time) error {
	if connection == nil || connection.connection == nil {
		return net.ErrClosed
	}
	return connection.connection.SetWriteDeadline(deadline)
}

// SelectedPath reports whether ICE chose host, server-reflexive, or relay
// candidates. It never returns TURN credentials.
func (connection *PacketConn) SelectedPath() (Path, error) {
	if connection == nil || connection.agent == nil {
		return Path{}, net.ErrClosed
	}
	pair, err := connection.agent.GetSelectedCandidatePair()
	if err != nil {
		return Path{}, err
	}
	if pair == nil || pair.Local == nil || pair.Remote == nil {
		return Path{}, ErrInvalidSessionState
	}
	kind := "host"
	if pair.Local.Type() == ice.CandidateTypeRelay || pair.Remote.Type() == ice.CandidateTypeRelay {
		kind = "relay"
	} else if pair.Local.Type() == ice.CandidateTypeServerReflexive ||
		pair.Remote.Type() == ice.CandidateTypeServerReflexive ||
		pair.Local.Type() == ice.CandidateTypePeerReflexive ||
		pair.Remote.Type() == ice.CandidateTypePeerReflexive {
		kind = "server-reflexive"
	}
	return Path{
		Kind: kind, LocalType: pair.Local.Type().String(), RemoteType: pair.Remote.Type().String(),
		LocalAddr:  net.JoinHostPort(pair.Local.Address(), strconv.Itoa(pair.Local.Port())),
		RemoteAddr: net.JoinHostPort(pair.Remote.Address(), strconv.Itoa(pair.Remote.Port())),
	}, nil
}
