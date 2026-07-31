package mapper

import (
	"crypto/ed25519"
	"fmt"
	"slices"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/trust"
)

// PairingToProto maps a validated ephemeral pairing for the authenticated local UI.
func PairingToProto(value trust.Pairing) (*localv1.Pairing, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	role, err := roleToProto(value.PeerRole)
	if err != nil {
		return nil, err
	}
	direction := localv1.PairingDirection_PAIRING_DIRECTION_UNSPECIFIED
	switch value.Direction {
	case trust.DirectionOutbound:
		direction = localv1.PairingDirection_PAIRING_DIRECTION_OUTBOUND
	case trust.DirectionInbound:
		direction = localv1.PairingDirection_PAIRING_DIRECTION_INBOUND
	default:
		return nil, trust.ErrInvalidPairing
	}
	state := localv1.PairingState_PAIRING_STATE_UNSPECIFIED
	switch value.State {
	case trust.PairingWaiting:
		state = localv1.PairingState_PAIRING_STATE_WAITING
	case trust.PairingPaired:
		state = localv1.PairingState_PAIRING_STATE_PAIRED
	case trust.PairingRejected:
		state = localv1.PairingState_PAIRING_STATE_REJECTED
	case trust.PairingExpired:
		state = localv1.PairingState_PAIRING_STATE_EXPIRED
	case trust.PairingFailed:
		state = localv1.PairingState_PAIRING_STATE_FAILED
	default:
		return nil, trust.ErrInvalidPairing
	}
	return &localv1.Pairing{
		Id: string(value.ID), PeerDeviceId: string(value.PeerID),
		PeerPublicKey: append([]byte(nil), value.PeerPublicKey...), PeerName: value.PeerName,
		PeerRole: role, VerificationCode: string(value.Verification),
		Direction: direction, State: state,
		LocalConfirmed: value.LocalConfirmed, RemoteConfirmed: value.RemoteConfirmed,
		StartedAtUnixNano: value.StartedAt.UnixNano(), ExpiresAtUnixNano: value.ExpiresAt.UnixNano(),
		Failure: value.Failure,
	}, nil
}

// PairingFromProto validates a daemon response before CLI presentation.
func PairingFromProto(message *localv1.Pairing) (trust.Pairing, error) {
	if message == nil {
		return trust.Pairing{}, trust.ErrInvalidPairing
	}
	id, err := trust.ParsePairID(message.GetId())
	if err != nil {
		return trust.Pairing{}, err
	}
	peerID, err := device.ParseID(message.GetPeerDeviceId())
	if err != nil {
		return trust.Pairing{}, err
	}
	role, err := roleFromProto(message.GetPeerRole())
	if err != nil {
		return trust.Pairing{}, err
	}
	direction := trust.Direction("")
	switch message.GetDirection() {
	case localv1.PairingDirection_PAIRING_DIRECTION_OUTBOUND:
		direction = trust.DirectionOutbound
	case localv1.PairingDirection_PAIRING_DIRECTION_INBOUND:
		direction = trust.DirectionInbound
	}
	state := trust.PairingState("")
	switch message.GetState() {
	case localv1.PairingState_PAIRING_STATE_WAITING:
		state = trust.PairingWaiting
	case localv1.PairingState_PAIRING_STATE_PAIRED:
		state = trust.PairingPaired
	case localv1.PairingState_PAIRING_STATE_REJECTED:
		state = trust.PairingRejected
	case localv1.PairingState_PAIRING_STATE_EXPIRED:
		state = trust.PairingExpired
	case localv1.PairingState_PAIRING_STATE_FAILED:
		state = trust.PairingFailed
	}
	value := trust.Pairing{
		ID: id, PeerID: peerID, PeerPublicKey: append([]byte(nil), message.GetPeerPublicKey()...),
		PeerName: message.GetPeerName(), PeerRole: role,
		Verification: trust.VerificationCode(message.GetVerificationCode()),
		Direction:    direction, State: state,
		LocalConfirmed: message.GetLocalConfirmed(), RemoteConfirmed: message.GetRemoteConfirmed(),
		StartedAt: time.Unix(0, message.GetStartedAtUnixNano()).UTC(),
		ExpiresAt: time.Unix(0, message.GetExpiresAtUnixNano()).UTC(), Failure: message.GetFailure(),
	}
	if err := value.Validate(); err != nil {
		return trust.Pairing{}, err
	}
	return value, nil
}

// TrustedPeerToProto maps one durable public-key pin.
func TrustedPeerToProto(peer trust.Peer) (*localv1.TrustedDevice, error) {
	if err := peer.Validate(); err != nil {
		return nil, err
	}
	role, err := roleToProto(peer.Role)
	if err != nil {
		return nil, err
	}
	state := localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNSPECIFIED
	switch peer.State {
	case trust.StateActive:
		state = localv1.DeviceTrustState_DEVICE_TRUST_STATE_PAIRED
	case trust.StateRevoked:
		state = localv1.DeviceTrustState_DEVICE_TRUST_STATE_REVOKED
	default:
		return nil, trust.ErrInvalidPeer
	}
	message := &localv1.TrustedDevice{
		PairId: string(peer.PairID), DeviceId: string(peer.DeviceID),
		PublicKey: append([]byte(nil), peer.PublicKey...), Name: peer.Name,
		Role: role, TrustState: state,
		PairedAtUnixNano: peer.PairedAt.UnixNano(), UpdatedAtUnixNano: peer.UpdatedAt.UnixNano(),
		Platform: peer.Platform, Arch: peer.Architecture,
		LogicalCpuCount: peer.LogicalCPUCount, TotalMemoryBytes: peer.TotalMemoryBytes,
		ToolIds:            append([]string(nil), peer.ToolIDs...),
		SupportedExecutors: supportedExecutorsToProto(peer.SupportedExecutors),
	}
	if peer.RevokedAt != nil {
		message.RevokedAtUnixNano = peer.RevokedAt.UnixNano()
	}
	if peer.HintsObservedAt != nil {
		message.HintsObservedAtUnixNano = peer.HintsObservedAt.UnixNano()
	}
	return message, nil
}

// TrustedPeerFromProto validates a daemon response before CLI presentation.
func TrustedPeerFromProto(message *localv1.TrustedDevice) (trust.Peer, error) {
	if message == nil {
		return trust.Peer{}, trust.ErrInvalidPeer
	}
	pairID, err := trust.ParsePairID(message.GetPairId())
	if err != nil {
		return trust.Peer{}, err
	}
	deviceID, err := device.ParseID(message.GetDeviceId())
	if err != nil {
		return trust.Peer{}, err
	}
	role, err := roleFromProto(message.GetRole())
	if err != nil {
		return trust.Peer{}, err
	}
	state := trust.State("")
	switch message.GetTrustState() {
	case localv1.DeviceTrustState_DEVICE_TRUST_STATE_PAIRED:
		state = trust.StateActive
	case localv1.DeviceTrustState_DEVICE_TRUST_STATE_REVOKED:
		state = trust.StateRevoked
	}
	peer := trust.Peer{
		PairID: pairID, DeviceID: deviceID,
		PublicKey: ed25519.PublicKey(append([]byte(nil), message.GetPublicKey()...)),
		Name:      message.GetName(), Role: role, State: state,
		Platform: message.GetPlatform(), Architecture: message.GetArch(),
		LogicalCPUCount: message.GetLogicalCpuCount(), TotalMemoryBytes: message.GetTotalMemoryBytes(),
		ToolIDs:            append([]string(nil), message.GetToolIds()...),
		SupportedExecutors: supportedExecutorsFromProto(message.GetSupportedExecutors()),
		PairedAt:           time.Unix(0, message.GetPairedAtUnixNano()).UTC(),
		UpdatedAt:          time.Unix(0, message.GetUpdatedAtUnixNano()).UTC(),
	}
	if message.GetRevokedAtUnixNano() != 0 {
		revokedAt := time.Unix(0, message.GetRevokedAtUnixNano()).UTC()
		peer.RevokedAt = &revokedAt
	}
	if message.GetHintsObservedAtUnixNano() != 0 {
		observedAt := time.Unix(0, message.GetHintsObservedAtUnixNano()).UTC()
		peer.HintsObservedAt = &observedAt
	}
	if err := peer.Validate(); err != nil {
		return trust.Peer{}, err
	}
	return peer, nil
}

func supportedExecutorsToProto(values []string) []localv1.Executor {
	result := make([]localv1.Executor, 0, len(values))
	for _, value := range values {
		executor, err := executorToProto(job.Executor(value))
		if err == nil {
			result = append(result, executor)
		}
	}
	return result
}

func supportedExecutorsFromProto(values []localv1.Executor) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		executor, err := executorFromProto(value)
		if err == nil {
			result = append(result, string(executor))
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func roleToProto(role device.Role) (localv1.DeviceRole, error) {
	switch role {
	case device.RoleWorker:
		return localv1.DeviceRole_DEVICE_ROLE_WORKER, nil
	case device.RoleOrchestrator:
		return localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR, nil
	default:
		return localv1.DeviceRole_DEVICE_ROLE_UNSPECIFIED, fmt.Errorf("%w: role", trust.ErrInvalidPeer)
	}
}

func roleFromProto(role localv1.DeviceRole) (device.Role, error) {
	switch role {
	case localv1.DeviceRole_DEVICE_ROLE_WORKER:
		return device.RoleWorker, nil
	case localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR:
		return device.RoleOrchestrator, nil
	default:
		return "", fmt.Errorf("%w: role", trust.ErrInvalidPeer)
	}
}
