// Package device models ComputeHop installation identities and nearby devices.
package device

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encodedIDLength = 52

var (
	ErrInvalidID       = errors.New("invalid device ID")
	ErrInvalidIdentity = errors.New("invalid device identity")
)

// ID is a public fingerprint used to correlate an installation. It is not
// proof of identity until a later authenticated pairing pins its public key.
type ID string

// ParseID validates a canonical lowercase SHA-256 identity fingerprint.
func ParseID(value string) (ID, error) {
	if len(value) != encodedIDLength || value != strings.ToLower(value) {
		return "", fmt.Errorf("%w: malformed fingerprint", ErrInvalidID)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%w: malformed fingerprint", ErrInvalidID)
	}
	return ID(value), nil
}

// Short returns a presentation-only prefix. It must never authorize a peer.
func (id ID) Short() string {
	if len(id) < 8 {
		return string(id)
	}
	return string(id[:8])
}

// Valid reports whether id is canonical.
func (id ID) Valid() bool {
	_, err := ParseID(string(id))
	return err == nil
}

// Identity owns one installation's long-lived Ed25519 key pair.
type Identity struct {
	id         ID
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

// GenerateIdentity creates a new installation identity from cryptographic entropy.
func GenerateIdentity(random io.Reader) (Identity, error) {
	if random == nil {
		return Identity{}, ErrInvalidIdentity
	}
	_, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return Identity{}, fmt.Errorf("generate Ed25519 identity: %w", err)
	}
	return NewIdentity(privateKey)
}

// NewIdentity validates and copies an Ed25519 private key.
func NewIdentity(privateKey ed25519.PrivateKey) (Identity, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Identity{}, fmt.Errorf("%w: private key length", ErrInvalidIdentity)
	}
	storedPrivate := append(ed25519.PrivateKey(nil), privateKey...)
	publicKey, ok := storedPrivate.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Identity{}, ErrInvalidIdentity
	}
	storedPublic := append(ed25519.PublicKey(nil), publicKey...)
	digest := sha256.Sum256(storedPublic)
	id := ID(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])))
	return Identity{id: id, publicKey: storedPublic, privateKey: storedPrivate}, nil
}

// ID returns the public installation fingerprint.
func (identity Identity) ID() ID {
	return identity.id
}

// PublicKey returns a copy of the identity's public key.
func (identity Identity) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), identity.publicKey...)
}

// PrivateKey returns a copy for secure persistence or authenticated transport.
func (identity Identity) PrivateKey() ed25519.PrivateKey {
	return append(ed25519.PrivateKey(nil), identity.privateKey...)
}

// Validate checks that the stored ID and key pair agree.
func (identity Identity) Validate() error {
	reconstructed, err := NewIdentity(identity.privateKey)
	if err != nil {
		return err
	}
	if reconstructed.id != identity.id || !identity.id.Valid() ||
		!identity.publicKey.Equal(reconstructed.publicKey) {
		return ErrInvalidIdentity
	}
	return nil
}
