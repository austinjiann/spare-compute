// Package remoteconn supervises pair-scoped rendezvous and selected ICE paths.
package remoteconn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	connectivityv1 "github.com/austinjiann/spare-compute/gen/go/computehop/connectivity/v1"
	computehopv1 "github.com/austinjiann/spare-compute/gen/go/computehop/v1"
	"github.com/austinjiann/spare-compute/internal/connectivity"
	"github.com/austinjiann/spare-compute/internal/connectivity/icepath"
	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
	"github.com/austinjiann/spare-compute/internal/device"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/trust"
)

const (
	defaultTrustRefreshInterval = 3 * time.Second
	defaultPublishInterval      = 3 * time.Second
	defaultRetryDelay           = 5 * time.Second
	defaultGatherTimeout        = 12 * time.Second
	defaultExchangeTimeout      = 30 * time.Second
	defaultConnectTimeout       = 20 * time.Second
	defaultPresenceLifetime     = time.Minute
	minimumCredentialWindow     = 10 * time.Second
)

var (
	ErrInvalidConfiguration = errors.New("invalid remote connectivity configuration")
	ErrPathUnavailable      = errors.New("remote connectivity path is unavailable")
	ErrCredentialWindow     = errors.New("rendezvous credential window is closing")
)

// Path carries identity-pinned QUIC over one selected packet connection.
type Path interface {
	Dial(context.Context, trust.Peer) (remoteprotocol.Caller, error)
	Serve(context.Context, remoteprotocol.Handler) error
	Close() error
}

// PathFactory transfers ownership of a selected packet connection to a secure
// application transport.
type PathFactory func(net.PacketConn, net.Addr) (Path, error)

// Status is the user-visible lifecycle state for one paired peer.
type Status string

const (
	StatusConnecting  Status = "connecting"
	StatusConnected   Status = "connected"
	StatusUnavailable Status = "unavailable"
)

// State is a secret-free snapshot suitable for diagnostics and local UI.
type State struct {
	DeviceID  device.ID
	Name      string
	Status    Status
	PathKind  string
	LastError string
	UpdatedAt time.Time
}

// Config defines a bounded background connection manager.
type Config struct {
	LocalRole device.Role
	Trust     trust.Repository
	Client    *rendezvous.Client
	ICE       icepath.Config
	NewPath   PathFactory
	Now       func() time.Time

	TrustRefreshInterval time.Duration
	PublishInterval      time.Duration
	RetryDelay           time.Duration
	GatherTimeout        time.Duration
	ExchangeTimeout      time.Duration
	ConnectTimeout       time.Duration
	ReportError          func(device.ID, error)
}

// Manager discovers active pair records, negotiates one path per compatible
// peer, and makes orchestrator paths available to the job router.
type Manager struct {
	localRole device.Role
	trust     trust.Repository
	client    *rendezvous.Client
	ice       icepath.Config
	newPath   PathFactory
	now       func() time.Time

	trustRefreshInterval time.Duration
	publishInterval      time.Duration
	retryDelay           time.Duration
	gatherTimeout        time.Duration
	exchangeTimeout      time.Duration
	connectTimeout       time.Duration
	reportError          func(device.ID, error)

	mu      sync.Mutex
	paths   map[device.ID]*managedPath
	states  map[device.ID]State
	changed chan struct{}
}

type managedPath struct {
	path     Path
	failure  chan struct{}
	failOnce sync.Once
}

func (path *managedPath) fail() {
	path.failOnce.Do(func() { close(path.failure) })
}

// NewManager validates and constructs a remote connectivity manager.
func NewManager(config Config) (*Manager, error) {
	if (config.LocalRole != device.RoleOrchestrator && config.LocalRole != device.RoleWorker) ||
		config.Trust == nil || config.Client == nil || config.NewPath == nil {
		return nil, ErrInvalidConfiguration
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReportError == nil {
		config.ReportError = func(device.ID, error) {}
	}
	probe, err := icepath.NewSession(config.ICE)
	if err != nil {
		return nil, fmt.Errorf("%w: ICE settings: %v", ErrInvalidConfiguration, err)
	}
	_ = probe.Close()
	applyDurationDefault(&config.TrustRefreshInterval, defaultTrustRefreshInterval)
	applyDurationDefault(&config.PublishInterval, defaultPublishInterval)
	applyDurationDefault(&config.RetryDelay, defaultRetryDelay)
	applyDurationDefault(&config.GatherTimeout, defaultGatherTimeout)
	applyDurationDefault(&config.ExchangeTimeout, defaultExchangeTimeout)
	applyDurationDefault(&config.ConnectTimeout, defaultConnectTimeout)
	if config.TrustRefreshInterval < 100*time.Millisecond || config.TrustRefreshInterval > time.Minute ||
		config.PublishInterval < 100*time.Millisecond || config.PublishInterval > time.Minute ||
		config.RetryDelay < 100*time.Millisecond || config.RetryDelay > 5*time.Minute ||
		config.GatherTimeout < time.Second || config.GatherTimeout > 2*time.Minute ||
		config.ExchangeTimeout < time.Second || config.ExchangeTimeout > 5*time.Minute ||
		config.ConnectTimeout < time.Second || config.ConnectTimeout > 2*time.Minute {
		return nil, ErrInvalidConfiguration
	}
	return &Manager{
		localRole: config.LocalRole, trust: config.Trust, client: config.Client,
		ice: config.ICE, newPath: config.NewPath, now: config.Now,
		trustRefreshInterval: config.TrustRefreshInterval,
		publishInterval:      config.PublishInterval, retryDelay: config.RetryDelay,
		gatherTimeout: config.GatherTimeout, exchangeTimeout: config.ExchangeTimeout,
		connectTimeout: config.ConnectTimeout, reportError: config.ReportError,
		paths: make(map[device.ID]*managedPath), states: make(map[device.ID]State),
		changed: make(chan struct{}),
	}, nil
}

// Run continuously reconciles active compatible pair records. Worker paths
// serve authenticated control sessions; orchestrator paths are held for Dial.
func (manager *Manager) Run(ctx context.Context, handler remoteprotocol.Handler) error {
	if manager == nil || (manager.localRole == device.RoleWorker && handler == nil) {
		return ErrInvalidConfiguration
	}
	type runningPeer struct {
		peer   trust.Peer
		cancel context.CancelFunc
	}
	running := make(map[device.ID]runningPeer)
	var wait sync.WaitGroup
	reconcile := func() {
		peers, err := manager.trust.List(ctx)
		if err != nil {
			manager.reportError("", err)
			return
		}
		wanted := make(map[device.ID]trust.Peer)
		for _, peer := range peers {
			if manager.compatible(peer) {
				wanted[peer.DeviceID] = peer.Clone()
			}
		}
		for id, current := range running {
			peer, exists := wanted[id]
			if exists && samePair(current.peer, peer) {
				delete(wanted, id)
				continue
			}
			current.cancel()
			delete(running, id)
			manager.removePeer(id)
		}
		for id, peer := range wanted {
			peerContext, cancel := context.WithCancel(ctx)
			running[id] = runningPeer{peer: peer, cancel: cancel}
			wait.Add(1)
			go func() {
				defer wait.Done()
				manager.runPeer(peerContext, peer, handler)
			}()
		}
	}

	reconcile()
	ticker := time.NewTicker(manager.trustRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, current := range running {
				current.cancel()
			}
			wait.Wait()
			return nil
		case <-ticker.C:
			reconcile()
		}
	}
}

// DialRemotePeer waits for a ready path and opens an identity-pinned control
// connection. Failed handshakes invalidate the path so its supervisor retries.
func (manager *Manager) DialRemotePeer(
	ctx context.Context,
	peer trust.Peer,
) (remoteprotocol.Caller, error) {
	if manager == nil || manager.localRole != device.RoleOrchestrator || !manager.compatible(peer) {
		return nil, ErrPathUnavailable
	}
	for {
		path, changed := manager.currentPath(peer.DeviceID)
		if path == nil {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: %s: %v", ErrPathUnavailable, peer.Name, ctx.Err())
			case <-changed:
				continue
			}
		}
		caller, err := path.path.Dial(ctx, peer)
		if err == nil {
			return &trackedCaller{Caller: caller, path: path}, nil
		}
		path.fail()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %s: %v", ErrPathUnavailable, peer.Name, ctx.Err())
		case <-changed:
		}
	}
}

// States returns sorted secret-free lifecycle diagnostics.
func (manager *Manager) States() []State {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	states := make([]State, 0, len(manager.states))
	for _, state := range manager.states {
		states = append(states, state)
	}
	sort.Slice(states, func(left, right int) bool {
		if states[left].Name != states[right].Name {
			return states[left].Name < states[right].Name
		}
		return states[left].DeviceID < states[right].DeviceID
	})
	return states
}

func (manager *Manager) runPeer(ctx context.Context, peer trust.Peer, handler remoteprotocol.Handler) {
	var generation uint64
	for ctx.Err() == nil {
		manager.setState(peer, StatusConnecting, "", "")
		connectedGeneration, err := manager.connectOnce(ctx, peer, handler, generation)
		if connectedGeneration > generation {
			generation = connectedGeneration
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			manager.setState(peer, StatusUnavailable, "", err.Error())
			manager.reportError(peer.DeviceID, err)
		}
		if !waitFor(ctx, manager.retryDelay) {
			return
		}
	}
}

func (manager *Manager) connectOnce(
	ctx context.Context,
	peer trust.Peer,
	handler remoteprotocol.Handler,
	lastGeneration uint64,
) (uint64, error) {
	session, err := icepath.NewSession(manager.ice)
	if err != nil {
		return lastGeneration, err
	}
	defer session.Close()
	gatherContext, stopGather := context.WithTimeout(ctx, manager.gatherTimeout)
	description, err := session.Gather(gatherContext)
	stopGather()
	if err != nil {
		return lastGeneration, fmt.Errorf("gather remote candidates: %w", err)
	}
	now := manager.now().UTC()
	access, err := connectivity.DeriveAccess(peer.ConnectivitySecret, now)
	if err != nil {
		return lastGeneration, err
	}
	remaining := access.ExpiresAt.Sub(now)
	if remaining < minimumCredentialWindow {
		return lastGeneration, ErrCredentialWindow
	}
	generation := uint64(now.UnixNano())
	if generation <= lastGeneration {
		generation = lastGeneration + 1
	}
	lifetime := min(defaultPresenceLifetime, remaining)
	presence, err := icepath.NewPresence(description, generation, now, lifetime)
	if err != nil {
		return generation, err
	}
	ciphertext, err := icepath.SealPresence(
		peer.ConnectivitySecret, access.RouteID, manager.localRole, presence,
	)
	if err != nil {
		return generation, err
	}
	exchangeContext, stopExchange := context.WithTimeout(ctx, manager.exchangeTimeout)
	remotePresence, err := manager.exchangePresence(
		exchangeContext, peer, access, presence, ciphertext,
	)
	stopExchange()
	if err != nil {
		return generation, err
	}
	connectContext, stopConnect := context.WithTimeout(ctx, manager.connectTimeout)
	connection, err := session.Connect(connectContext, manager.localRole, remotePresence.Description)
	stopConnect()
	if err != nil {
		return generation, fmt.Errorf("select remote path: %w", err)
	}
	selected, err := connection.SelectedPath()
	if err != nil {
		_ = connection.Close()
		return generation, fmt.Errorf("inspect selected path: %w", err)
	}
	path, err := manager.newPath(connection, connection.RemoteAddr())
	if err != nil {
		_ = connection.Close()
		return generation, err
	}
	managed := &managedPath{path: path, failure: make(chan struct{})}
	defer path.Close()

	if manager.localRole == device.RoleWorker {
		manager.setState(peer, StatusConnected, selected.Kind, "")
		return generation, path.Serve(ctx, handler)
	}
	manager.installPath(peer, managed, selected.Kind)
	defer manager.removePath(peer.DeviceID, managed)
	select {
	case <-ctx.Done():
		return generation, nil
	case <-managed.failure:
		return generation, ErrPathUnavailable
	}
}

func (manager *Manager) exchangePresence(
	ctx context.Context,
	peer trust.Peer,
	access connectivity.Access,
	presence icepath.Presence,
	ciphertext []byte,
) (icepath.Presence, error) {
	localRole := endpointRole(manager.localRole)
	remoteRole := oppositeRole(manager.localRole)
	for {
		response, err := manager.client.PublishPresence(
			ctx, access, localRole, presence.Generation, ciphertext,
		)
		if err != nil {
			return icepath.Presence{}, fmt.Errorf("publish remote presence: %w", err)
		}
		if remote := response.GetPeer(); remote != nil {
			opened, openErr := icepath.OpenPresence(
				peer.ConnectivitySecret,
				access.RouteID,
				remoteRole,
				remote.GetGeneration(),
				remote.GetEncryptedPayload(),
				manager.now().UTC(),
			)
			if openErr == nil {
				return opened, nil
			}
		}
		if !waitFor(ctx, manager.publishInterval) {
			return icepath.Presence{}, fmt.Errorf("exchange remote presence: %w", ctx.Err())
		}
	}
}

func (manager *Manager) compatible(peer trust.Peer) bool {
	if peer.Validate() != nil || peer.State != trust.StateActive || !peer.ConnectivitySecret.Valid() {
		return false
	}
	return (manager.localRole == device.RoleOrchestrator && peer.Role == device.RoleWorker) ||
		(manager.localRole == device.RoleWorker && peer.Role == device.RoleOrchestrator)
}

func (manager *Manager) installPath(peer trust.Peer, path *managedPath, kind string) {
	manager.mu.Lock()
	if existing := manager.paths[peer.DeviceID]; existing != nil && existing != path {
		existing.fail()
	}
	manager.paths[peer.DeviceID] = path
	manager.states[peer.DeviceID] = State{
		DeviceID: peer.DeviceID, Name: peer.Name, Status: StatusConnected,
		PathKind: kind, UpdatedAt: manager.now().UTC(),
	}
	manager.notifyLocked()
	manager.mu.Unlock()
}

func (manager *Manager) removePath(id device.ID, expected *managedPath) {
	manager.mu.Lock()
	if manager.paths[id] == expected {
		delete(manager.paths, id)
		manager.notifyLocked()
	}
	manager.mu.Unlock()
}

func (manager *Manager) currentPath(id device.ID) (*managedPath, <-chan struct{}) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.paths[id], manager.changed
}

func (manager *Manager) setState(peer trust.Peer, status Status, kind, lastError string) {
	manager.mu.Lock()
	manager.states[peer.DeviceID] = State{
		DeviceID: peer.DeviceID, Name: peer.Name, Status: status,
		PathKind: kind, LastError: lastError, UpdatedAt: manager.now().UTC(),
	}
	manager.notifyLocked()
	manager.mu.Unlock()
}

func (manager *Manager) removePeer(id device.ID) {
	manager.mu.Lock()
	if path := manager.paths[id]; path != nil {
		path.fail()
		delete(manager.paths, id)
	}
	delete(manager.states, id)
	manager.notifyLocked()
	manager.mu.Unlock()
}

func (manager *Manager) notifyLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}

type trackedCaller struct {
	remoteprotocol.Caller
	path *managedPath
}

func (caller *trackedCaller) Call(
	ctx context.Context,
	request *computehopv1.RemoteRequest,
) (*computehopv1.RemoteResponse, error) {
	response, err := caller.Caller.Call(ctx, request)
	var remoteError *remoteprotocol.Error
	if err != nil && !errors.As(err, &remoteError) {
		caller.path.fail()
	}
	return response, err
}

func endpointRole(role device.Role) connectivityv1.EndpointRole {
	if role == device.RoleOrchestrator {
		return connectivityv1.EndpointRole_ENDPOINT_ROLE_ORCHESTRATOR
	}
	return connectivityv1.EndpointRole_ENDPOINT_ROLE_WORKER
}

func oppositeRole(role device.Role) device.Role {
	if role == device.RoleOrchestrator {
		return device.RoleWorker
	}
	return device.RoleOrchestrator
}

func samePair(left, right trust.Peer) bool {
	return left.PairID == right.PairID && left.Name == right.Name && left.State == right.State && left.Role == right.Role &&
		bytes.Equal(left.PublicKey, right.PublicKey) &&
		bytes.Equal(left.ConnectivitySecret, right.ConnectivitySecret)
}

func waitFor(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func applyDurationDefault(value *time.Duration, fallback time.Duration) {
	if *value == 0 {
		*value = fallback
	}
}
