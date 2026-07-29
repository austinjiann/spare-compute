package icepath

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
	"google.golang.org/protobuf/proto"
)

const (
	PresenceProtocolVersion  = 1
	SessionIDBytes           = 16
	MaximumPresenceLifetime  = 2 * time.Minute
	maximumPresenceClockSkew = 30 * time.Second
)

var ErrInvalidPresence = errors.New("invalid encrypted ICE presence")

// Presence is the short-lived ICE description exchanged inside one encrypted
// rendezvous record. Generation is also bound into the outer AEAD context.
type Presence struct {
	Generation  uint64
	SessionID   [SessionIDBytes]byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Description Description
}

// NewPresence creates a fresh connection-attempt document. Generation must be
// monotonically increasing for a given endpoint within a rendezvous route.
func NewPresence(
	description Description,
	generation uint64,
	now time.Time,
	lifetime time.Duration,
) (Presence, error) {
	return newPresence(description, generation, now, lifetime, rand.Reader)
}

func newPresence(
	description Description,
	generation uint64,
	now time.Time,
	lifetime time.Duration,
	random io.Reader,
) (Presence, error) {
	if generation == 0 || now.IsZero() || now.UnixNano() <= 0 || lifetime <= 0 ||
		lifetime > MaximumPresenceLifetime || random == nil || description.Validate() != nil {
		return Presence{}, ErrInvalidPresence
	}
	presence := Presence{
		Generation: generation, CreatedAt: now.UTC(), ExpiresAt: now.Add(lifetime).UTC(),
		Description: description.Clone(),
	}
	if _, err := io.ReadFull(random, presence.SessionID[:]); err != nil {
		return Presence{}, fmt.Errorf("%w: create session ID: %v", ErrInvalidPresence, err)
	}
	if validatePresence(presence, time.Time{}) != nil {
		return Presence{}, ErrInvalidPresence
	}
	return presence, nil
}

// SealPresence serializes and encrypts a presence document for the paired peer.
func SealPresence(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	presence Presence,
) ([]byte, error) {
	message, err := presenceMessage(presence)
	if err != nil {
		return nil, err
	}
	plaintext, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil || len(plaintext) == 0 || len(plaintext) > connectivity.MaximumPlaintextBytes {
		return nil, ErrInvalidPresence
	}
	ciphertext, err := connectivity.SealPresence(
		secret, routeID, sender, presence.Generation, plaintext,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: encrypt payload", ErrInvalidPresence)
	}
	return ciphertext, nil
}

// OpenPresence decrypts, authenticates, and validates a peer ICE description.
func OpenPresence(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	generation uint64,
	ciphertext []byte,
	now time.Time,
) (Presence, error) {
	if generation == 0 || now.IsZero() || now.UnixNano() <= 0 {
		return Presence{}, ErrInvalidPresence
	}
	plaintext, err := connectivity.OpenPresence(secret, routeID, sender, generation, ciphertext)
	if err != nil {
		return Presence{}, fmt.Errorf("%w: decrypt payload", ErrInvalidPresence)
	}
	message := &connectivityv1.EndpointPresencePayload{}
	if err := proto.Unmarshal(plaintext, message); err != nil ||
		len(message.ProtoReflect().GetUnknown()) != 0 {
		return Presence{}, ErrInvalidPresence
	}
	presence, err := presenceFromMessage(message)
	if err != nil || presence.Generation != generation || validatePresence(presence, now.UTC()) != nil {
		return Presence{}, ErrInvalidPresence
	}
	return presence, nil
}

func presenceMessage(presence Presence) (*connectivityv1.EndpointPresencePayload, error) {
	if validatePresence(presence, time.Time{}) != nil {
		return nil, ErrInvalidPresence
	}
	return &connectivityv1.EndpointPresencePayload{
		ProtocolVersion:   PresenceProtocolVersion,
		Generation:        presence.Generation,
		SessionId:         append([]byte(nil), presence.SessionID[:]...),
		CreatedAtUnixNano: presence.CreatedAt.UTC().UnixNano(),
		ExpiresAtUnixNano: presence.ExpiresAt.UTC().UnixNano(),
		Ice: &connectivityv1.ICEPathDescription{
			UsernameFragment: presence.Description.Ufrag,
			Password:         presence.Description.Password,
			Candidates:       append([]string(nil), presence.Description.Candidates...),
		},
	}, nil
}

func presenceFromMessage(message *connectivityv1.EndpointPresencePayload) (Presence, error) {
	if message == nil || message.GetProtocolVersion() != PresenceProtocolVersion ||
		message.GetGeneration() == 0 || !validSessionID(message.GetSessionId()) ||
		message.GetCreatedAtUnixNano() <= 0 || message.GetExpiresAtUnixNano() <= 0 || message.GetIce() == nil {
		return Presence{}, ErrInvalidPresence
	}
	presence := Presence{
		Generation: message.GetGeneration(),
		CreatedAt:  time.Unix(0, message.GetCreatedAtUnixNano()).UTC(),
		ExpiresAt:  time.Unix(0, message.GetExpiresAtUnixNano()).UTC(),
		Description: Description{
			Ufrag: message.GetIce().GetUsernameFragment(), Password: message.GetIce().GetPassword(),
			Candidates: append([]string(nil), message.GetIce().GetCandidates()...),
		},
	}
	copy(presence.SessionID[:], message.GetSessionId())
	return presence, nil
}

func validatePresence(presence Presence, now time.Time) error {
	if presence.Generation == 0 || presence.CreatedAt.IsZero() || presence.ExpiresAt.IsZero() ||
		presence.CreatedAt.UnixNano() <= 0 || !presence.ExpiresAt.After(presence.CreatedAt) ||
		presence.ExpiresAt.Sub(presence.CreatedAt) > MaximumPresenceLifetime ||
		!validSessionID(presence.SessionID[:]) || presence.Description.Validate() != nil {
		return ErrInvalidPresence
	}
	if !now.IsZero() && (presence.CreatedAt.After(now.Add(maximumPresenceClockSkew)) || !now.Before(presence.ExpiresAt)) {
		return ErrInvalidPresence
	}
	return nil
}

func validSessionID(sessionID []byte) bool {
	if len(sessionID) != SessionIDBytes {
		return false
	}
	var combined byte
	for _, value := range sessionID {
		combined |= value
	}
	return combined != 0
}
