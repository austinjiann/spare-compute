package mapper

import (
	"fmt"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
)

// DiscoverySnapshotToProto maps the current explicitly untrusted nearby view.
func DiscoverySnapshotToProto(snapshot device.DiscoverySnapshot) (*localv1.ListDevicesResponse, error) {
	state := localv1.DiscoveryState_DISCOVERY_STATE_STARTING
	if snapshot.Available {
		state = localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE
	} else if snapshot.LastError != "" {
		state = localv1.DiscoveryState_DISCOVERY_STATE_UNAVAILABLE
	}
	response := &localv1.ListDevicesResponse{
		Devices:        make([]*localv1.NearbyDevice, len(snapshot.Devices)),
		DiscoveryState: state,
		DiscoveryError: snapshot.LastError,
	}
	if snapshot.LastStartedAt != nil {
		response.DiscoveryStartedAtUnixNano = snapshot.LastStartedAt.UnixNano()
	}
	for index, nearby := range snapshot.Devices {
		if err := nearby.Observation.Validate(); err != nil || nearby.FirstSeenAt.IsZero() {
			return nil, fmt.Errorf("map nearby device: %w", device.ErrInvalidObservation)
		}
		role := localv1.DeviceRole_DEVICE_ROLE_UNSPECIFIED
		switch nearby.Announcement.Role {
		case device.RoleWorker:
			role = localv1.DeviceRole_DEVICE_ROLE_WORKER
		case device.RoleOrchestrator:
			role = localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR
		default:
			return nil, device.ErrInvalidAnnouncement
		}
		addresses := make([]string, len(nearby.Addresses))
		for addressIndex, address := range nearby.Addresses {
			addresses[addressIndex] = address.String()
		}
		response.Devices[index] = &localv1.NearbyDevice{
			PresenceId: string(nearby.Announcement.PresenceID), Name: nearby.Announcement.Name,
			Role: role, ProtocolVersion: nearby.Announcement.ProtocolVersion,
			Instance: nearby.Instance, HostName: nearby.HostName,
			Addresses: addresses, Port: uint32(nearby.Announcement.Port),
			EndpointReady:       nearby.Announcement.EndpointReady,
			FirstSeenAtUnixNano: nearby.FirstSeenAt.UnixNano(),
			LastSeenAtUnixNano:  nearby.SeenAt.UnixNano(),
			ExpiresAtUnixNano:   nearby.ExpiresAt.UnixNano(),
			TrustState:          localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED,
			Platform:            nearby.Announcement.Platform,
			Arch:                nearby.Announcement.Architecture,
			LogicalCpuCount:     nearby.Announcement.LogicalCPUCount,
			TotalMemoryBytes:    nearby.Announcement.TotalMemoryBytes,
		}
	}
	return response, nil
}
