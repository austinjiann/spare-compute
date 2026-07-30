package device

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPresenceIDRoundTrip(t *testing.T) {
	id, err := NewPresenceID(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePresenceID(string(id))
	if err != nil || parsed != id || len(id.Short()) != 8 {
		t.Fatalf("ParsePresenceID() = %q, %v", parsed, err)
	}
	if _, err := ParsePresenceID("persistent-looking-id"); !errors.Is(err, ErrInvalidPresenceID) {
		t.Fatalf("ParsePresenceID(invalid) error = %v", err)
	}
}

func TestObservationValidationKeepsDiscoveryUntrusted(t *testing.T) {
	presenceID, err := NewPresenceID(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	observation := Observation{
		Key: "instance|host|47823",
		Announcement: Announcement{
			PresenceID: presenceID, Name: "Gaming PC", Role: RoleWorker,
			ProtocolVersion: DiscoveryProtocolVersion, Port: 47823,
		},
		Instance:  "Gaming PC (12345678)",
		HostName:  "gaming-pc.local.",
		Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		SeenAt:    now,
		ExpiresAt: now.Add(time.Minute),
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAnnouncementRejectsMalformedPlatformHints(t *testing.T) {
	presenceID, err := NewPresenceID(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	announcement := Announcement{
		PresenceID: presenceID, Name: "Gaming PC", Role: RoleWorker,
		ProtocolVersion: DiscoveryProtocolVersion, Port: 47823,
		Platform: "windows", Architecture: "amd64",
	}
	if err := announcement.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"bad value", "bad=value", strings.Repeat("a", 33)} {
		announcement.Platform = value
		if err := announcement.Validate(); !errors.Is(err, ErrInvalidAnnouncement) {
			t.Fatalf("Validate(%q) error = %v", value, err)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(contents []byte) (int, error) {
	clear(contents)
	return len(contents), nil
}
