package trust

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

const (
	PairingBindingBytes = 32
	verificationBytes   = 10
)

var (
	ErrInvalidPairing     = errors.New("invalid pairing")
	ErrPairingNotFound    = errors.New("pairing not found")
	ErrPairingUnavailable = errors.New("pairing unavailable")
)

// PairingState is the local view of one ephemeral verification ceremony.
type PairingState string

const (
	PairingWaiting  PairingState = "waiting"
	PairingPaired   PairingState = "paired"
	PairingRejected PairingState = "rejected"
	PairingExpired  PairingState = "expired"
	PairingFailed   PairingState = "failed"
)

// Direction identifies which device initiated a pairing connection.
type Direction string

const (
	DirectionOutbound Direction = "outbound"
	DirectionInbound  Direction = "inbound"
)

// VerificationCode is compared by the user on both devices. It never authorizes alone.
type VerificationCode string

// Pairing is an ephemeral, connection-bound confirmation ceremony.
type Pairing struct {
	ID              PairID
	PeerID          device.ID
	PeerPublicKey   []byte
	PeerName        string
	PeerRole        device.Role
	Verification    VerificationCode
	Direction       Direction
	State           PairingState
	LocalConfirmed  bool
	RemoteConfirmed bool
	StartedAt       time.Time
	ExpiresAt       time.Time
	Failure         string
}

func (pairing Pairing) Validate() error {
	derivedID, err := device.IDFromPublicKey(pairing.PeerPublicKey)
	if err != nil || pairing.PeerID != derivedID || !pairing.ID.Valid() ||
		device.ValidateName(pairing.PeerName) != nil ||
		(pairing.PeerRole != device.RoleWorker && pairing.PeerRole != device.RoleOrchestrator) ||
		(pairing.Direction != DirectionInbound && pairing.Direction != DirectionOutbound) ||
		pairing.StartedAt.IsZero() || !pairing.ExpiresAt.After(pairing.StartedAt) ||
		!validVerificationCode(pairing.Verification) {
		return ErrInvalidPairing
	}
	switch pairing.State {
	case PairingWaiting:
		if pairing.Failure != "" {
			return ErrInvalidPairing
		}
	case PairingPaired:
		if !pairing.LocalConfirmed || !pairing.RemoteConfirmed || pairing.Failure != "" {
			return ErrInvalidPairing
		}
	case PairingRejected, PairingExpired:
		if pairing.Failure != "" {
			return ErrInvalidPairing
		}
	case PairingFailed:
		if strings.TrimSpace(pairing.Failure) == "" {
			return ErrInvalidPairing
		}
	default:
		return ErrInvalidPairing
	}
	return nil
}

func (pairing Pairing) Clone() Pairing {
	pairing.PeerPublicKey = append([]byte(nil), pairing.PeerPublicKey...)
	return pairing
}

// DerivePairing material deterministically binds the display code and pair ID
// to this TLS connection and the ordered initiator/responder identities.
func DerivePairing(binding []byte, initiatorID, responderID device.ID) (PairID, VerificationCode, error) {
	context, err := pairingContext(binding, initiatorID, responderID)
	if err != nil {
		return "", "", err
	}

	idDigest := sha256.Sum256(append(append([]byte(nil), "pair-id\x00"...), context...))
	id := PairID(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(idDigest[:16])))
	codeDigest := sha256.Sum256(append(append([]byte(nil), "verify\x00"...), context...))
	encoded := crockford(codeDigest[:verificationBytes])
	code := VerificationCode(fmt.Sprintf("%s-%s-%s-%s", encoded[:4], encoded[4:8], encoded[8:12], encoded[12:16]))
	return id, code, nil
}

// DeriveConnectivitySecret creates pair-scoped secret material from the same
// confirmed TLS exporter used by the display code. Both endpoints derive the
// same bytes without transmitting them.
func DeriveConnectivitySecret(
	binding []byte,
	initiatorID device.ID,
	responderID device.ID,
) (ConnectivitySecret, error) {
	context, err := pairingContext(binding, initiatorID, responderID)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append(append([]byte(nil), "connectivity-secret\x00"...), context...))
	return append(ConnectivitySecret(nil), digest[:]...), nil
}

func pairingContext(binding []byte, initiatorID, responderID device.ID) ([]byte, error) {
	if len(binding) != PairingBindingBytes || !initiatorID.Valid() || !responderID.Valid() ||
		subtle.ConstantTimeCompare([]byte(initiatorID), []byte(responderID)) == 1 {
		return nil, ErrInvalidPairing
	}
	context := make([]byte, 0, len(binding)+len(initiatorID)+len(responderID)+32)
	context = append(context, "ComputeHop pairing v1\x00"...)
	context = append(context, binding...)
	context = append(context, initiatorID...)
	context = append(context, 0)
	context = append(context, responderID...)

	return context, nil
}

func validVerificationCode(code VerificationCode) bool {
	parts := strings.Split(string(code), "-")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if len(part) != 4 {
			return false
		}
		for _, value := range part {
			if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", value) {
				return false
			}
		}
	}
	return true
}

func crockford(contents []byte) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var (
		result strings.Builder
		buffer uint32
		bits   uint
	)
	for _, value := range contents {
		buffer = (buffer << 8) | uint32(value)
		bits += 8
		for bits >= 5 {
			bits -= 5
			result.WriteByte(alphabet[(buffer>>bits)&31])
		}
	}
	if bits > 0 {
		result.WriteByte(alphabet[(buffer<<(5-bits))&31])
	}
	return result.String()
}
