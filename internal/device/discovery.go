package device

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const DiscoveryProtocolVersion uint32 = 1

const encodedPresenceIDLength = 26

type Role string

const (
	RoleWorker       Role = "worker"
	RoleOrchestrator Role = "orchestrator"
)

var (
	ErrInvalidAnnouncement = errors.New("invalid device announcement")
	ErrInvalidObservation  = errors.New("invalid nearby-device observation")
	ErrInvalidPresenceID   = errors.New("invalid discovery presence ID")
)

// PresenceID is an opaque, per-daemon-session discovery identifier. It is not
// the durable device identity and intentionally changes after daemon restart.
type PresenceID string

// NewPresenceID creates an unguessable LAN presence identifier.
func NewPresenceID(random io.Reader) (PresenceID, error) {
	if random == nil {
		return "", ErrInvalidPresenceID
	}
	contents := make([]byte, 16)
	if _, err := io.ReadFull(random, contents); err != nil {
		return "", fmt.Errorf("generate discovery presence ID: %w", err)
	}
	return PresenceID(strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(contents))), nil
}

// ParsePresenceID validates a canonical opaque discovery identifier.
func ParsePresenceID(value string) (PresenceID, error) {
	if len(value) != encodedPresenceIDLength || value != strings.ToLower(value) {
		return "", ErrInvalidPresenceID
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
	if err != nil || len(decoded) != 16 {
		return "", ErrInvalidPresenceID
	}
	return PresenceID(value), nil
}

func (id PresenceID) Valid() bool {
	_, err := ParsePresenceID(string(id))
	return err == nil
}

func (id PresenceID) Short() string {
	if len(id) < 8 {
		return string(id)
	}
	return string(id[:8])
}

// Announcement contains only untrusted LAN presentation and routing hints.
type Announcement struct {
	PresenceID      PresenceID
	Name            string
	Role            Role
	ProtocolVersion uint32
	Port            uint16
	EndpointReady   bool
}

func (announcement Announcement) Validate() error {
	if !announcement.PresenceID.Valid() || validateName(announcement.Name) != nil ||
		(announcement.Role != RoleWorker && announcement.Role != RoleOrchestrator) ||
		announcement.ProtocolVersion == 0 || announcement.Port == 0 {
		return ErrInvalidAnnouncement
	}
	return nil
}

// ObservationKey distinguishes untrusted advertisements without assuming IDs
// or human-readable names are unique or authentic.
type ObservationKey string

// Observation is one resolved DNS-SD service sighting.
type Observation struct {
	Key          ObservationKey
	Announcement Announcement
	Instance     string
	HostName     string
	Addresses    []netip.Addr
	SeenAt       time.Time
	ExpiresAt    time.Time
}

func (observation Observation) Validate() error {
	if observation.Key == "" || observation.Announcement.Validate() != nil ||
		strings.TrimSpace(observation.Instance) == "" ||
		strings.TrimSpace(observation.HostName) == "" ||
		observation.SeenAt.IsZero() || !observation.ExpiresAt.After(observation.SeenAt) {
		return ErrInvalidObservation
	}
	if len(observation.Addresses) == 0 {
		return fmt.Errorf("%w: address is required", ErrInvalidObservation)
	}
	for _, address := range observation.Addresses {
		if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
			return fmt.Errorf("%w: invalid address", ErrInvalidObservation)
		}
	}
	return nil
}

// NearbyDevice is a current, explicitly untrusted LAN observation.
type NearbyDevice struct {
	Observation
	FirstSeenAt time.Time
}

// DiscoverySnapshot is returned atomically to the local UI and CLI.
type DiscoverySnapshot struct {
	Devices       []NearbyDevice
	Available     bool
	LastError     string
	LastStartedAt *time.Time
}

// LANDiscovery advertises local presence and reports resolved nearby entries.
// ready is called only after the local announcement is active.
type LANDiscovery interface {
	Run(context.Context, Announcement, func(Observation), func()) error
}

func validateName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || len(name) > 80 || !utf8.ValidString(name) {
		return ErrInvalidAnnouncement
	}
	for _, value := range name {
		if unicode.IsControl(value) {
			return ErrInvalidAnnouncement
		}
	}
	return nil
}
