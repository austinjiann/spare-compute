package mapper

import (
	"bytes"
	"net/netip"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
)

func TestDiscoverySnapshotToProtoMarksEveryObservationUnpaired(t *testing.T) {
	presenceID, err := device.NewPresenceID(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Second)
	message, err := DiscoverySnapshotToProto(device.DiscoverySnapshot{
		Available: true, LastStartedAt: &started,
		Devices: []device.NearbyDevice{{
			Observation: device.Observation{
				Key: "worker|worker.local.|47823",
				Announcement: device.Announcement{
					PresenceID: presenceID, Name: "Worker", Role: device.RoleWorker,
					ProtocolVersion: 1, Port: 47823,
				},
				Instance: "Worker", HostName: "worker.local.",
				Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
				SeenAt:    now, ExpiresAt: now.Add(time.Minute),
			},
			FirstSeenAt: started,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.GetDiscoveryState() != localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE ||
		len(message.GetDevices()) != 1 ||
		message.GetDevices()[0].GetTrustState() != localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED {
		t.Fatalf("message = %#v", message)
	}
}
