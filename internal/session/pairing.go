// Package session defines transport-independent authenticated connection boundaries.
package session

import (
	"context"
	"crypto/ed25519"
	"errors"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

var (
	ErrInvalidEndpoint = errors.New("invalid pairing endpoint")
	ErrInvalidPeer     = errors.New("invalid pairing peer")
	ErrProtocol        = errors.New("pairing protocol error")
)

// LocalDevice supplies the identity and presentation fields bound into a pairing handshake.
type LocalDevice struct {
	Identity   device.Identity
	Name       string
	Role       device.Role
	PresenceID device.PresenceID
}

func (local LocalDevice) Validate() error {
	if local.Identity.Validate() != nil || device.ValidateName(local.Name) != nil ||
		(local.Role != device.RoleWorker && local.Role != device.RoleOrchestrator) ||
		!local.PresenceID.Valid() {
		return ErrInvalidPeer
	}
	return nil
}

// Peer is authenticated by proof of possession of PublicKey on the active TLS session.
type Peer struct {
	ID         device.ID
	PublicKey  ed25519.PublicKey
	Name       string
	Role       device.Role
	PresenceID device.PresenceID
}

func (peer Peer) Validate() error {
	id, err := device.IDFromPublicKey(peer.PublicKey)
	if err != nil || id != peer.ID || device.ValidateName(peer.Name) != nil ||
		(peer.Role != device.RoleWorker && peer.Role != device.RoleOrchestrator) ||
		!peer.PresenceID.Valid() {
		return ErrInvalidPeer
	}
	return nil
}

// PairingDecision is a connection-bound local confirmation or rejection.
type PairingDecision struct {
	PairingID trust.PairID
	Confirmed bool
	Committed bool
}

// PairingChannel is a mutually authenticated, encrypted ceremony channel.
// One goroutine may receive while another sends.
type PairingChannel interface {
	Peer() Peer
	Binding() []byte
	SendDecision(context.Context, PairingDecision) error
	ReceiveDecision(context.Context) (PairingDecision, error)
	Close() error
}

// PairingEndpoint owns the shared QUIC listener and can initiate pairings with
// an explicitly selected, still-untrusted discovery observation.
type PairingEndpoint interface {
	Port() uint16
	Run(context.Context, func(PairingChannel)) error
	Dial(context.Context, device.NearbyDevice) (PairingChannel, error)
	Close() error
}
