// Package connectivity models anonymous, pair-scoped remote reachability.
package connectivity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/austinjiann/spare-compute/internal/trust"
)

const CredentialLifetime = 5 * time.Minute

var ErrInvalidCredentialMaterial = errors.New("invalid rendezvous credential material")

// Access contains the rotating opaque values presented to the hosted service.
// Neither value reveals a pair ID or durable device identity.
type Access struct {
	RouteID   string
	Token     string
	ExpiresAt time.Time
}

// DeriveAccess creates the same short-lived route and bearer token on both
// paired endpoints without sending the durable pair secret to the service.
func DeriveAccess(secret trust.ConnectivitySecret, at time.Time) (Access, error) {
	if !secret.Valid() || at.IsZero() || at.Unix() < 0 {
		return Access{}, ErrInvalidCredentialMaterial
	}
	epoch := at.Unix() / int64(CredentialLifetime/time.Second)
	var encodedEpoch [8]byte
	binary.BigEndian.PutUint64(encodedEpoch[:], uint64(epoch))
	route := derive(secret, "ComputeHop rendezvous route v1\x00", encodedEpoch[:])
	token := derive(secret, "ComputeHop rendezvous auth v1\x00", route)
	return Access{
		RouteID:   base64.RawURLEncoding.EncodeToString(route),
		Token:     base64.RawURLEncoding.EncodeToString(token),
		ExpiresAt: time.Unix((epoch+1)*int64(CredentialLifetime/time.Second), 0).UTC(),
	}, nil
}

func derive(secret trust.ConnectivitySecret, label string, contents []byte) []byte {
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte(label))
	_, _ = digest.Write(contents)
	return digest.Sum(nil)
}
