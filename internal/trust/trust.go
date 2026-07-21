// Package trust models durable, explicitly confirmed relationships between devices.
package trust

import (
	"context"
	"crypto/ed25519"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

const encodedPairIDLength = 26

// ConnectivitySecretBytes is the size of pair-scoped secret material derived
// from the confirmed TLS pairing session.
const ConnectivitySecretBytes = 32

var (
	ErrInvalidPairID = errors.New("invalid pair ID")
	ErrInvalidPeer   = errors.New("invalid trusted peer")
	ErrNotFound      = errors.New("trusted peer not found")
	ErrConflict      = errors.New("trusted peer conflicts with existing trust")
)

// PairID is an opaque, pair-scoped identifier. It is not a secret or a device ID.
type PairID string

// ConnectivitySecret derives anonymous rendezvous credentials. It must never
// be exposed through local UI protocols or sent to the connectivity service.
type ConnectivitySecret []byte

// Valid reports whether the secret has the required entropy-bearing length.
func (secret ConnectivitySecret) Valid() bool {
	return len(secret) == ConnectivitySecretBytes
}

// NewPairID creates an unguessable pair-scoped identifier.
func NewPairID(random io.Reader) (PairID, error) {
	if random == nil {
		return "", ErrInvalidPairID
	}
	contents := make([]byte, 16)
	if _, err := io.ReadFull(random, contents); err != nil {
		return "", fmt.Errorf("generate pair ID: %w", err)
	}
	return PairID(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(contents))), nil
}

// ParsePairID validates a canonical opaque pair identifier.
func ParsePairID(value string) (PairID, error) {
	if len(value) != encodedPairIDLength || value != strings.ToLower(value) {
		return "", ErrInvalidPairID
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
	if err != nil || len(decoded) != 16 {
		return "", ErrInvalidPairID
	}
	return PairID(value), nil
}

func (id PairID) Valid() bool {
	_, err := ParsePairID(string(id))
	return err == nil
}

func (id PairID) Short() string {
	if len(id) < 8 {
		return string(id)
	}
	return string(id[:8])
}

// State describes whether a pinned peer may authenticate future sessions.
type State string

const (
	StateActive  State = "active"
	StateRevoked State = "revoked"
)

// Peer is a durable public-key pin created only after two-sided confirmation.
type Peer struct {
	PairID             PairID
	DeviceID           device.ID
	PublicKey          ed25519.PublicKey
	ConnectivitySecret ConnectivitySecret
	Name               string
	Role               device.Role
	State              State
	PairedAt           time.Time
	UpdatedAt          time.Time
	RevokedAt          *time.Time
}

// Validate checks identity binding, lifecycle timestamps, and state invariants.
func (peer Peer) Validate() error {
	derivedID, err := device.IDFromPublicKey(peer.PublicKey)
	if err != nil || !peer.PairID.Valid() || peer.DeviceID != derivedID ||
		device.ValidateName(peer.Name) != nil ||
		(len(peer.ConnectivitySecret) != 0 && !peer.ConnectivitySecret.Valid()) ||
		(peer.Role != device.RoleWorker && peer.Role != device.RoleOrchestrator) ||
		peer.PairedAt.IsZero() || peer.UpdatedAt.Before(peer.PairedAt) {
		return ErrInvalidPeer
	}
	switch peer.State {
	case StateActive:
		if peer.RevokedAt != nil {
			return ErrInvalidPeer
		}
	case StateRevoked:
		if peer.RevokedAt == nil || peer.RevokedAt.Before(peer.PairedAt) ||
			!peer.UpdatedAt.Equal(*peer.RevokedAt) {
			return ErrInvalidPeer
		}
	default:
		return ErrInvalidPeer
	}
	return nil
}

// Clone returns a copy that does not share key or timestamp storage.
func (peer Peer) Clone() Peer {
	peer.PublicKey = append(ed25519.PublicKey(nil), peer.PublicKey...)
	peer.ConnectivitySecret = append(ConnectivitySecret(nil), peer.ConnectivitySecret...)
	if peer.RevokedAt != nil {
		revokedAt := *peer.RevokedAt
		peer.RevokedAt = &revokedAt
	}
	return peer
}

// Repository is the durable trust boundary used by pairing and session authentication.
type Repository interface {
	Activate(context.Context, Peer) error
	Get(context.Context, device.ID) (Peer, error)
	List(context.Context) ([]Peer, error)
	Revoke(context.Context, device.ID, time.Time) (Peer, error)
}
