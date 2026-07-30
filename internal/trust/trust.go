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
	"unicode"
	"unicode/utf8"

	"github.com/austinjiann/spare-compute/internal/device"
)

const encodedPairIDLength = 26

// ConnectivitySecretBytes is the size of pair-scoped secret material derived
// from the confirmed TLS pairing session.
const ConnectivitySecretBytes = 32

var (
	ErrInvalidPairID = errors.New("invalid pair ID")
	ErrInvalidPeer   = errors.New("invalid trusted peer")
	ErrInvalidHints  = errors.New("invalid trusted peer hints")
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
	Platform           string
	Architecture       string
	LogicalCPUCount    uint32
	TotalMemoryBytes   uint64
	HintsObservedAt    *time.Time
	PairedAt           time.Time
	UpdatedAt          time.Time
	RevokedAt          *time.Time
}

// PeerHints are non-authoritative compatibility and resource hints last
// observed for an already trusted peer. They may influence scheduling and UI,
// but trust still comes only from the pinned peer public key.
type PeerHints struct {
	Platform         string
	Architecture     string
	LogicalCPUCount  uint32
	TotalMemoryBytes uint64
	ObservedAt       time.Time
}

func (hints PeerHints) Validate() error {
	if hints.ObservedAt.IsZero() ||
		validateHint(hints.Platform) != nil ||
		validateHint(hints.Architecture) != nil ||
		hints.LogicalCPUCount > 4096 {
		return ErrInvalidHints
	}
	return nil
}

// Validate checks identity binding, lifecycle timestamps, and state invariants.
func (peer Peer) Validate() error {
	derivedID, err := device.IDFromPublicKey(peer.PublicKey)
	if err != nil || !peer.PairID.Valid() || peer.DeviceID != derivedID ||
		device.ValidateName(peer.Name) != nil ||
		(len(peer.ConnectivitySecret) != 0 && !peer.ConnectivitySecret.Valid()) ||
		(peer.Role != device.RoleWorker && peer.Role != device.RoleOrchestrator) ||
		validateHint(peer.Platform) != nil || validateHint(peer.Architecture) != nil ||
		peer.LogicalCPUCount > 4096 ||
		peer.PairedAt.IsZero() || peer.UpdatedAt.Before(peer.PairedAt) {
		return ErrInvalidPeer
	}
	if peer.HintsObservedAt == nil {
		if peer.Platform != "" || peer.Architecture != "" ||
			peer.LogicalCPUCount != 0 || peer.TotalMemoryBytes != 0 {
			return ErrInvalidPeer
		}
	} else if peer.HintsObservedAt.IsZero() {
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
	if peer.HintsObservedAt != nil {
		observedAt := *peer.HintsObservedAt
		peer.HintsObservedAt = &observedAt
	}
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
	UpdateHints(context.Context, device.ID, PeerHints) (Peer, error)
}

// MatchNearbyHints conservatively associates untrusted nearby LAN sightings
// with already trusted peers. A hint is returned only when exactly one active
// trusted worker and exactly one endpoint-ready nearby worker share the same
// presentation name. The returned hints are not identity proof.
func MatchNearbyHints(peers []Peer, nearby []device.NearbyDevice) map[device.ID]PeerHints {
	peerByName := make(map[string][]Peer)
	for _, peer := range peers {
		if peer.State == StateActive && peer.Role == device.RoleWorker {
			peerByName[peer.Name] = append(peerByName[peer.Name], peer)
		}
	}
	nearbyByName := make(map[string][]device.NearbyDevice)
	for _, value := range nearby {
		announcement := value.Announcement
		if announcement.Role == device.RoleWorker && announcement.EndpointReady {
			nearbyByName[announcement.Name] = append(nearbyByName[announcement.Name], value)
		}
	}
	result := make(map[device.ID]PeerHints)
	for name, matchingPeers := range peerByName {
		matchingNearby := nearbyByName[name]
		if len(matchingPeers) != 1 || len(matchingNearby) != 1 {
			continue
		}
		announcement := matchingNearby[0].Announcement
		result[matchingPeers[0].DeviceID] = PeerHints{
			Platform: announcement.Platform, Architecture: announcement.Architecture,
			LogicalCPUCount: announcement.LogicalCPUCount, TotalMemoryBytes: announcement.TotalMemoryBytes,
			ObservedAt: matchingNearby[0].SeenAt.UTC(),
		}
	}
	return result
}

func validateHint(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > 32 || !utf8.ValidString(value) {
		return ErrInvalidHints
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '=' {
			return ErrInvalidHints
		}
	}
	return nil
}
