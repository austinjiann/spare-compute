package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

func TestDeviceServiceFiltersSelfMergesAddressesAndExpires(t *testing.T) {
	localPresence := testPresenceID(t, 0)
	remotePresence := testPresenceID(t, 1)
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	service := newDeviceServiceForTest(t, localPresence, &idleDiscovery{}, func() time.Time { return now })

	self := testObservation(localPresence, now, "192.0.2.1")
	service.observe(self)
	first := testObservation(remotePresence, now, "192.0.2.2")
	service.observe(first)
	second := testObservation(remotePresence, now.Add(time.Second), "2001:db8::2")
	service.observe(second)
	service.observe(first) // A replay must not replace a newer observation.

	snapshot, err := service.ListNearby(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 1 || len(snapshot.Devices[0].Addresses) != 2 {
		t.Fatalf("nearby devices = %#v", snapshot.Devices)
	}
	if snapshot.Devices[0].FirstSeenAt != now || snapshot.Devices[0].SeenAt != now.Add(time.Second) {
		t.Fatalf("observation times = %#v", snapshot.Devices[0])
	}

	now = second.ExpiresAt
	snapshot, err = service.ListNearby(context.Background())
	if err != nil || len(snapshot.Devices) != 0 {
		t.Fatalf("expired snapshot = %#v, %v", snapshot, err)
	}
}

func TestDeviceServiceReportsFailureAndRetries(t *testing.T) {
	presenceID := testPresenceID(t, 2)
	discovery := &retryDiscovery{secondStarted: make(chan struct{})}
	reported := make(chan error, 1)
	service, err := NewDeviceService(DeviceDependencies{
		Local: device.Announcement{
			PresenceID: presenceID, Name: "Mac", Role: device.RoleWorker,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: 47823,
		},
		Discovery: discovery,
		Now:       time.Now,
		ReportError: func(err error) {
			select {
			case reported <- err:
			default:
			}
		},
		RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- service.Run(ctx) }()
	select {
	case err := <-reported:
		if err.Error() != "multicast blocked" {
			t.Fatalf("reported error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("discovery failure was not reported")
	}
	select {
	case <-discovery.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("discovery was not retried")
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestDeviceServiceDoesNotCollapseConflictingIdentityClaims(t *testing.T) {
	localPresence := testPresenceID(t, 3)
	firstPresence := testPresenceID(t, 4)
	secondPresence := testPresenceID(t, 5)
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	service := newDeviceServiceForTest(t, localPresence, &idleDiscovery{}, func() time.Time { return now })
	service.observe(testObservation(firstPresence, now, "192.0.2.4"))
	service.observe(testObservation(secondPresence, now, "192.0.2.5"))
	snapshot, err := service.ListNearby(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Devices) != 2 {
		t.Fatalf("conflicting untrusted IDs were collapsed: %#v", snapshot.Devices)
	}
}

func newDeviceServiceForTest(
	t *testing.T,
	presenceID device.PresenceID,
	discovery device.LANDiscovery,
	now func() time.Time,
) *DeviceService {
	t.Helper()
	service, err := NewDeviceService(DeviceDependencies{
		Local: device.Announcement{
			PresenceID: presenceID, Name: "Mac", Role: device.RoleWorker,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: 47823,
		},
		Discovery: discovery,
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testPresenceID(t *testing.T, fill byte) device.PresenceID {
	t.Helper()
	presenceID, err := device.NewPresenceID(bytes.NewReader(bytes.Repeat([]byte{fill}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	return presenceID
}

func testObservation(presenceID device.PresenceID, seen time.Time, address string) device.Observation {
	return device.Observation{
		Key: "Worker|worker.local.|47823",
		Announcement: device.Announcement{
			PresenceID: presenceID, Name: "Worker", Role: device.RoleWorker,
			ProtocolVersion: device.DiscoveryProtocolVersion, Port: 47823,
		},
		Instance: "Worker", HostName: "worker.local.",
		Addresses: []netip.Addr{netip.MustParseAddr(address)},
		SeenAt:    seen, ExpiresAt: seen.Add(time.Minute),
	}
}

type idleDiscovery struct{}

func (*idleDiscovery) Run(ctx context.Context, _ device.Announcement, _ func(device.Observation), ready func()) error {
	ready()
	<-ctx.Done()
	return nil
}

type retryDiscovery struct {
	mu            sync.Mutex
	attempts      int
	secondStarted chan struct{}
}

func (discovery *retryDiscovery) Run(
	ctx context.Context,
	_ device.Announcement,
	_ func(device.Observation),
	ready func(),
) error {
	discovery.mu.Lock()
	discovery.attempts++
	attempt := discovery.attempts
	discovery.mu.Unlock()
	if attempt == 1 {
		return errors.New("multicast blocked")
	}
	if attempt == 2 {
		close(discovery.secondStarted)
	}
	ready()
	<-ctx.Done()
	return nil
}
