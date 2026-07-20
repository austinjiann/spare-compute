package mdns

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

func TestAnnouncementTextParsesResolvedEntry(t *testing.T) {
	presenceID, err := device.NewPresenceID(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	announcement := device.Announcement{
		PresenceID: presenceID, Name: "Gaming PC", Role: device.RoleWorker,
		ProtocolVersion: device.DiscoveryProtocolVersion, Port: DefaultPort,
	}
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	observation, err := parseEntry(rawEntry{
		Instance: "Gaming PC (" + presenceID.Short() + ")",
		HostName: "gaming-pc.local.",
		Port:     DefaultPort, Text: announcementText(announcement), TTL: 120,
		IPv4: []net.IP{net.ParseIP("192.0.2.20"), net.ParseIP("192.0.2.20")},
		IPv6: []net.IP{net.ParseIP("2001:db8::20")},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Announcement != announcement {
		t.Fatalf("announcement = %#v, want %#v", observation.Announcement, announcement)
	}
	if len(observation.Addresses) != 2 || observation.ExpiresAt.Sub(now) != 45*time.Second {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestParseEntryRejectsForgedOrMalformedText(t *testing.T) {
	valid := []string{
		"txtvers=1",
		"sid=aaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name=Worker",
		"role=worker",
		"proto=1",
		"ready=false",
	}
	malformedIdentity := append([]string(nil), valid...)
	malformedIdentity[1] = "sid=bad"
	for _, test := range []struct {
		name string
		text []string
	}{
		{"duplicate", append(append([]string(nil), valid...), "name=forged")},
		{"malformed identity", malformedIdentity},
		{"missing", valid[:5]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseEntry(rawEntry{
				Instance: "Worker", HostName: "worker.local.", Port: DefaultPort,
				Text: test.text, TTL: 30, IPv4: []net.IP{net.ParseIP("192.0.2.1")},
			}, time.Now()); err == nil {
				t.Fatal("parseEntry() error = nil")
			}
		})
	}
}

func TestServiceRunReportsReadyObservationAndShutdown(t *testing.T) {
	presenceID, err := device.NewPresenceID(zeroReader{})
	if err != nil {
		t.Fatal(err)
	}
	announcement := device.Announcement{
		PresenceID: presenceID, Name: "Worker", Role: device.RoleWorker,
		ProtocolVersion: 1, Port: DefaultPort,
	}
	registered := &fakeRegistration{}
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		now:          time.Now,
		browseWindow: time.Second,
		register: func(device.Announcement) (registration, error) {
			return registered, nil
		},
		browse: func(ctx context.Context, entries chan<- rawEntry) error {
			entries <- rawEntry{
				Instance: "Worker", HostName: "worker.local.", Port: DefaultPort,
				Text: announcementText(announcement), TTL: 30,
				IPv4: []net.IP{net.ParseIP("192.0.2.2")},
			}
			<-ctx.Done()
			return nil
		},
	}
	ready := false
	observed := false
	if err := service.Run(ctx, announcement, func(device.Observation) {
		observed = true
		cancel()
	}, func() { ready = true }); err != nil {
		t.Fatal(err)
	}
	if !ready || !observed || !registered.shutdown {
		t.Fatalf("ready = %v, observed = %v, shutdown = %v", ready, observed, registered.shutdown)
	}
}

type fakeRegistration struct {
	shutdown bool
}

func (registration *fakeRegistration) Shutdown() { registration.shutdown = true }
func (*fakeRegistration) TTL(uint32)             {}

type zeroReader struct{}

func (zeroReader) Read(contents []byte) (int, error) {
	clear(contents)
	return len(contents), nil
}
