package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/austinjiann/spare-compute/internal/device"
)

const (
	defaultDiscoveryRetryDelay = 15 * time.Second
	addressMergeWindow         = 2 * time.Second
)

var ErrMissingDeviceDependency = errors.New("nearby device service dependency is required")

var (
	ErrNearbyDeviceNotFound  = errors.New("nearby device not found")
	ErrNearbyDeviceAmbiguous = errors.New("nearby device selector is ambiguous")
)

// DeviceDependencies configure local LAN presence and the in-memory nearby view.
type DeviceDependencies struct {
	Local       device.Announcement
	Discovery   device.LANDiscovery
	Now         func() time.Time
	ReportError func(error)
	RetryDelay  time.Duration
}

// DeviceService supervises untrusted LAN discovery and expiring observations.
type DeviceService struct {
	local       device.Announcement
	discovery   device.LANDiscovery
	now         func() time.Time
	reportError func(error)
	retryDelay  time.Duration

	mu            sync.Mutex
	devices       map[string]device.NearbyDevice
	available     bool
	lastError     string
	lastStartedAt *time.Time
}

// NewDeviceService validates and constructs the nearby-device service.
func NewDeviceService(dependencies DeviceDependencies) (*DeviceService, error) {
	if err := dependencies.Local.Validate(); err != nil {
		return nil, err
	}
	if dependencies.Discovery == nil || dependencies.Now == nil {
		return nil, ErrMissingDeviceDependency
	}
	if dependencies.ReportError == nil {
		dependencies.ReportError = func(error) {}
	}
	if dependencies.RetryDelay == 0 {
		dependencies.RetryDelay = defaultDiscoveryRetryDelay
	}
	if dependencies.RetryDelay < 0 {
		return nil, fmt.Errorf("%w: retry delay must not be negative", ErrMissingDeviceDependency)
	}
	return &DeviceService{
		local: dependencies.Local, discovery: dependencies.Discovery,
		now: dependencies.Now, reportError: dependencies.ReportError,
		retryDelay: dependencies.RetryDelay,
		devices:    make(map[string]device.NearbyDevice),
	}, nil
}

// Run keeps discovery active, reporting failures while retrying until ctx ends.
func (service *DeviceService) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		err := service.discovery.Run(ctx, service.local, service.observe, service.markReady)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("LAN discovery stopped unexpectedly")
		}
		if service.markUnavailable(err) {
			service.reportError(err)
		}
		timer := time.NewTimer(service.retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
	return nil
}

// ListNearby returns a sorted copy and removes expired observations.
func (service *DeviceService) ListNearby(ctx context.Context) (device.DiscoverySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return device.DiscoverySnapshot{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now().UTC()
	for key, nearby := range service.devices {
		if !nearby.ExpiresAt.After(now) {
			delete(service.devices, key)
		}
	}
	result := device.DiscoverySnapshot{
		Devices:       make([]device.NearbyDevice, 0, len(service.devices)),
		Available:     service.available,
		LastError:     service.lastError,
		LastStartedAt: cloneTime(service.lastStartedAt),
	}
	for _, nearby := range service.devices {
		nearby.Addresses = append([]netip.Addr(nil), nearby.Addresses...)
		result.Devices = append(result.Devices, nearby)
	}
	sort.Slice(result.Devices, func(left, right int) bool {
		if result.Devices[left].Announcement.Name != result.Devices[right].Announcement.Name {
			return result.Devices[left].Announcement.Name < result.Devices[right].Announcement.Name
		}
		return result.Devices[left].Key < result.Devices[right].Key
	})
	return result, nil
}

// ResolveNearby selects one current worker by exact name, full presence ID, or
// an unambiguous presence-ID prefix. Discovery remains untrusted after lookup.
func (service *DeviceService) ResolveNearby(ctx context.Context, selector string) (device.NearbyDevice, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return device.NearbyDevice{}, ErrNearbyDeviceNotFound
	}
	snapshot, err := service.ListNearby(ctx)
	if err != nil {
		return device.NearbyDevice{}, err
	}
	matches := make([]device.NearbyDevice, 0, 1)
	for _, nearby := range snapshot.Devices {
		presence := string(nearby.Announcement.PresenceID)
		if nearby.Announcement.Name == selector || presence == selector || strings.HasPrefix(presence, selector) {
			matches = append(matches, nearby)
		}
	}
	switch len(matches) {
	case 0:
		return device.NearbyDevice{}, fmt.Errorf("%w: %s", ErrNearbyDeviceNotFound, selector)
	case 1:
		return matches[0], nil
	default:
		return device.NearbyDevice{}, fmt.Errorf("%w: %s matches %d devices", ErrNearbyDeviceAmbiguous, selector, len(matches))
	}
}

func (service *DeviceService) observe(observation device.Observation) {
	if observation.Validate() != nil || observation.Announcement.PresenceID == service.local.PresenceID {
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	key := string(observation.Key) + "|" + string(observation.Announcement.PresenceID)
	firstSeen := observation.SeenAt.UTC()
	var existingAddresses []netip.Addr
	if current, exists := service.devices[key]; exists {
		if observation.SeenAt.Before(current.SeenAt) {
			return
		}
		firstSeen = current.FirstSeenAt
		elapsed := observation.SeenAt.Sub(current.SeenAt)
		if elapsed >= 0 && elapsed <= addressMergeWindow {
			existingAddresses = current.Addresses
		}
	}
	observation.Addresses = mergeAddresses(existingAddresses, observation.Addresses)
	observation.SeenAt = observation.SeenAt.UTC()
	observation.ExpiresAt = observation.ExpiresAt.UTC()
	service.devices[key] = device.NearbyDevice{Observation: observation, FirstSeenAt: firstSeen}
}

func (service *DeviceService) markReady() {
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now().UTC()
	service.available = true
	service.lastError = ""
	service.lastStartedAt = &now
}

func (service *DeviceService) markUnavailable(err error) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	changed := service.available || service.lastError != err.Error()
	service.available = false
	service.lastError = err.Error()
	return changed
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func mergeAddresses(left, right []netip.Addr) []netip.Addr {
	unique := make(map[netip.Addr]struct{}, len(left)+len(right))
	for _, address := range append(append([]netip.Addr(nil), left...), right...) {
		unique[address] = struct{}{}
	}
	result := make([]netip.Addr, 0, len(unique))
	for address := range unique {
		result = append(result, address)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Less(result[right]) })
	return result
}
