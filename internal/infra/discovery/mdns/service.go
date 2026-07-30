// Package mdns implements untrusted LAN presence with multicast DNS-SD.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/austinjiann/spare-compute/internal/device"
)

const (
	ServiceType = "_computehop._udp"
	Domain      = "local."
	DefaultPort = 47823

	textFormatVersion   = "1"
	defaultTTLSeconds   = 45
	minimumTTLSeconds   = 10
	maximumTTLSeconds   = 45
	defaultBrowseWindow = 20 * time.Second
)

type registration interface {
	Shutdown()
	TTL(uint32)
}

type rawEntry struct {
	Instance string
	HostName string
	Port     int
	Text     []string
	TTL      uint32
	IPv4     []net.IP
	IPv6     []net.IP
}

// Service advertises one local daemon and periodically refreshes browse state.
type Service struct {
	now          func() time.Time
	register     func(device.Announcement) (registration, error)
	browse       func(context.Context, chan<- rawEntry) error
	browseWindow time.Duration
}

// New constructs the production cross-platform mDNS service.
func New() *Service {
	service := &Service{now: time.Now, browseWindow: defaultBrowseWindow}
	service.register = service.registerAnnouncement
	service.browse = browse
	return service
}

// Run advertises local and reports resolved observations until ctx ends.
func (service *Service) Run(
	ctx context.Context,
	local device.Announcement,
	observe func(device.Observation),
	ready func(),
) error {
	if err := local.Validate(); err != nil {
		return err
	}
	if observe == nil || ready == nil || service.now == nil || service.register == nil ||
		service.browse == nil || service.browseWindow <= 0 {
		return errors.New("mDNS service dependencies are required")
	}
	server, err := service.register(local)
	if err != nil {
		return fmt.Errorf("advertise ComputeHop on LAN: %w", err)
	}
	defer server.Shutdown()
	ready()

	for ctx.Err() == nil {
		windowContext, stopWindow := context.WithTimeout(ctx, service.browseWindow)
		entries := make(chan rawEntry, 64)
		browseResult := make(chan error, 1)
		go func() {
			browseResult <- service.browse(windowContext, entries)
			close(entries)
		}()
		for entry := range entries {
			observation, err := parseEntry(entry, service.now().UTC())
			if err == nil {
				observe(observation)
			}
		}
		browseErr := <-browseResult
		stopWindow()
		if ctx.Err() != nil {
			return nil
		}
		if browseErr != nil {
			return fmt.Errorf("browse ComputeHop services: %w", browseErr)
		}
	}
	return nil
}

func (service *Service) registerAnnouncement(local device.Announcement) (registration, error) {
	instance := fmt.Sprintf("%s (%s)", local.Name, local.PresenceID.Short())
	server, err := zeroconf.Register(
		instance,
		ServiceType,
		Domain,
		int(local.Port),
		announcementText(local),
		nil,
	)
	if err != nil {
		return nil, err
	}
	server.TTL(defaultTTLSeconds)
	return server, nil
}

func announcementText(local device.Announcement) []string {
	records := []string{
		"txtvers=" + textFormatVersion,
		"sid=" + string(local.PresenceID),
		"name=" + local.Name,
		"role=" + string(local.Role),
		"proto=" + strconv.FormatUint(uint64(local.ProtocolVersion), 10),
		"ready=" + strconv.FormatBool(local.EndpointReady),
	}
	if local.Platform != "" {
		records = append(records, "platform="+local.Platform)
	}
	if local.Architecture != "" {
		records = append(records, "arch="+local.Architecture)
	}
	return records
}

func browse(ctx context.Context, output chan<- rawEntry) error {
	type family struct {
		traffic zeroconf.IPType
		name    string
	}
	families := []family{{zeroconf.IPv4, "IPv4"}, {zeroconf.IPv6, "IPv6"}}
	var (
		wait        sync.WaitGroup
		started     int
		startErrors []error
	)
	for _, candidate := range families {
		resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(candidate.traffic))
		if err != nil {
			startErrors = append(startErrors, fmt.Errorf("%s: %w", candidate.name, err))
			continue
		}
		entries := make(chan *zeroconf.ServiceEntry, 64)
		if err := resolver.Browse(ctx, ServiceType, Domain, entries); err != nil {
			startErrors = append(startErrors, fmt.Errorf("%s: %w", candidate.name, err))
			continue
		}
		started++
		wait.Add(1)
		go func() {
			defer wait.Done()
			for entry := range entries {
				if entry == nil {
					continue
				}
				converted := rawEntry{
					Instance: entry.Instance,
					HostName: entry.HostName,
					Port:     entry.Port,
					Text:     append([]string(nil), entry.Text...),
					TTL:      entry.TTL,
				}
				converted.IPv4 = cloneIPs(entry.AddrIPv4)
				converted.IPv6 = cloneIPs(entry.AddrIPv6)
				select {
				case output <- converted:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	if started == 0 {
		return errors.Join(startErrors...)
	}
	<-ctx.Done()
	wait.Wait()
	return nil
}

func parseEntry(entry rawEntry, now time.Time) (device.Observation, error) {
	fields, err := parseText(entry.Text)
	if err != nil {
		return device.Observation{}, err
	}
	if fields["txtvers"] != textFormatVersion {
		return device.Observation{}, device.ErrInvalidObservation
	}
	presenceID, err := device.ParsePresenceID(fields["sid"])
	if err != nil {
		return device.Observation{}, err
	}
	protocolVersion, err := strconv.ParseUint(fields["proto"], 10, 32)
	if err != nil {
		return device.Observation{}, device.ErrInvalidObservation
	}
	ready, err := strconv.ParseBool(fields["ready"])
	if err != nil {
		return device.Observation{}, device.ErrInvalidObservation
	}
	if entry.Port <= 0 || entry.Port > 65535 || entry.TTL == 0 {
		return device.Observation{}, device.ErrInvalidObservation
	}
	announcement := device.Announcement{
		PresenceID: presenceID, Name: fields["name"], Role: device.Role(fields["role"]),
		ProtocolVersion: uint32(protocolVersion), Port: uint16(entry.Port), EndpointReady: ready,
		Platform: fields["platform"], Architecture: fields["arch"],
	}
	addresses := normalizedAddresses(append(cloneIPs(entry.IPv4), entry.IPv6...))
	ttl := min(max(entry.TTL, minimumTTLSeconds), maximumTTLSeconds)
	observation := device.Observation{
		Key:          device.ObservationKey(entry.Instance + "|" + entry.HostName + "|" + strconv.Itoa(entry.Port)),
		Announcement: announcement,
		Instance:     entry.Instance,
		HostName:     entry.HostName,
		Addresses:    addresses,
		SeenAt:       now,
		ExpiresAt:    now.Add(time.Duration(ttl) * time.Second),
	}
	if err := observation.Validate(); err != nil {
		return device.Observation{}, err
	}
	return observation, nil
}

func parseText(records []string) (map[string]string, error) {
	if len(records) == 0 || len(records) > 16 {
		return nil, device.ErrInvalidObservation
	}
	fields := make(map[string]string, len(records))
	for _, record := range records {
		if len(record) == 0 || len(record) > 255 {
			return nil, device.ErrInvalidObservation
		}
		key, value, found := strings.Cut(record, "=")
		if !found || key == "" || value == "" || key != strings.ToLower(key) {
			return nil, device.ErrInvalidObservation
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, device.ErrInvalidObservation
		}
		fields[key] = value
	}
	for _, required := range []string{"txtvers", "sid", "name", "role", "proto", "ready"} {
		if fields[required] == "" {
			return nil, device.ErrInvalidObservation
		}
	}
	return fields, nil
}

func normalizedAddresses(values []net.IP) []netip.Addr {
	unique := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		address, ok := netip.AddrFromSlice(value)
		if !ok {
			continue
		}
		address = address.Unmap()
		if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
			continue
		}
		unique[address] = struct{}{}
	}
	addresses := make([]netip.Addr, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(left, right int) bool {
		return addresses[left].Less(addresses[right])
	})
	return addresses
}

func cloneIPs(values []net.IP) []net.IP {
	clones := make([]net.IP, len(values))
	for index, value := range values {
		clones[index] = append(net.IP(nil), value...)
	}
	return clones
}
