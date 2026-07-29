package connectivity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	EncryptedPayloadVersion = 1
	MaximumPlaintextBytes   = 24 << 10
	MaximumCiphertextBytes  = 32 << 10
)

var ErrInvalidEncryptedPayload = errors.New("invalid encrypted rendezvous payload")

// PayloadKind separates presence and signaling ciphertext domains.
type PayloadKind uint8

const (
	PayloadPresence PayloadKind = iota + 1
	PayloadSignal
)

// PayloadContext binds ciphertext to its role, direction, kind, and generation.
// Presence uses a non-zero generation; signals use generation zero.
type PayloadContext struct {
	Kind       PayloadKind
	RouteID    string
	Sender     device.Role
	Recipient  device.Role
	Generation uint64
}

// SealPayload encrypts one pair-scoped rendezvous payload with authenticated
// context. The hosted service never receives the secret or plaintext.
func SealPayload(
	secret trust.ConnectivitySecret,
	context PayloadContext,
	plaintext []byte,
) ([]byte, error) {
	return sealPayload(secret, context, plaintext, rand.Reader)
}

func sealPayload(
	secret trust.ConnectivitySecret,
	context PayloadContext,
	plaintext []byte,
	random io.Reader,
) ([]byte, error) {
	if !secret.Valid() || validatePayloadContext(context) != nil ||
		len(plaintext) == 0 || len(plaintext) > MaximumPlaintextBytes || random == nil {
		return nil, ErrInvalidEncryptedPayload
	}
	aead, err := payloadAEAD(secret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("%w: create nonce: %v", ErrInvalidEncryptedPayload, err)
	}
	result := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+aead.Overhead())
	result[0] = EncryptedPayloadVersion
	copy(result[1:], nonce)
	result = aead.Seal(result, nonce, plaintext, payloadAssociatedData(context))
	if len(result) > MaximumCiphertextBytes {
		return nil, ErrInvalidEncryptedPayload
	}
	return result, nil
}

// OpenPayload authenticates and decrypts a pair-scoped rendezvous payload.
func OpenPayload(
	secret trust.ConnectivitySecret,
	context PayloadContext,
	ciphertext []byte,
) ([]byte, error) {
	if !secret.Valid() || validatePayloadContext(context) != nil ||
		len(ciphertext) == 0 || len(ciphertext) > MaximumCiphertextBytes ||
		ciphertext[0] != EncryptedPayloadVersion {
		return nil, ErrInvalidEncryptedPayload
	}
	aead, err := payloadAEAD(secret)
	if err != nil {
		return nil, err
	}
	nonceEnd := 1 + aead.NonceSize()
	if len(ciphertext) < nonceEnd+aead.Overhead()+1 {
		return nil, ErrInvalidEncryptedPayload
	}
	plaintext, err := aead.Open(
		nil,
		ciphertext[1:nonceEnd],
		ciphertext[nonceEnd:],
		payloadAssociatedData(context),
	)
	if err != nil || len(plaintext) == 0 || len(plaintext) > MaximumPlaintextBytes {
		return nil, ErrInvalidEncryptedPayload
	}
	return plaintext, nil
}

func payloadAEAD(secret trust.ConnectivitySecret) (cipher.AEAD, error) {
	keyDigest := hmac.New(sha256.New, secret)
	_, _ = keyDigest.Write([]byte("ComputeHop rendezvous payload key v1"))
	block, err := aes.NewCipher(keyDigest.Sum(nil))
	if err != nil {
		return nil, fmt.Errorf("%w: create cipher: %v", ErrInvalidEncryptedPayload, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: create AEAD: %v", ErrInvalidEncryptedPayload, err)
	}
	return aead, nil
}

func validatePayloadContext(context PayloadContext) error {
	decodedRoute, err := base64.RawURLEncoding.DecodeString(context.RouteID)
	if err != nil || len(decodedRoute) != sha256.Size ||
		base64.RawURLEncoding.EncodeToString(decodedRoute) != context.RouteID {
		return ErrInvalidEncryptedPayload
	}
	if context.Sender != device.RoleOrchestrator && context.Sender != device.RoleWorker {
		return ErrInvalidEncryptedPayload
	}
	if context.Recipient != device.RoleOrchestrator && context.Recipient != device.RoleWorker {
		return ErrInvalidEncryptedPayload
	}
	if context.Sender == context.Recipient {
		return ErrInvalidEncryptedPayload
	}
	switch context.Kind {
	case PayloadPresence:
		if context.Generation == 0 {
			return ErrInvalidEncryptedPayload
		}
	case PayloadSignal:
		if context.Generation != 0 {
			return ErrInvalidEncryptedPayload
		}
	default:
		return ErrInvalidEncryptedPayload
	}
	return nil
}

func payloadAssociatedData(context PayloadContext) []byte {
	result := make([]byte, 0, 64)
	result = append(result, []byte("ComputeHop rendezvous payload context v1\x00")...)
	result = append(result, context.RouteID...)
	result = append(result, byte(context.Kind), roleByte(context.Sender), roleByte(context.Recipient))
	var generation [8]byte
	binary.BigEndian.PutUint64(generation[:], context.Generation)
	return append(result, generation[:]...)
}

func roleByte(role device.Role) byte {
	if role == device.RoleOrchestrator {
		return 1
	}
	if role == device.RoleWorker {
		return 2
	}
	return 0
}

// SealPresence encrypts an endpoint presence document.
func SealPresence(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	generation uint64,
	plaintext []byte,
) ([]byte, error) {
	return SealPayload(secret, PayloadContext{
		Kind: PayloadPresence, RouteID: routeID,
		Sender: sender, Recipient: oppositeDeviceRole(sender), Generation: generation,
	}, plaintext)
}

// OpenPresence authenticates and decrypts an endpoint presence document.
func OpenPresence(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	generation uint64,
	ciphertext []byte,
) ([]byte, error) {
	return OpenPayload(secret, PayloadContext{
		Kind: PayloadPresence, RouteID: routeID,
		Sender: sender, Recipient: oppositeDeviceRole(sender), Generation: generation,
	}, ciphertext)
}

// SealSignal encrypts one signaling document for the opposite endpoint.
func SealSignal(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	plaintext []byte,
) ([]byte, error) {
	return SealPayload(secret, PayloadContext{
		Kind: PayloadSignal, RouteID: routeID,
		Sender: sender, Recipient: oppositeDeviceRole(sender),
	}, plaintext)
}

// OpenSignal authenticates and decrypts one signaling document.
func OpenSignal(
	secret trust.ConnectivitySecret,
	routeID string,
	sender device.Role,
	ciphertext []byte,
) ([]byte, error) {
	return OpenPayload(secret, PayloadContext{
		Kind: PayloadSignal, RouteID: routeID,
		Sender: sender, Recipient: oppositeDeviceRole(sender),
	}, ciphertext)
}

func oppositeDeviceRole(role device.Role) device.Role {
	if role == device.RoleOrchestrator {
		return device.RoleWorker
	}
	if role == device.RoleWorker {
		return device.RoleOrchestrator
	}
	return ""
}
