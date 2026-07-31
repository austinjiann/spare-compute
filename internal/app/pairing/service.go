// Package pairing coordinates explicit two-device verification and durable trust.
package pairing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	defaultPairingLifetime = 5 * time.Minute
	maximumPendingPairings = 16
	decisionSendTimeout    = 5 * time.Second
	pairingConnectTimeout  = 12 * time.Second
)

var ErrMissingDependency = errors.New("pairing service dependency is required")

// NearbyResolver selects an ephemeral endpoint without treating it as identity.
type NearbyResolver interface {
	ResolveNearby(context.Context, string) (device.NearbyDevice, error)
}

// Dependencies configure a daemon's pairing coordinator.
type Dependencies struct {
	Local       session.LocalDevice
	Nearby      NearbyResolver
	Trust       trust.Repository
	Endpoint    session.PairingDialer
	Now         func() time.Time
	Lifetime    time.Duration
	ReportError func(error)
}

// Service owns ephemeral pairing ceremonies and durable trust operations.
type Service struct {
	local       session.LocalDevice
	nearby      NearbyResolver
	repository  trust.Repository
	endpoint    session.PairingDialer
	now         func() time.Time
	lifetime    time.Duration
	reportError func(error)

	mu       sync.Mutex
	pairings map[trust.PairID]*pending
}

type pending struct {
	value              trust.Pairing
	connectivitySecret trust.ConnectivitySecret
	channel            session.PairingChannel
	activating         bool
	localCommitted     bool
	remoteCommitted    bool
}

// NewService validates and creates a pairing coordinator.
func NewService(dependencies Dependencies) (*Service, error) {
	if dependencies.Local.Validate() != nil || dependencies.Nearby == nil || dependencies.Trust == nil ||
		dependencies.Endpoint == nil || dependencies.Now == nil {
		return nil, ErrMissingDependency
	}
	if dependencies.Lifetime == 0 {
		dependencies.Lifetime = defaultPairingLifetime
	}
	if dependencies.Lifetime < time.Minute || dependencies.Lifetime > 15*time.Minute {
		return nil, fmt.Errorf("%w: pairing lifetime must be between 1 and 15 minutes", ErrMissingDependency)
	}
	if dependencies.ReportError == nil {
		dependencies.ReportError = func(error) {}
	}
	if err := ValidateLocalRole(context.Background(), dependencies.Local.Role, dependencies.Trust); err != nil {
		return nil, err
	}
	return &Service{
		local: dependencies.Local, nearby: dependencies.Nearby, repository: dependencies.Trust,
		endpoint: dependencies.Endpoint, now: dependencies.Now, lifetime: dependencies.Lifetime,
		reportError: dependencies.ReportError, pairings: make(map[trust.PairID]*pending),
	}, nil
}

// ValidateLocalRole prevents changing a paired installation between worker and
// orchestrator semantics while active pins still exist.
func ValidateLocalRole(ctx context.Context, localRole device.Role, repository trust.Repository) error {
	if repository == nil || (localRole != device.RoleWorker && localRole != device.RoleOrchestrator) {
		return ErrMissingDependency
	}
	peers, err := repository.List(ctx)
	if err != nil {
		return err
	}
	for _, peer := range peers {
		if peer.State != trust.StateActive {
			continue
		}
		if localRole == device.RoleWorker && peer.Role != device.RoleOrchestrator {
			return fmt.Errorf("%w: unpair active workers before changing this device to worker role", trust.ErrConflict)
		}
		if localRole == device.RoleOrchestrator && peer.Role != device.RoleWorker {
			return fmt.Errorf("%w: unpair the active orchestrator before changing this device to orchestrator role", trust.ErrConflict)
		}
	}
	return nil
}

// Accept handles one inbound pairing supplied by the shared QUIC endpoint.
func (service *Service) Accept(ctx context.Context, channel session.PairingChannel) {
	if err := service.acceptInbound(ctx, channel); err != nil {
		service.reportError(err)
		_ = channel.Close()
	}
}

// Begin connects an orchestrator to one explicitly selected nearby worker.
func (service *Service) Begin(ctx context.Context, selector string) (trust.Pairing, error) {
	if service.local.Role != device.RoleOrchestrator {
		return trust.Pairing{}, fmt.Errorf("%w: only an orchestrator can initiate pairing", trust.ErrPairingUnavailable)
	}
	target, err := service.nearby.ResolveNearby(ctx, selector)
	if err != nil {
		return trust.Pairing{}, err
	}
	if target.Announcement.Role != device.RoleWorker {
		return trust.Pairing{}, fmt.Errorf("%w: %s is not a worker", trust.ErrPairingUnavailable, target.Announcement.Name)
	}
	if !target.Announcement.EndpointReady {
		return trust.Pairing{}, fmt.Errorf("%w: %s does not expose a pairing endpoint", trust.ErrPairingUnavailable, target.Announcement.Name)
	}
	dialContext := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		dialContext, cancel = context.WithTimeout(ctx, pairingConnectTimeout)
		defer cancel()
	}
	channel, err := service.endpoint.DialPairing(dialContext, target)
	if err != nil {
		return trust.Pairing{}, fmt.Errorf("%w: connect to %s: %v", trust.ErrPairingUnavailable, target.Announcement.Name, err)
	}
	value, err := service.register(ctx, channel, trust.DirectionOutbound)
	if err != nil {
		_ = channel.Close()
		return trust.Pairing{}, err
	}
	go service.monitor(channel, value.ID, value.ExpiresAt)
	return value, nil
}

// ListPairings returns current ceremonies, newest first.
func (service *Service) ListPairings(ctx context.Context) ([]trust.Pairing, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked()
	values := make([]trust.Pairing, 0, len(service.pairings))
	for _, entry := range service.pairings {
		values = append(values, entry.value.Clone())
	}
	sort.Slice(values, func(left, right int) bool {
		if !values[left].StartedAt.Equal(values[right].StartedAt) {
			return values[left].StartedAt.After(values[right].StartedAt)
		}
		return values[left].ID < values[right].ID
	})
	return values, nil
}

// Confirm records this device's physical-user confirmation and notifies the peer.
func (service *Service) Confirm(ctx context.Context, selector string) (trust.Pairing, error) {
	entry, err := service.updateLocalDecision(selector, true)
	if err != nil {
		return trust.Pairing{}, err
	}
	if err := sendDecision(ctx, entry.channel, entry.value.ID, true); err != nil {
		service.fail(entry.value.ID, err)
		return trust.Pairing{}, fmt.Errorf("%w: notify %s: %v", trust.ErrPairingUnavailable, entry.value.PeerName, err)
	}
	service.maybeActivate(entry.value.ID)
	return service.getPairing(entry.value.ID)
}

// Reject terminates a pending ceremony and tells the remote peer.
func (service *Service) Reject(ctx context.Context, selector string) (trust.Pairing, error) {
	entry, err := service.updateLocalDecision(selector, false)
	if err != nil {
		return trust.Pairing{}, err
	}
	_ = sendDecision(ctx, entry.channel, entry.value.ID, false)
	_ = entry.channel.Close()
	return service.getPairing(entry.value.ID)
}

// ListTrusted returns durable pins, including revoked history.
func (service *Service) ListTrusted(ctx context.Context) ([]trust.Peer, error) {
	return service.repository.List(ctx)
}

// RefreshTrustedHints stores current LAN compatibility/resource hints for
// unambiguously matched active trusted workers, then returns the refreshed list.
func (service *Service) RefreshTrustedHints(
	ctx context.Context,
	snapshot device.DiscoverySnapshot,
) ([]trust.Peer, error) {
	peers, err := service.repository.List(ctx)
	if err != nil {
		return nil, err
	}
	for id, hints := range trust.MatchNearbyHints(peers, snapshot.Devices) {
		if _, err := service.repository.UpdateHints(ctx, id, hints); err != nil {
			return nil, err
		}
	}
	return service.repository.List(ctx)
}

// Unpair revokes exactly one active peer selected by ID prefix or exact name.
func (service *Service) Unpair(ctx context.Context, selector string) (trust.Peer, error) {
	peers, err := service.repository.List(ctx)
	if err != nil {
		return trust.Peer{}, err
	}
	selector = strings.TrimSpace(selector)
	matches := make([]trust.Peer, 0, 1)
	for _, peer := range peers {
		if peer.State != trust.StateActive {
			continue
		}
		id := string(peer.DeviceID)
		if peer.Name == selector || id == selector || strings.HasPrefix(id, selector) {
			matches = append(matches, peer)
		}
	}
	if len(matches) == 0 {
		return trust.Peer{}, fmt.Errorf("%w: %s", trust.ErrNotFound, selector)
	}
	if len(matches) > 1 {
		return trust.Peer{}, fmt.Errorf("%w: %s matches %d active peers", trust.ErrConflict, selector, len(matches))
	}
	return service.repository.Revoke(ctx, matches[0].DeviceID, service.now().UTC())
}

// Close stops the shared QUIC listener.
func (service *Service) Close() error {
	return service.endpoint.Close()
}

func (service *Service) acceptInbound(ctx context.Context, channel session.PairingChannel) error {
	if service.local.Role != device.RoleWorker {
		return fmt.Errorf("%w: only workers accept pairing", trust.ErrPairingUnavailable)
	}
	value, err := service.register(ctx, channel, trust.DirectionInbound)
	if err != nil {
		return err
	}
	service.monitor(channel, value.ID, value.ExpiresAt)
	return nil
}

func (service *Service) register(
	ctx context.Context,
	channel session.PairingChannel,
	direction trust.Direction,
) (trust.Pairing, error) {
	peer := channel.Peer()
	if err := peer.Validate(); err != nil {
		return trust.Pairing{}, err
	}
	if direction == trust.DirectionOutbound &&
		(service.local.Role != device.RoleOrchestrator || peer.Role != device.RoleWorker) {
		return trust.Pairing{}, trust.ErrInvalidPairing
	}
	if direction == trust.DirectionInbound &&
		(service.local.Role != device.RoleWorker || peer.Role != device.RoleOrchestrator) {
		return trust.Pairing{}, trust.ErrInvalidPairing
	}
	if existing, err := service.repository.Get(ctx, peer.ID); err == nil && existing.State == trust.StateActive {
		return trust.Pairing{}, fmt.Errorf("%w: %s is already paired", trust.ErrConflict, peer.ID)
	} else if err != nil && !errors.Is(err, trust.ErrNotFound) {
		return trust.Pairing{}, err
	}
	if service.local.Role == device.RoleWorker {
		peers, err := service.repository.List(ctx)
		if err != nil {
			return trust.Pairing{}, err
		}
		for _, existing := range peers {
			if existing.State == trust.StateActive && existing.Role == device.RoleOrchestrator {
				return trust.Pairing{}, fmt.Errorf("%w: worker already has a paired orchestrator", trust.ErrConflict)
			}
		}
	}
	initiatorID, responderID := service.local.Identity.ID(), peer.ID
	if direction == trust.DirectionInbound {
		initiatorID, responderID = peer.ID, service.local.Identity.ID()
	}
	id, verification, err := trust.DerivePairing(channel.Binding(), initiatorID, responderID)
	if err != nil {
		return trust.Pairing{}, err
	}
	connectivitySecret, err := trust.DeriveConnectivitySecret(channel.Binding(), initiatorID, responderID)
	if err != nil {
		return trust.Pairing{}, err
	}
	startedAt := service.now().UTC()
	value := trust.Pairing{
		ID: id, PeerID: peer.ID, PeerPublicKey: append([]byte(nil), peer.PublicKey...),
		PeerName: peer.Name, PeerRole: peer.Role, Verification: verification,
		Direction: direction, State: trust.PairingWaiting,
		StartedAt: startedAt, ExpiresAt: startedAt.Add(service.lifetime),
	}
	if err := value.Validate(); err != nil {
		return trust.Pairing{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked()
	waiting := 0
	for _, existing := range service.pairings {
		if existing.value.State == trust.PairingWaiting {
			waiting++
		}
	}
	if waiting >= maximumPendingPairings {
		return trust.Pairing{}, fmt.Errorf("%w: too many pending requests", trust.ErrPairingUnavailable)
	}
	if _, exists := service.pairings[id]; exists {
		return trust.Pairing{}, fmt.Errorf("%w: duplicate session", trust.ErrConflict)
	}
	service.pairings[id] = &pending{
		value: value, connectivitySecret: connectivitySecret, channel: channel,
	}
	return value.Clone(), nil
}

func (service *Service) monitor(channel session.PairingChannel, id trust.PairID, expiresAt time.Time) {
	ctx, cancel := context.WithDeadline(context.Background(), expiresAt)
	defer cancel()
	for {
		decision, err := channel.ReceiveDecision(ctx)
		if err != nil {
			service.mu.Lock()
			entry := service.pairings[id]
			if entry != nil && entry.value.State == trust.PairingWaiting {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					entry.value.State = trust.PairingExpired
				} else {
					entry.value.State = trust.PairingFailed
					entry.value.Failure = "pairing connection closed before both devices confirmed"
				}
			}
			service.mu.Unlock()
			_ = channel.Close()
			return
		}
		if decision.PairingID != id {
			service.fail(id, session.ErrProtocol)
			_ = channel.Close()
			return
		}
		if !decision.Confirmed {
			service.mu.Lock()
			if entry := service.pairings[id]; entry != nil && entry.value.State == trust.PairingWaiting {
				entry.value.State = trust.PairingRejected
			}
			service.mu.Unlock()
			_ = channel.Close()
			return
		}
		service.mu.Lock()
		if entry := service.pairings[id]; entry != nil {
			if entry.value.State == trust.PairingWaiting {
				entry.value.RemoteConfirmed = true
			}
			if decision.Committed {
				entry.remoteCommitted = true
			}
		}
		service.mu.Unlock()
		service.maybeActivate(id)
		service.mu.Lock()
		entry := service.pairings[id]
		done := entry == nil || entry.value.State == trust.PairingRejected ||
			entry.value.State == trust.PairingExpired || entry.value.State == trust.PairingFailed ||
			(entry.localCommitted && entry.remoteCommitted)
		service.mu.Unlock()
		if done {
			_ = channel.Close()
			return
		}
	}
}

func (service *Service) updateLocalDecision(selector string, confirmed bool) (*pending, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.pruneLocked()
	entry, err := service.resolvePairingLocked(selector)
	if err != nil {
		return nil, err
	}
	if entry.value.State != trust.PairingWaiting || entry.activating {
		return nil, fmt.Errorf("%w: pairing %s is %s", trust.ErrConflict, entry.value.ID, entry.value.State)
	}
	if confirmed {
		entry.value.LocalConfirmed = true
	} else {
		entry.value.State = trust.PairingRejected
	}
	return &pending{value: entry.value.Clone(), channel: entry.channel}, nil
}

func (service *Service) resolvePairingLocked(selector string) (*pending, error) {
	selector = strings.ToUpper(strings.TrimSpace(selector))
	matches := make([]*pending, 0, 1)
	for id, entry := range service.pairings {
		if strings.HasPrefix(strings.ToUpper(string(id)), selector) ||
			strings.EqualFold(string(entry.value.Verification), selector) {
			matches = append(matches, entry)
		}
	}
	if selector == "" || len(matches) == 0 {
		return nil, fmt.Errorf("%w: %s", trust.ErrPairingNotFound, selector)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: selector matches %d pairings", trust.ErrConflict, len(matches))
	}
	return matches[0], nil
}

func (service *Service) maybeActivate(id trust.PairID) {
	service.mu.Lock()
	entry := service.pairings[id]
	if entry == nil || entry.value.State != trust.PairingWaiting || entry.activating ||
		!entry.value.LocalConfirmed || !entry.value.RemoteConfirmed {
		service.mu.Unlock()
		return
	}
	entry.activating = true
	value := entry.value.Clone()
	connectivitySecret := append(trust.ConnectivitySecret(nil), entry.connectivitySecret...)
	channel := entry.channel
	service.mu.Unlock()

	pairedAt := service.now().UTC()
	peer := trust.Peer{
		PairID: value.ID, DeviceID: value.PeerID, PublicKey: value.PeerPublicKey,
		ConnectivitySecret: connectivitySecret,
		Name:               value.PeerName, Role: value.PeerRole, State: trust.StateActive,
		PairedAt: pairedAt, UpdatedAt: pairedAt,
	}
	err := service.repository.Activate(context.Background(), peer)
	service.mu.Lock()
	entry = service.pairings[id]
	if entry != nil && entry.value.State == trust.PairingWaiting {
		entry.activating = false
		if err != nil {
			entry.value.State = trust.PairingFailed
			entry.value.Failure = err.Error()
		} else {
			entry.value.State = trust.PairingPaired
			entry.localCommitted = true
		}
	}
	service.mu.Unlock()
	if err != nil {
		service.reportError(err)
		_ = channel.Close()
		return
	}
	commitContext, cancel := context.WithTimeout(context.Background(), decisionSendTimeout)
	defer cancel()
	if sendErr := channel.SendDecision(commitContext, session.PairingDecision{
		PairingID: id, Confirmed: true, Committed: true,
	}); sendErr != nil {
		service.reportError(fmt.Errorf("announce durable pairing: %w", sendErr))
	}
	service.mu.Lock()
	entry = service.pairings[id]
	done := entry != nil && entry.localCommitted && entry.remoteCommitted
	service.mu.Unlock()
	if done {
		_ = channel.Close()
	}
}

func (service *Service) fail(id trust.PairID, err error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if entry := service.pairings[id]; entry != nil && entry.value.State == trust.PairingWaiting {
		entry.value.State = trust.PairingFailed
		entry.value.Failure = err.Error()
	}
}

func (service *Service) getPairing(id trust.PairID) (trust.Pairing, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	entry := service.pairings[id]
	if entry == nil {
		return trust.Pairing{}, trust.ErrPairingNotFound
	}
	return entry.value.Clone(), nil
}

func (service *Service) pruneLocked() {
	now := service.now().UTC()
	for id, entry := range service.pairings {
		if entry.value.State == trust.PairingWaiting && !entry.value.ExpiresAt.After(now) {
			entry.value.State = trust.PairingExpired
			_ = entry.channel.Close()
		}
		if entry.value.State != trust.PairingWaiting && now.After(entry.value.ExpiresAt.Add(service.lifetime)) {
			delete(service.pairings, id)
		}
	}
}

func sendDecision(
	ctx context.Context,
	channel session.PairingChannel,
	id trust.PairID,
	confirmed bool,
) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, decisionSendTimeout)
		defer cancel()
	}
	return channel.SendDecision(ctx, session.PairingDecision{PairingID: id, Confirmed: confirmed})
}
