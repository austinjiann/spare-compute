package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/contentcache"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	"github.com/austinjiann/spare-compute/internal/trust"
)

var (
	ErrDaemonNotRunning      = errors.New("ComputeHop daemon is not running")
	ErrInvalidDaemonResponse = errors.New("invalid response from computehopd")
)

type nearbyDeviceView struct {
	presenceID   device.PresenceID
	name         string
	role         string
	address      string
	availability string
	lastSeen     time.Time
}

func newRootCommand(dependencies dependencies) *cobra.Command {
	var stateDir string
	root := &cobra.Command{
		Use:           "computehop",
		Short:         "Run background jobs across your computers",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(dependencies.stdout)
	root.SetErr(dependencies.stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&stateDir, "state-dir", "", "directory containing local daemon state")

	clientForCommand := func() (caller, error) {
		client, err := dependencies.newClient(stateDir)
		if err != nil {
			return nil, err
		}
		return client, nil
	}

	root.AddCommand(newVersionCommand(dependencies.stdout))
	root.AddCommand(newSetupCommand(dependencies.stdout))
	root.AddCommand(newStatusCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newDoctorCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newDevicesCommand(dependencies.stdout, dependencies.stderr, clientForCommand))
	root.AddCommand(newConnectCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newPairCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newDisconnectCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newRunCommand(dependencies.stdout, dependencies.getwd, clientForCommand))
	root.AddCommand(newSmokeCommand(dependencies.stdout, dependencies.stderr, clientForCommand))
	root.AddCommand(newJobsCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newArtifactsCommand(
		dependencies.stdout, dependencies.stderr, dependencies.getwd, clientForCommand,
	))
	root.AddCommand(newCancelCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newLogsCommand(dependencies.stdout, dependencies.stderr, clientForCommand))
	return root
}

func newDevicesCommand(
	stdout io.Writer,
	stderr io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   "devices",
		Short: "List connected and nearby devices",
		Long: strings.TrimSpace(`List devices ComputeHop knows about.

The table includes trusted devices saved on this computer and nearby unpaired
devices discovered on the LAN. The CONNECTION column shows whether the device is
connected, not connected, or revoked. AVAILABILITY and PATH explain whether a
connected device is currently reachable over LAN, direct internet/STUN, TURN
relay, or LAN only.

Use this before connecting, disconnecting, or choosing a run target.`),
		Example: strings.TrimSpace(`computehop devices
computehop connect nearby
computehop disconnect "Gaming PC"`),
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
			})
			if err != nil {
				return err
			}
			result := response.GetListDevices()
			if result == nil {
				return fmt.Errorf("%w: missing device list result", ErrInvalidDaemonResponse)
			}
			switch result.GetDiscoveryState() {
			case localv1.DiscoveryState_DISCOVERY_STATE_STARTING:
				if len(result.GetDevices()) == 0 {
					if _, err = fmt.Fprintln(stdout, "LAN discovery is starting."); err != nil {
						return err
					}
					if len(result.GetTrustedDevices()) == 0 {
						return nil
					}
				}
			case localv1.DiscoveryState_DISCOVERY_STATE_UNAVAILABLE:
				message := result.GetDiscoveryError()
				if message == "" {
					message = "multicast DNS is unavailable"
				}
				if _, err := fmt.Fprintf(stderr, "LAN discovery unavailable: %s\n", message); err != nil {
					return err
				}
			case localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE:
			default:
				return fmt.Errorf("%w: invalid discovery state", ErrInvalidDaemonResponse)
			}
			if len(result.GetDevices()) == 0 && len(result.GetTrustedDevices()) == 0 {
				_, err = fmt.Fprintln(stdout, "No connected or nearby devices.")
				return err
			}

			trustedPeers := make([]trust.Peer, 0, len(result.GetTrustedDevices()))
			trustedMessages := make(map[device.ID]*localv1.TrustedDevice, len(result.GetTrustedDevices()))
			for _, message := range result.GetTrustedDevices() {
				peer, err := mapper.TrustedPeerFromProto(message)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				trustedPeers = append(trustedPeers, peer)
				trustedMessages[peer.DeviceID] = message
			}
			nearbyDevices := make([]nearbyDeviceView, 0, len(result.GetDevices()))
			for _, message := range result.GetDevices() {
				nearby, err := nearbyViewFromProto(message)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				nearbyDevices = append(nearbyDevices, nearby)
			}

			activePeerCounts := make(map[string]int)
			nearbyByKey := make(map[string][]int)
			for _, peer := range trustedPeers {
				if peer.State == trust.StateActive {
					activePeerCounts[deviceDisplayKey(peer.Name, string(peer.Role))]++
				}
			}
			for index, nearby := range nearbyDevices {
				key := deviceDisplayKey(nearby.name, nearby.role)
				nearbyByKey[key] = append(nearbyByKey[key], index)
			}

			writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tIDENTIFIER\tCONNECTION\tROLE\tAVAILABILITY\tPATH\tADDRESS\tUPDATED"); err != nil {
				return err
			}
			matchedNearby := make(map[int]bool)
			for _, peer := range trustedPeers {
				availability := "offline"
				path := "—"
				address := "—"
				updatedAt := peer.UpdatedAt
				key := deviceDisplayKey(peer.Name, string(peer.Role))
				matches := nearbyByKey[key]
				if peer.State == trust.StateActive && activePeerCounts[key] == 1 && len(matches) > 0 {
					path = "LAN"
					if len(matches) == 1 {
						matchIndex := matches[0]
						matchedNearby[matchIndex] = true
						availability = nearbyDevices[matchIndex].availability
						address = nearbyDevices[matchIndex].address
						updatedAt = nearbyDevices[matchIndex].lastSeen
					} else {
						availability = "nearby"
						address = fmt.Sprintf("%d LAN records", len(matches))
						for _, matchIndex := range matches {
							matchedNearby[matchIndex] = true
							if nearbyDevices[matchIndex].lastSeen.After(updatedAt) {
								updatedAt = nearbyDevices[matchIndex].lastSeen
							}
						}
					}
				} else if message := trustedMessages[peer.DeviceID]; message != nil {
					switch message.GetConnectivityState() {
					case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED:
						availability = "remote"
						path = remotePathLabel(message.GetConnectivityPath())
					case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTING:
						availability = "connecting"
						path = "internet"
					case localv1.ConnectivityState_CONNECTIVITY_STATE_DISABLED:
						path = "LAN only"
					}
					if message.GetConnectivityUpdatedAtUnixNano() > 0 {
						updatedAt = time.Unix(0, message.GetConnectivityUpdatedAtUnixNano()).UTC()
					}
				}
				if _, err := fmt.Fprintf(
					writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					peer.Name, peer.DeviceID.Short(), trustStateLabel(peer.State), peer.Role, availability, path, address,
					updatedAt.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			for index, nearby := range nearbyDevices {
				if matchedNearby[index] {
					continue
				}
				if _, err := fmt.Fprintf(
					writer,
					"%s\t%s\tnot connected\t%s\t%s\tLAN\t%s\t%s\n",
					nearby.name, nearby.presenceID.Short(), nearby.role, nearby.availability,
					nearby.address, nearby.lastSeen.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
}

func remotePathLabel(kind string) string {
	switch kind {
	case "host":
		return "direct"
	case "server-reflexive":
		return "direct (STUN)"
	case "relay":
		return "relay (TURN)"
	default:
		return "internet"
	}
}

func trustStateLabel(state trust.State) string {
	switch state {
	case trust.StateActive:
		return "connected"
	case trust.StateRevoked:
		return "revoked"
	default:
		return string(state)
	}
}

func nearbyViewFromProto(nearby *localv1.NearbyDevice) (nearbyDeviceView, error) {
	presenceID, err := device.ParsePresenceID(nearby.GetPresenceId())
	if err != nil {
		return nearbyDeviceView{}, err
	}
	if nearby.GetTrustState() != localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED {
		return nearbyDeviceView{}, errors.New("invalid nearby trust state")
	}
	role := ""
	switch nearby.GetRole() {
	case localv1.DeviceRole_DEVICE_ROLE_WORKER:
		role = string(device.RoleWorker)
	case localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR:
		role = string(device.RoleOrchestrator)
	default:
		return nearbyDeviceView{}, errors.New("invalid nearby device role")
	}
	address := nearby.GetHostName()
	if len(nearby.GetAddresses()) > 0 {
		address = nearby.GetAddresses()[0]
	}
	availability := "nearby"
	if address != "" && nearby.GetEndpointReady() && nearby.GetPort() > 0 {
		address = net.JoinHostPort(address, strconv.FormatUint(uint64(nearby.GetPort()), 10))
	} else if address != "" {
		address += " (discovery only)"
	}
	lastSeen := time.Unix(0, nearby.GetLastSeenAtUnixNano()).UTC()
	if nearby.GetName() == "" || address == "" || nearby.GetLastSeenAtUnixNano() <= 0 {
		return nearbyDeviceView{}, errors.New("incomplete nearby device")
	}
	return nearbyDeviceView{
		presenceID: presenceID, name: nearby.GetName(), role: role, address: address,
		availability: availability, lastSeen: lastSeen,
	}, nil
}

func deviceDisplayKey(name, role string) string {
	return name + "\x00" + role
}

func newSetupCommand(stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "setup",
		Short: "Print first-run setup commands",
		Long: strings.TrimSpace(`Print first-run commands without requiring the ComputeHop daemon.

Use this before the app is installed, when the daemon is stopped, or when you
want the exact commands for another Mac or the one-VPS staging stack.`),
		Example: strings.TrimSpace(`computehop setup
computehop setup orchestrator
computehop setup worker --device-name "Gaming PC"
computehop setup worker --device-name "Gaming PC" --lan-only
computehop setup vps --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10
computehop setup worker --device-name "Gaming PC" --connectivity-domain connect.example.com --turn-domain turn.example.com --turn-server "turn:turn.example.com:3478?transport=udp" --turn-username "1800000000:computehop" --turn-password "secret"`),
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return printSetupGuide(stdout)
		},
	}
	command.AddCommand(newSetupMacCommand(stdout))
	command.AddCommand(newSetupMacRoleCommand(stdout, device.RoleOrchestrator))
	command.AddCommand(newSetupMacRoleCommand(stdout, device.RoleWorker))
	command.AddCommand(newSetupVPSCommand(stdout))
	return command
}

func newSetupMacCommand(stdout io.Writer) *cobra.Command {
	options := defaultMacSetupOptions(string(device.RoleOrchestrator), "computehop", "setup", "mac")
	options.includeRoleInCustomize = true
	command := &cobra.Command{
		Use:   "mac",
		Short: "Print the macOS app and daemon install checklist",
		Long: strings.TrimSpace(`Print the exact macOS installer command for an orchestrator or worker.

This is the flag-based form of setup orchestrator and setup worker. It is useful
for scripts or generated setup instructions.`),
		Example: strings.TrimSpace(`computehop setup mac
computehop setup mac --role worker --device-name "Gaming PC" --cache-size 40GiB
computehop setup mac --role worker --device-name "Gaming PC" --lan-only
computehop setup mac --role orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com
computehop setup mac --role worker --device-name "Gaming PC" --connectivity-domain connect.example.com --turn-domain turn.example.com --turn-server "turn:turn.example.com:3478?transport=udp" --turn-username "1800000000:computehop" --turn-password "secret"`),
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := options.validate(); err != nil {
				return err
			}
			return printMacSetupGuide(stdout, options)
		},
	}
	command.Flags().StringVar(&options.role, "role", options.role, "mac role: orchestrator or worker")
	addMacSetupFlags(command, &options)
	return command
}

func newSetupMacRoleCommand(stdout io.Writer, role device.Role) *cobra.Command {
	options := defaultMacSetupOptions(string(role), "computehop", "setup", string(role))
	article := "a"
	if role == device.RoleOrchestrator {
		article = "an"
	}
	command := &cobra.Command{
		Use:     string(role),
		Short:   "Print the macOS " + string(role) + " install checklist",
		Long:    "Print the exact macOS installer command for " + article + " " + string(role) + " Mac without requiring the daemon.",
		Example: setupRoleExample(role),
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := options.validate(); err != nil {
				return err
			}
			return printMacSetupGuide(stdout, options)
		},
	}
	addMacSetupFlags(command, &options)
	return command
}

func setupRoleExample(role device.Role) string {
	switch role {
	case device.RoleWorker:
		return strings.TrimSpace(`computehop setup worker --device-name "Gaming PC"
computehop setup worker --device-name "Gaming PC" --cache-size 40GiB
computehop setup worker --device-name "Gaming PC" --lan-only
computehop setup worker --device-name "Gaming PC" --connectivity-domain connect.example.com --turn-domain turn.example.com
computehop setup worker --device-name "Gaming PC" --connectivity-domain connect.example.com --turn-domain turn.example.com --turn-server "turn:turn.example.com:3478?transport=udp" --turn-username "1800000000:computehop" --turn-password "secret"`)
	default:
		return strings.TrimSpace(`computehop setup orchestrator
computehop setup orchestrator --cache-size 40GiB
computehop setup orchestrator --lan-only
computehop setup orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com`)
	}
}

func newSetupVPSCommand(stdout io.Writer) *cobra.Command {
	options := defaultVPSSetupOptions()
	command := &cobra.Command{
		Use:   "vps",
		Short: "Print the one-VPS deployment checklist",
		Long: strings.TrimSpace(`Print a provider-neutral one-VPS checklist.

The VPS runs the public rendezvous service, HTTPS edge, and authenticated TURN
relay for devices that are no longer on the same LAN. The command only prints
commands and safe planning guidance; it does not buy or mutate a server.`),
		Example: strings.TrimSpace(`computehop setup vps
computehop setup vps --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10`),
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return printVPSSetupGuide(stdout, options)
		},
	}
	command.Flags().StringVar(&options.connectivityDomain, "connectivity-domain", options.connectivityDomain, "public HTTPS domain for ComputeHop rendezvous")
	command.Flags().StringVar(&options.turnDomain, "turn-domain", options.turnDomain, "public STUN/TURN domain")
	command.Flags().StringVar(&options.email, "email", options.email, "operations email for HTTPS certificate registration")
	command.Flags().StringVar(&options.publicIP, "public-ip", options.publicIP, "VPS public IPv4 address")
	return command
}

func addMacSetupFlags(command *cobra.Command, options *macSetupOptions) {
	command.Flags().StringVar(&options.deviceName, "device-name", "", "human-readable device name")
	command.Flags().StringVar(&options.cacheSize, "cache-size", "", "verified content cache limit, for example 40GiB")
	command.Flags().StringVar(&options.connectivityDomain, "connectivity-domain", "", "public HTTPS domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnDomain, "turn-domain", "", "public STUN/TURN domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnServer, "turn-server", "", "TURN relay URI printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnUsername, "turn-username", "", "short-lived TURN username printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnPassword, "turn-password", "", "short-lived TURN password printed by deploy/vps/turn-credentials.sh")
	command.Flags().BoolVar(&options.lanOnly, "lan-only", false, "install without hosted rendezvous, ICE, or TURN")
}

type vpsSetupOptions struct {
	connectivityDomain string
	turnDomain         string
	email              string
	publicIP           string
}

type macSetupOptions struct {
	role                   string
	deviceName             string
	cacheSize              string
	lanOnly                bool
	connectivityDomain     string
	turnDomain             string
	turnServer             string
	turnUsername           string
	turnPassword           string
	customizeBase          []string
	includeRoleInCustomize bool
}

func defaultMacSetupOptions(role string, customizeBase ...string) macSetupOptions {
	return macSetupOptions{
		role:          role,
		customizeBase: append([]string(nil), customizeBase...),
	}
}

func (options macSetupOptions) validate() error {
	switch strings.TrimSpace(options.role) {
	case string(device.RoleOrchestrator), string(device.RoleWorker):
	default:
		return errors.New("--role must be orchestrator or worker")
	}
	connectivityDomain := strings.TrimSpace(options.connectivityDomain)
	turnDomain := strings.TrimSpace(options.turnDomain)
	turnServer := strings.TrimSpace(options.turnServer)
	turnUsername := strings.TrimSpace(options.turnUsername)
	turnPassword := strings.TrimSpace(options.turnPassword)
	if options.lanOnly && (connectivityDomain != "" || turnDomain != "" || turnServer != "" || turnUsername != "" || turnPassword != "") {
		return errors.New("--lan-only cannot be combined with VPS, STUN, or TURN flags")
	}
	if connectivityDomain == "" && (turnDomain != "" || turnServer != "" || turnUsername != "" || turnPassword != "") {
		return errors.New("--connectivity-domain is required when configuring STUN or TURN")
	}
	if connectivityDomain != "" && turnDomain == "" && turnServer == "" {
		return errors.New("--connectivity-domain requires --turn-domain or --turn-server")
	}
	if turnServer != "" && !strings.HasPrefix(turnServer, "turn:") && !strings.HasPrefix(turnServer, "turns:") {
		return errors.New("--turn-server must begin with turn: or turns:")
	}
	if turnServer != "" && (turnUsername == "" || turnPassword == "") {
		return errors.New("--turn-server requires --turn-username and --turn-password")
	}
	if turnServer == "" && (turnUsername != "" || turnPassword != "") {
		return errors.New("--turn-username and --turn-password require --turn-server")
	}
	if strings.TrimSpace(options.cacheSize) == "" {
		return nil
	}
	if err := validateSetupCacheSize(options.cacheSize); err != nil {
		return fmt.Errorf("--cache-size: %w", err)
	}
	return nil
}

func validateSetupCacheSize(encoded string) error {
	value, err := parseSetupByteSize(encoded)
	if err != nil {
		return err
	}
	return contentcache.ValidateMaximumBytes(value)
}

func parseSetupByteSize(encoded string) (int64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(encoded))
	if normalized == "" {
		return 0, errors.New("invalid byte size: use a value such as 20GiB or 512MB")
	}
	type unit struct {
		suffix     string
		multiplier float64
	}
	units := []unit{
		{suffix: "TIB", multiplier: 1 << 40},
		{suffix: "GIB", multiplier: 1 << 30},
		{suffix: "MIB", multiplier: 1 << 20},
		{suffix: "KIB", multiplier: 1 << 10},
		{suffix: "TB", multiplier: 1_000_000_000_000},
		{suffix: "GB", multiplier: 1_000_000_000},
		{suffix: "MB", multiplier: 1_000_000},
		{suffix: "KB", multiplier: 1_000},
		{suffix: "B", multiplier: 1},
	}
	multiplier := float64(1)
	number := normalized
	for _, candidate := range units {
		if strings.HasSuffix(normalized, candidate.suffix) {
			multiplier = candidate.multiplier
			number = strings.TrimSpace(strings.TrimSuffix(normalized, candidate.suffix))
			break
		}
	}
	numeric, err := strconv.ParseFloat(number, 64)
	bytes := numeric * multiplier
	if err != nil || numeric <= 0 || math.IsInf(bytes, 0) || math.IsNaN(bytes) ||
		bytes > math.MaxInt64 || bytes != math.Trunc(bytes) {
		return 0, fmt.Errorf("invalid byte size %q: use a value such as 20GiB or 512MB", encoded)
	}
	return int64(bytes), nil
}

func (options macSetupOptions) installCommand() string {
	role := strings.TrimSpace(options.role)
	deviceName := strings.TrimSpace(options.deviceName)
	if role == string(device.RoleOrchestrator) && deviceName == "" &&
		strings.TrimSpace(options.cacheSize) == "" &&
		!options.lanOnly &&
		strings.TrimSpace(options.connectivityDomain) == "" {
		return "make install-macos"
	}
	parts := []string{"./packaging/macos/install.sh", "--role", role}
	if deviceName != "" {
		parts = append(parts, "--device-name", deviceName)
	} else if role == string(device.RoleWorker) {
		parts = append(parts, "--device-name", "Gaming PC")
	}
	if strings.TrimSpace(options.cacheSize) != "" {
		parts = append(parts, "--cache-size", options.cacheSize)
	}
	if options.lanOnly {
		parts = append(parts, "--lan-only")
	}
	if strings.TrimSpace(options.connectivityDomain) != "" {
		parts = append(
			parts,
			"--connectivity-url", "https://"+strings.TrimSpace(options.connectivityDomain),
		)
		if strings.TrimSpace(options.turnDomain) != "" {
			parts = append(parts, "--stun-server", "stun:"+strings.TrimSpace(options.turnDomain)+":3478")
		}
		if strings.TrimSpace(options.turnServer) != "" {
			parts = append(
				parts,
				"--turn-server", strings.TrimSpace(options.turnServer),
				"--turn-username", strings.TrimSpace(options.turnUsername),
				"--turn-password", strings.TrimSpace(options.turnPassword),
			)
		}
	}
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = shellArg(part)
	}
	return strings.Join(escaped, " ")
}

func (options macSetupOptions) customizeCommand() string {
	role := strings.TrimSpace(options.role)
	if role == "" {
		role = string(device.RoleOrchestrator)
	}
	deviceName := strings.TrimSpace(options.deviceName)
	if deviceName == "" && role == string(device.RoleWorker) {
		deviceName = "Gaming PC"
	}
	parts := append([]string(nil), options.customizeBase...)
	if len(parts) == 0 {
		parts = []string{"computehop", "setup", "mac"}
	}
	if options.includeRoleInCustomize {
		parts = append(parts, "--role", role)
	}
	if deviceName != "" {
		parts = append(parts, "--device-name", deviceName)
	}
	if strings.TrimSpace(options.cacheSize) != "" {
		parts = append(parts, "--cache-size", options.cacheSize)
	}
	if options.lanOnly {
		parts = append(parts, "--lan-only")
	}
	if strings.TrimSpace(options.connectivityDomain) != "" {
		parts = append(
			parts,
			"--connectivity-domain", strings.TrimSpace(options.connectivityDomain),
		)
		if strings.TrimSpace(options.turnDomain) != "" {
			parts = append(parts, "--turn-domain", strings.TrimSpace(options.turnDomain))
		}
		if strings.TrimSpace(options.turnServer) != "" {
			parts = append(
				parts,
				"--turn-server", strings.TrimSpace(options.turnServer),
				"--turn-username", strings.TrimSpace(options.turnUsername),
				"--turn-password", strings.TrimSpace(options.turnPassword),
			)
		}
	}
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = shellArg(part)
	}
	return strings.Join(escaped, " ")
}

func (options macSetupOptions) vpsReminderCommand() string {
	withVPS := options
	withVPS.lanOnly = false
	withVPS.connectivityDomain = "connect.example.com"
	withVPS.turnDomain = "turn.example.com"
	return withVPS.customizeCommand()
}

func defaultVPSSetupOptions() vpsSetupOptions {
	return vpsSetupOptions{
		connectivityDomain: "connect.example.com",
		turnDomain:         "turn.example.com",
		email:              "admin@example.com",
		publicIP:           "203.0.113.10",
	}
}

func (options vpsSetupOptions) initCommand() string {
	return fmt.Sprintf(
		"./init.sh --connectivity-domain %s --turn-domain %s --email %s --public-ip %s",
		shellArg(options.connectivityDomain),
		shellArg(options.turnDomain),
		shellArg(options.email),
		shellArg(options.publicIP),
	)
}

func (options vpsSetupOptions) orchestratorInstallCommand() string {
	return fmt.Sprintf(
		"./packaging/macos/install.sh --role orchestrator --connectivity-url %s --stun-server %s",
		shellArg("https://"+options.connectivityDomain),
		shellArg("stun:"+options.turnDomain+":3478"),
	)
}

func (options vpsSetupOptions) workerInstallCommand() string {
	return fmt.Sprintf(
		"./packaging/macos/install.sh --role worker --device-name \"Gaming PC\" --connectivity-url %s --stun-server %s",
		shellArg("https://"+options.connectivityDomain),
		shellArg("stun:"+options.turnDomain+":3478"),
	)
}

func (options vpsSetupOptions) orchestratorSetupCommand() string {
	return macSetupOptions{
		role:               string(device.RoleOrchestrator),
		connectivityDomain: options.connectivityDomain,
		turnDomain:         options.turnDomain,
		customizeBase:      []string{"computehop", "setup", "orchestrator"},
	}.customizeCommand()
}

func (options vpsSetupOptions) workerSetupCommand() string {
	return macSetupOptions{
		role:               string(device.RoleWorker),
		deviceName:         "Gaming PC",
		connectivityDomain: options.connectivityDomain,
		turnDomain:         options.turnDomain,
		customizeBase:      []string{"computehop", "setup", "worker"},
	}.customizeCommand()
}

func shellArg(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$`!*?[]{}()<>|&;") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func printSetupGuide(stdout io.Writer) error {
	vpsDefaults := defaultVPSSetupOptions()
	lines := []string{
		"ComputeHop setup",
		"",
		"1. Print the exact macOS install command for this computer:",
		"   computehop setup orchestrator",
		"   computehop setup worker --device-name \"Gaming PC\"",
		"   # Advanced equivalent: computehop setup mac --role worker --device-name \"Gaming PC\"",
		"",
		"2. Check this computer:",
		"   computehop doctor",
		"",
		"3. Install a worker on another Mac on the same LAN:",
		"   computehop setup worker --device-name \"Gaming PC\"",
		"   # Development-only alternative: go run ./cmd/computehopd --role worker --device-name \"Gaming PC\"",
		"",
		"4. Connect devices:",
		"   computehop connect",
		"   computehop connect nearby",
		"   computehop connect <device>",
		"   computehop connect confirm",
		"",
		"5. Run a smoke test:",
		"   computehop smoke",
		"",
		"After buying the VPS:",
		"   computehop setup vps",
		"   cd deploy/vps",
		"   sudo ./bootstrap-ubuntu.sh",
		"   " + vpsDefaults.initCommand(),
		"   docker compose up -d --build",
		"   ./verify.sh",
		"   ./turn-credentials.sh",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func printMacSetupGuide(stdout io.Writer, options macSetupOptions) error {
	lines := []string{
		"ComputeHop macOS setup",
		"",
		"Customize:",
		"   " + options.customizeCommand(),
		"",
		"Install on this Mac:",
		"   " + options.installCommand(),
		"",
		"After install:",
		"   computehop doctor",
	}
	if strings.TrimSpace(options.role) == string(device.RoleWorker) {
		lines = append(lines,
			"",
			"Connect from your orchestrator Mac:",
			"   computehop connect nearby",
			"   computehop connect confirm",
			"",
			"Confirm on this worker if a pairing request is waiting:",
			"   computehop connect confirm",
		)
	} else {
		lines = append(lines,
			"",
			"Connect a worker on the same LAN:",
			"   computehop connect nearby",
			"   computehop connect confirm",
			"",
			"Smoke test:",
			"   computehop smoke",
		)
	}
	if strings.TrimSpace(options.connectivityDomain) == "" {
		lines = append(lines,
			"",
			"After buying the VPS, rerun with:",
			"   "+options.vpsReminderCommand(),
		)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func printVPSSetupGuide(stdout io.Writer, options vpsSetupOptions) error {
	lines := []string{
		"ComputeHop one-VPS setup",
		"",
		"Customize:",
		"   computehop setup vps --connectivity-domain " + shellArg(options.connectivityDomain) + " --turn-domain " + shellArg(options.turnDomain) + " --email " + shellArg(options.email) + " --public-ip " + shellArg(options.publicIP),
		"",
		"Buy:",
		"- Ubuntu 24.04 LTS VPS",
		"- 1 shared vCPU, 1 GiB RAM, static public IPv4",
		"- At least 1 TiB monthly transfer and provider bandwidth alerts",
		"- Budget about $5-10/month for the VPS before bandwidth overage; confirm included transfer and IPv4 pricing before buying",
		"",
		"DNS:",
		"- " + options.connectivityDomain + " -> " + options.publicIP,
		"- " + options.turnDomain + " -> " + options.publicIP,
		"",
		"Provider firewall:",
		"- Allow TCP 22 from your IP",
		"- Allow TCP 80/443, UDP 443, TCP/UDP 3478, UDP 49160-49200",
		"",
		"On the VPS:",
		"   git clone https://github.com/austinjiann/spare-compute.git",
		"   cd spare-compute",
		"   sudo ./deploy/vps/bootstrap-ubuntu.sh",
		"   cd deploy/vps",
		"   " + options.initCommand(),
		"   docker compose config --quiet",
		"   docker compose up -d --build",
		"   ./verify.sh",
		"   ./turn-credentials.sh",
		"",
		"On each Mac after pairing once on the LAN, print the exact installer command:",
		"   " + options.orchestratorSetupCommand(),
		"   " + options.workerSetupCommand(),
		"",
		"Direct installer equivalents:",
		"   " + options.orchestratorInstallCommand(),
		"   " + options.workerInstallCommand(),
		"   # For forced relay testing, use the setup or installer commands printed by ./turn-credentials.sh with --turn-server, --turn-username, and --turn-password.",
		"",
		"Smoke test:",
		"   computehop devices",
		"   computehop smoke",
		"",
		"Boundary:",
		"- This enables rendezvous, direct ICE/STUN paths, and operator-provisioned TURN relay testing.",
		"- Public production relay still needs server-verifiable entitlement and quota enforcement before launch.",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func newPairCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	command := &cobra.Command{
		Use:    "pair [device]",
		Short:  "Start pairing or list current verification requests",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			if len(arguments) == 0 {
				return listPairings(command, stdout, client)
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_BeginPairing{BeginPairing: &localv1.BeginPairingRequest{
					DeviceSelector: arguments[0],
				}},
			})
			if err != nil {
				return err
			}
			result := response.GetBeginPairing()
			if result == nil {
				return fmt.Errorf("%w: missing begin-pairing result", ErrInvalidDaemonResponse)
			}
			value, err := mapper.PairingFromProto(result.GetPairing())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			return printPairingInstructions(stdout, value)
		},
	}
	command.AddCommand(newPairDecisionCommand(stdout, clientForCommand, true))
	command.AddCommand(newPairDecisionCommand(stdout, clientForCommand, false))
	return command
}

func newConnectCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	command := &cobra.Command{
		Use:   "connect [nearby|device]",
		Short: "Connect another computer",
		Long: "Connect starts trust setup with another ComputeHop device.\n\n" +
			"Run without arguments to see the next connection step for the current state.\n" +
			"Use 'nearby' when exactly one nearby unpaired worker is visible, or pass a\n" +
			"device name, full presence ID, or short presence ID prefix to choose explicitly.\n" +
			"'auto' remains a compatibility alias for 'nearby'.",
		Example: strings.Join([]string{
			"computehop connect",
			"computehop connect nearby",
			"computehop connect \"Gaming PC\"",
			"computehop connect confirm",
		}, "\n"),
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			if len(arguments) == 0 {
				response, err := client.Call(command.Context(), &localv1.Request{
					Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
				})
				if err != nil {
					return err
				}
				result := response.GetListDevices()
				if result == nil {
					return fmt.Errorf("%w: missing device list result", ErrInvalidDaemonResponse)
				}
				return printDoctorDevices(stdout, result)
			}
			var value trust.Pairing
			if isConnectAutoSelector(arguments[0]) {
				value, err = beginAutomaticPairing(command.Context(), client)
			} else {
				value, err = beginPairing(command.Context(), client, arguments[0])
			}
			if err != nil {
				return err
			}
			return printPairingInstructionsWithConfirm(stdout, value, "computehop connect confirm")
		},
	}
	command.AddCommand(newPairDecisionCommand(stdout, clientForCommand, true))
	command.AddCommand(newPairDecisionCommand(stdout, clientForCommand, false))
	return command
}

func beginPairing(ctx context.Context, client caller, deviceSelector string) (trust.Pairing, error) {
	response, err := client.Call(ctx, &localv1.Request{
		Operation: &localv1.Request_BeginPairing{BeginPairing: &localv1.BeginPairingRequest{
			DeviceSelector: deviceSelector,
		}},
	})
	if err != nil {
		return trust.Pairing{}, err
	}
	result := response.GetBeginPairing()
	if result == nil {
		return trust.Pairing{}, fmt.Errorf("%w: missing begin-pairing result", ErrInvalidDaemonResponse)
	}
	value, err := mapper.PairingFromProto(result.GetPairing())
	if err != nil {
		return trust.Pairing{}, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
	}
	return value, nil
}

func beginAutomaticPairing(ctx context.Context, client caller) (trust.Pairing, error) {
	response, err := client.Call(ctx, &localv1.Request{
		Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
	})
	if err != nil {
		return trust.Pairing{}, err
	}
	result := response.GetListDevices()
	if result == nil {
		return trust.Pairing{}, fmt.Errorf("%w: missing device list result", ErrInvalidDaemonResponse)
	}
	candidates, err := nearbyUnpairedWorkers(result)
	if err != nil {
		return trust.Pairing{}, err
	}
	switch len(candidates) {
	case 0:
		if result.GetDiscoveryState() == localv1.DiscoveryState_DISCOVERY_STATE_UNAVAILABLE {
			reason := result.GetDiscoveryError()
			if reason == "" {
				reason = "multicast DNS is unavailable"
			}
			return trust.Pairing{}, fmt.Errorf(
				"no nearby unpaired worker found because LAN discovery is unavailable: %s; run 'computehop connect' for setup status",
				reason,
			)
		}
		return trust.Pairing{}, errors.New("no nearby unpaired worker found; start ComputeHop as a worker on another computer, then run 'computehop connect'")
	case 1:
		return beginPairing(ctx, client, candidates[0].presenceID.Short())
	default:
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, strconv.Quote(candidate.name))
		}
		return trust.Pairing{}, fmt.Errorf(
			"more than one nearby unpaired worker found: %s; choose one with 'computehop connect <device>'",
			strings.Join(names, ", "),
		)
	}
}

func nearbyUnpairedWorkers(result *localv1.ListDevicesResponse) ([]nearbyDeviceView, error) {
	activePeerKeys := make(map[string]int)
	for _, message := range result.GetTrustedDevices() {
		peer, err := mapper.TrustedPeerFromProto(message)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if peer.State == trust.StateActive && peer.Role == device.RoleWorker {
			activePeerKeys[deviceDisplayKey(peer.Name, string(peer.Role))]++
		}
	}
	candidates := make([]nearbyDeviceView, 0, len(result.GetDevices()))
	for _, message := range result.GetDevices() {
		nearby, err := nearbyViewFromProto(message)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if nearby.role == string(device.RoleWorker) &&
			activePeerKeys[deviceDisplayKey(nearby.name, nearby.role)] == 0 {
			candidates = append(candidates, nearby)
		}
	}
	return candidates, nil
}

func listPairings(command *cobra.Command, stdout io.Writer, client caller) error {
	response, err := client.Call(command.Context(), &localv1.Request{
		Operation: &localv1.Request_ListPairings{ListPairings: &localv1.ListPairingsRequest{}},
	})
	if err != nil {
		return err
	}
	result := response.GetListPairings()
	if result == nil {
		return fmt.Errorf("%w: missing pairing list result", ErrInvalidDaemonResponse)
	}
	if len(result.GetPairings()) == 0 {
		_, err = fmt.Fprintln(stdout, "No pairing requests.")
		return err
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tDEVICE\tCODE\tDIRECTION\tLOCAL\tREMOTE\tSTATE\tEXPIRES"); err != nil {
		return err
	}
	for _, message := range result.GetPairings() {
		value, err := mapper.PairingFromProto(message)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if _, err := fmt.Fprintf(
			writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			value.ID.Short(), value.PeerName, value.Verification, value.Direction,
			yesNo(value.LocalConfirmed), yesNo(value.RemoteConfirmed), value.State,
			value.ExpiresAt.Format(time.RFC3339),
		); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func newPairDecisionCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
	confirmed bool,
) *cobra.Command {
	verb := "confirm"
	short := "Confirm that the verification code matches on this device"
	if !confirmed {
		verb = "reject"
		short = "Reject a pairing request on this device"
	}
	return &cobra.Command{
		Use:   verb + " [pairing]",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			pairingSelector := ""
			if len(arguments) == 1 {
				pairingSelector = arguments[0]
			} else {
				pairingSelector, err = inferPairingSelector(command, client, confirmed)
				if err != nil {
					return err
				}
			}
			var request *localv1.Request
			if confirmed {
				request = &localv1.Request{Operation: &localv1.Request_ConfirmPairing{
					ConfirmPairing: &localv1.ConfirmPairingRequest{PairingSelector: pairingSelector},
				}}
			} else {
				request = &localv1.Request{Operation: &localv1.Request_RejectPairing{
					RejectPairing: &localv1.RejectPairingRequest{PairingSelector: pairingSelector},
				}}
			}
			response, err := client.Call(command.Context(), request)
			if err != nil {
				return err
			}
			var message *localv1.Pairing
			if confirmed && response.GetConfirmPairing() != nil {
				message = response.GetConfirmPairing().GetPairing()
			}
			if !confirmed && response.GetRejectPairing() != nil {
				message = response.GetRejectPairing().GetPairing()
			}
			value, err := mapper.PairingFromProto(message)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			if confirmed {
				return printConfirmationResult(stdout, command, value)
			}
			_, err = fmt.Fprintf(stdout, "Rejected pairing with %s.\n", value.PeerName)
			return err
		},
	}
}

func printConfirmationResult(stdout io.Writer, command *cobra.Command, value trust.Pairing) error {
	if command.Parent() == nil || command.Parent().Name() != "connect" {
		_, err := fmt.Fprintf(stdout, "Confirmed %s locally; state: %s.\n", value.PeerName, value.State)
		return err
	}
	switch value.State {
	case trust.PairingPaired:
		_, err := fmt.Fprintf(stdout, "Connected to %s.\n", value.PeerName)
		return err
	case trust.PairingWaiting:
		if _, err := fmt.Fprintf(stdout, "Confirmed %s on this device.\n", value.PeerName); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Finish on the other device with: %s confirm\n", command.Parent().CommandPath())
		return err
	default:
		_, err := fmt.Fprintf(stdout, "Confirmed %s on this device; state: %s.\n", value.PeerName, value.State)
		return err
	}
}

func inferPairingSelector(command *cobra.Command, client caller, confirmed bool) (string, error) {
	response, err := client.Call(command.Context(), &localv1.Request{
		Operation: &localv1.Request_ListPairings{ListPairings: &localv1.ListPairingsRequest{}},
	})
	if err != nil {
		return "", err
	}
	result := response.GetListPairings()
	if result == nil {
		return "", fmt.Errorf("%w: missing pairing list result", ErrInvalidDaemonResponse)
	}
	candidates := make([]trust.Pairing, 0, len(result.GetPairings()))
	for _, message := range result.GetPairings() {
		value, err := mapper.PairingFromProto(message)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if value.State == trust.PairingWaiting && (!confirmed || !value.LocalConfirmed) {
			candidates = append(candidates, value)
		}
	}
	if len(candidates) == 1 {
		return string(candidates[0].ID), nil
	}
	verb := "reject"
	if confirmed {
		verb = "confirm"
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"no pairing request is ready to %s; run '%s' to inspect device readiness",
			verb, command.Parent().CommandPath(),
		)
	}
	choices := make([]string, len(candidates))
	for index, candidate := range candidates {
		choices[index] = fmt.Sprintf("%s (%s)", candidate.PeerName, candidate.ID.Short())
	}
	return "", fmt.Errorf(
		"more than one pairing can be %sed: %s; choose one with '%s <id>'",
		verb, strings.Join(choices, ", "), command.CommandPath(),
	)
}

func newDisconnectCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:     "disconnect <device>",
		Aliases: []string{"unpair"},
		Short:   "Disconnect a paired device",
		Long: strings.TrimSpace(`Disconnect revokes this computer's saved trust for a paired device.

Use the device name or short device ID from computehop devices. To use that
computer again later, connect it again on the LAN and compare the verification
code on both devices.`),
		Example: strings.TrimSpace(`computehop disconnect "Gaming PC"
computehop disconnect abc12345`),
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_UnpairDevice{UnpairDevice: &localv1.UnpairDeviceRequest{
					DeviceSelector: arguments[0],
				}},
			})
			if err != nil {
				return err
			}
			if response.GetUnpairDevice() == nil {
				return fmt.Errorf("%w: missing unpair result", ErrInvalidDaemonResponse)
			}
			peer, err := mapper.TrustedPeerFromProto(response.GetUnpairDevice().GetDevice())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			_, err = fmt.Fprintf(stdout, "Disconnected %s (%s). Run 'computehop connect nearby' when it is nearby to connect again.\n", peer.Name, peer.DeviceID.Short())
			return err
		},
	}
}

func printPairingInstructions(stdout io.Writer, value trust.Pairing) error {
	return printPairingInstructionsWithConfirm(stdout, value, "computehop pair confirm")
}

func printPairingInstructionsWithConfirm(stdout io.Writer, value trust.Pairing, confirmCommand string) error {
	if _, err := fmt.Fprintf(stdout, "Pairing request %s opened with %s.\n", value.ID.Short(), value.PeerName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Verification code: %s\n", value.Verification); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "Compare this exact code on both devices. Do not confirm if it differs."); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "If the codes match, run this on both devices: %s\n", confirmCommand)
	return err
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func newVersionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ComputeHop CLI version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			_, err := fmt.Fprintln(stdout, version)
			return err
		},
	}
}

func newStatusCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check the local ComputeHop daemon",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
			})
			if err != nil {
				return err
			}
			ping := response.GetPing()
			if ping == nil {
				return fmt.Errorf("%w: missing ping result", ErrInvalidDaemonResponse)
			}
			if _, err := fmt.Fprintf(stdout, "computehopd %s ready\n", ping.GetDaemonVersion()); err != nil {
				return err
			}
			return printPingDeviceLine(stdout, ping)
		},
	}
}

func newDoctorCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Explain local daemon and device readiness",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := clientForCommand()
			if err != nil {
				return printDaemonStartAdvice(stdout, err)
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}},
			})
			if err != nil {
				if errors.Is(err, ErrDaemonNotRunning) {
					return printDaemonStartAdvice(stdout, err)
				}
				return err
			}
			ping := response.GetPing()
			if ping == nil {
				return fmt.Errorf("%w: missing ping result", ErrInvalidDaemonResponse)
			}
			if _, err := fmt.Fprintf(stdout, "Daemon: ok (computehopd %s)\n", ping.GetDaemonVersion()); err != nil {
				return err
			}
			if err := printPingDeviceLine(stdout, ping); err != nil {
				return err
			}

			response, err = client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}},
			})
			if err != nil {
				return err
			}
			result := response.GetListDevices()
			if result == nil {
				return fmt.Errorf("%w: missing device list result", ErrInvalidDaemonResponse)
			}
			return printDoctorDevices(stdout, result)
		},
	}
}

func printDaemonStartAdvice(stdout io.Writer, err error) error {
	if !errors.Is(err, ErrDaemonNotRunning) {
		return err
	}
	for _, line := range []string{
		"Daemon: not running",
		"",
		"Next:",
		"- If the app is installed: open -a ComputeHop",
		"- If you are developing from this repo: go run ./cmd/computehopd --role orchestrator --device-name \"This Mac\"",
		"- To print the exact menu-bar app and launch-at-login install command: computehop setup orchestrator",
		"- Then run: computehop doctor",
	} {
		if _, writeErr := fmt.Fprintln(stdout, line); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func printPingDeviceLine(stdout io.Writer, ping *localv1.PingResponse) error {
	line, err := pingDeviceLine(ping)
	if err != nil || line == "" {
		return err
	}
	_, err = fmt.Fprintln(stdout, line)
	return err
}

func pingDeviceLine(ping *localv1.PingResponse) (string, error) {
	if ping.GetDeviceId() == "" && ping.GetDeviceName() == "" &&
		ping.GetRole() == localv1.DeviceRole_DEVICE_ROLE_UNSPECIFIED {
		return "", nil
	}
	if ping.GetDeviceId() == "" || ping.GetDeviceName() == "" ||
		ping.GetRole() == localv1.DeviceRole_DEVICE_ROLE_UNSPECIFIED {
		return "", fmt.Errorf("%w: incomplete ping device identity", ErrInvalidDaemonResponse)
	}
	id, err := device.ParseID(ping.GetDeviceId())
	if err != nil {
		return "", fmt.Errorf("%w: invalid ping device ID", ErrInvalidDaemonResponse)
	}
	role, err := pingRoleLabel(ping.GetRole())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Device: %s (%s, %s)", ping.GetDeviceName(), role, id.Short()), nil
}

func pingRoleLabel(role localv1.DeviceRole) (string, error) {
	switch role {
	case localv1.DeviceRole_DEVICE_ROLE_WORKER:
		return string(device.RoleWorker), nil
	case localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR:
		return string(device.RoleOrchestrator), nil
	default:
		return "", fmt.Errorf("%w: invalid ping device role", ErrInvalidDaemonResponse)
	}
}

type doctorWorker struct {
	name     string
	selector string
}

func printDoctorDevices(stdout io.Writer, result *localv1.ListDevicesResponse) error {
	nearbyDevices := make([]nearbyDeviceView, 0, len(result.GetDevices()))
	nearbyByKey := make(map[string]int)
	for _, message := range result.GetDevices() {
		nearby, err := nearbyViewFromProto(message)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		nearbyDevices = append(nearbyDevices, nearby)
		nearbyByKey[deviceDisplayKey(nearby.name, nearby.role)]++
	}

	activePairs := 0
	revokedPairs := 0
	pairedWorkers := 0
	offlineWorkers := 0
	activePeerKeys := make(map[string]int)
	reachableWorkers := make([]doctorWorker, 0)
	for _, message := range result.GetTrustedDevices() {
		peer, err := mapper.TrustedPeerFromProto(message)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		key := deviceDisplayKey(peer.Name, string(peer.Role))
		switch peer.State {
		case trust.StateActive:
			activePairs++
			activePeerKeys[key]++
		case trust.StateRevoked:
			revokedPairs++
		}
		if peer.State != trust.StateActive || peer.Role != device.RoleWorker {
			continue
		}
		pairedWorkers++
		reachable := nearbyByKey[key] > 0
		if message.GetConnectivityState() == localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED {
			reachable = true
		}
		if reachable {
			reachableWorkers = append(reachableWorkers, doctorWorker{
				name:     peer.Name,
				selector: peer.DeviceID.Short(),
			})
		} else {
			offlineWorkers++
		}
	}
	unpairedNearbyDevices := make([]nearbyDeviceView, 0, len(nearbyDevices))
	for _, nearby := range nearbyDevices {
		if activePeerKeys[deviceDisplayKey(nearby.name, nearby.role)] > 0 {
			continue
		}
		unpairedNearbyDevices = append(unpairedNearbyDevices, nearby)
	}

	if _, err := fmt.Fprintf(stdout, "LAN discovery: %s\n", doctorDiscoveryLabel(result)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Paired devices: %d active, %d revoked\n", activePairs, revokedPairs); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		stdout,
		"Reachable workers: %d%s\n",
		len(reachableWorkers),
		doctorWorkerNames(reachableWorkers),
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Nearby unpaired devices: %d\n", len(unpairedNearbyDevices)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "\nNext:"); err != nil {
		return err
	}
	switch {
	case len(reachableWorkers) > 0:
		selector := reachableWorkers[0].selector
		if len(reachableWorkers) == 1 {
			selector = "auto"
		}
		_, err := fmt.Fprintf(stdout, "- Run a smoke test: computehop smoke --on %s\n", selector)
		return err
	case len(unpairedNearbyDevices) > 0:
		unpairedWorkers := make([]nearbyDeviceView, 0, len(unpairedNearbyDevices))
		for _, nearby := range unpairedNearbyDevices {
			if nearby.role == string(device.RoleWorker) {
				unpairedWorkers = append(unpairedWorkers, nearby)
			}
		}
		if len(unpairedWorkers) == 1 {
			if _, err := fmt.Fprintln(stdout, "- Connect to the nearby worker: computehop connect nearby"); err != nil {
				return err
			}
		} else if len(unpairedWorkers) > 1 {
			if _, err := fmt.Fprintf(
				stdout,
				"- Choose a nearby worker: computehop connect %s\n",
				strconv.Quote(unpairedWorkers[0].name),
			); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(
			stdout,
			"- Connect to a nearby device: computehop connect %s\n",
			strconv.Quote(unpairedNearbyDevices[0].name),
		); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "- After comparing the code on both devices, run on both: computehop connect confirm")
		return err
	case pairedWorkers > 0:
		if _, err := fmt.Fprintf(stdout, "- %d paired worker(s) exist but are not reachable right now.\n", offlineWorkers); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "- Start computehopd on the worker, then run: computehop devices")
		return err
	case result.GetDiscoveryState() == localv1.DiscoveryState_DISCOVERY_STATE_STARTING:
		_, err := fmt.Fprintln(stdout, "- Wait a few seconds, then run: computehop devices")
		return err
	default:
		if _, err := fmt.Fprintln(
			stdout,
			"- Print the exact worker install command for another Mac on this LAN: computehop setup worker --device-name \"Gaming PC\"",
		); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(
			stdout,
			"- Development-only alternative: go run ./cmd/computehopd --role worker --device-name \"Gaming PC\"",
		); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "- Then run: computehop devices")
		return err
	}
}

func doctorDiscoveryLabel(result *localv1.ListDevicesResponse) string {
	switch result.GetDiscoveryState() {
	case localv1.DiscoveryState_DISCOVERY_STATE_STARTING:
		return "starting"
	case localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE:
		return "available"
	case localv1.DiscoveryState_DISCOVERY_STATE_UNAVAILABLE:
		if result.GetDiscoveryError() != "" {
			return "unavailable (" + result.GetDiscoveryError() + ")"
		}
		return "unavailable"
	default:
		return "unknown"
	}
}

func doctorWorkerNames(workers []doctorWorker) string {
	if len(workers) == 0 {
		return ""
	}
	names := make([]string, 0, min(len(workers), 3))
	for index, worker := range workers {
		if index >= 3 {
			break
		}
		names = append(names, worker.name)
	}
	if len(workers) > len(names) {
		names = append(names, fmt.Sprintf("+%d more", len(workers)-len(names)))
	}
	return " (" + strings.Join(names, ", ") + ")"
}

func newRunCommand(
	stdout io.Writer,
	getwd func() (string, error),
	clientForCommand func() (caller, error),
) *cobra.Command {
	var deviceSelector string
	var workingDirectory string
	var outputs []string
	var follow bool
	var wait bool
	var fetchOutputs bool
	var artifactDestination string
	var noProject bool
	command := &cobra.Command{
		Use:   "run [--on auto|device] <program> [args...]",
		Short: "Run a background command here or on a paired worker",
		Long: "Run submits a native background process.\n\n" +
			"Without --on, the command runs on this computer. With --on auto, ComputeHop\n" +
			"uses the single active paired worker. With --on <device>, it targets a named\n" +
			"or short-ID paired worker. Declare outputs with -o and add --get when you want\n" +
			"the CLI to wait for success and restore those outputs immediately.",
		Example: strings.Join([]string{
			"computehop run hostname",
			"computehop run --on auto --no-project hostname",
			"computehop run --on auto cargo build --release",
			"computehop run --on \"Gaming PC\" -C /local/project go test ./...",
			"computehop run --on auto -o target/release/app --follow --get cargo build --release",
		}, "\n"),
		Args: cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if artifactDestination != "" && !fetchOutputs {
				return errors.New("--to requires --get")
			}
			if fetchOutputs && len(outputs) == 0 {
				return errors.New("--get requires at least one declared output with -o/--output")
			}
			if noProject && deviceSelector == "" {
				return errors.New("--no-project requires --on auto or --on <device>")
			}
			if noProject && workingDirectory != "" {
				return errors.New("--no-project cannot be combined with --working-directory/-C")
			}
			if noProject && len(outputs) > 0 {
				return errors.New("--no-project cannot be combined with --output/-o")
			}
			targetDirectory := workingDirectory
			if noProject {
				targetDirectory = ""
			} else if targetDirectory == "" {
				var err error
				targetDirectory, err = getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
			}
			spec, err := mapper.SpecToProto(job.Spec{
				Executable:       arguments[0],
				Arguments:        arguments[1:],
				WorkingDirectory: targetDirectory,
				Executor:         job.ExecutorNative,
				Outputs:          outputs,
			})
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			if shouldPrintRemotePreparation(deviceSelector, targetDirectory) {
				if _, err := fmt.Fprintf(
					command.ErrOrStderr(),
					"Preparing remote run for %s from %s; snapshot/upload may take a moment.\n",
					deviceSelectorDisplay(deviceSelector),
					targetDirectory,
				); err != nil {
					return err
				}
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{
					Spec: spec, DeviceSelector: deviceSelector,
				}},
			})
			if err != nil {
				return runSubmitError(deviceSelector, err)
			}
			result := response.GetSubmitJob()
			if result == nil {
				return fmt.Errorf("%w: missing submit result", ErrInvalidDaemonResponse)
			}
			value, err := mapper.JobFromProto(result.GetJob())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			if deviceSelector == "" {
				_, err = fmt.Fprintf(stdout, "Submitted %s (%s)\n", value.ID, value.State)
			} else {
				_, err = fmt.Fprintf(
					stdout,
					"Submitted %s to %s (%s)\n",
					value.ID,
					deviceSelectorDisplay(deviceSelector),
					value.State,
				)
			}
			if err != nil {
				return err
			}
			shouldWait := wait || follow || fetchOutputs
			if !shouldWait {
				if _, err = fmt.Fprintf(stdout, "Follow it: computehop logs --follow %s\n", value.ID); err != nil {
					return err
				}
				if len(outputs) > 0 {
					_, err = fmt.Fprintf(stdout, "Get outputs after it succeeds: computehop outputs %s\n", value.ID)
				}
				return err
			}

			if follow {
				value, err = streamJobLogs(
					command.Context(), stdout, command.ErrOrStderr(), client, value.ID, deviceSelector, true,
				)
			} else {
				if _, err = fmt.Fprintf(stdout, "Waiting for %s to finish...\n", value.ID); err != nil {
					return err
				}
				value, err = waitForJob(command.Context(), client, value.ID, deviceSelector)
			}
			if err != nil {
				return err
			}
			if _, err = fmt.Fprintf(stdout, "Job %s %s\n", value.ID, value.State); err != nil {
				return err
			}
			if value.State != job.StateSucceeded {
				return fmt.Errorf("job %s ended as %s", value.ID, value.State)
			}
			if !fetchOutputs {
				if len(outputs) > 0 {
					_, err = fmt.Fprintf(stdout, "Get outputs: computehop outputs %s\n", value.ID)
				}
				return err
			}
			return fetchArtifactsWithDefault(
				command.Context(), stdout, command.ErrOrStderr(), getwd, client,
				value.ID, deviceSelector, targetDirectory, artifactDestination,
			)
		},
	}
	command.Flags().SetInterspersed(false)
	addDeviceSelectorFlags(command, &deviceSelector)
	command.Flags().StringVarP(
		&workingDirectory,
		"working-directory",
		"C",
		"",
		"local project directory to snapshot (defaults to the current directory)",
	)
	command.Flags().StringArrayVarP(
		&outputs,
		"output",
		"o",
		nil,
		"relative output file or directory to return (repeatable)",
	)
	command.Flags().BoolVarP(&follow, "follow", "f", false, "stream logs until the job finishes")
	command.Flags().BoolVar(&wait, "wait", false, "wait until the job finishes without streaming logs")
	command.Flags().BoolVar(&fetchOutputs, "get", false, "after success, download declared outputs")
	command.Flags().BoolVar(&fetchOutputs, "fetch", false, "alias for --get")
	command.Flags().BoolVar(&noProject, "no-project", false, "skip project snapshot for remote commands that do not need local files")
	command.Flags().StringVarP(
		&artifactDestination,
		"to",
		"t",
		"",
		"output destination when used with --get (defaults to the submitted working directory)",
	)
	return command
}

func shouldPrintRemotePreparation(selector string, workingDirectory string) bool {
	return strings.TrimSpace(selector) != "" && strings.TrimSpace(workingDirectory) != ""
}

func newSmokeCommand(
	stdout io.Writer,
	stderr io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var deviceSelector string
	command := &cobra.Command{
		Use:   "smoke",
		Short: "Run a cheap remote connectivity smoke test",
		Long: "Run a cheap remote connectivity smoke test.\n\n" +
			"Smoke submits hostname to a paired worker without uploading a project,\n" +
			"streams the result, and reports whether the remote job succeeded.",
		Example: strings.Join([]string{
			"computehop smoke",
			"computehop smoke --on \"Gaming PC\"",
		}, "\n"),
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			spec, err := mapper.SpecToProto(job.Spec{
				Executable: "hostname",
				Executor:   job.ExecutorNative,
			})
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{
					Spec: spec, DeviceSelector: deviceSelector,
				}},
			})
			if err != nil {
				return runSubmitError(deviceSelector, err)
			}
			result := response.GetSubmitJob()
			if result == nil {
				return fmt.Errorf("%w: missing submit result", ErrInvalidDaemonResponse)
			}
			value, err := mapper.JobFromProto(result.GetJob())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			if _, err := fmt.Fprintf(
				stdout,
				"Submitted smoke test %s to %s (%s)\n",
				value.ID,
				deviceSelectorDisplay(deviceSelector),
				value.State,
			); err != nil {
				return err
			}
			value, err = streamJobLogs(command.Context(), stdout, stderr, client, value.ID, deviceSelector, true)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(stdout, "Job %s %s\n", value.ID, value.State); err != nil {
				return err
			}
			if value.State != job.StateSucceeded {
				return fmt.Errorf("smoke test job %s ended as %s", value.ID, value.State)
			}
			_, err = fmt.Fprintln(stdout, "Smoke test passed.")
			return err
		},
	}
	addDeviceSelectorFlagsWithDefault(command, &deviceSelector, "auto")
	return command
}

func newArtifactsCommand(
	stdout io.Writer,
	stderr io.Writer,
	getwd func() (string, error),
	clientForCommand func() (caller, error),
) *cobra.Command {
	var destination string
	var deviceSelector string
	command := &cobra.Command{
		Use:     "outputs <job-id>",
		Aliases: []string{"artifacts", "fetch", "download"},
		Short:   "Download a completed job's declared outputs",
		Long: "Download declared outputs for a completed job.\n\n" +
			"By default ComputeHop infers the worker from the job ID and restores into\n" +
			".computehop-results/<job-id>. Use --to to choose another destination.",
		Example: strings.Join([]string{
			"computehop outputs <job-id>",
			"computehop outputs <job-id> --to ./results",
		}, "\n"),
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			id, err := job.ParseID(arguments[0])
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			return fetchArtifacts(command.Context(), stdout, stderr, getwd, client, id, deviceSelector, destination)
		},
	}
	command.Flags().StringVarP(&destination, "to", "t", "", "destination directory (defaults to .computehop-results/<job-id>)")
	addDeviceSelectorFlags(command, &deviceSelector)
	return command
}

func fetchArtifacts(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	getwd func() (string, error),
	client caller,
	id job.ID,
	deviceSelector string,
	destination string,
) error {
	return fetchArtifactsWithDefault(ctx, stdout, stderr, getwd, client, id, deviceSelector, "", destination)
}

func fetchArtifactsWithDefault(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	getwd func() (string, error),
	client caller,
	id job.ID,
	deviceSelector string,
	defaultDestination string,
	destination string,
) error {
	target, err := resolveArtifactDestination(getwd, id, defaultDestination, destination)
	if err != nil {
		return err
	}
	response, err := client.Call(ctx, &localv1.Request{
		Operation: &localv1.Request_FetchArtifacts{FetchArtifacts: &localv1.FetchArtifactsRequest{
			JobId: string(id), DeviceSelector: deviceSelector, Destination: target,
		}},
	})
	if err != nil {
		return err
	}
	result := response.GetFetchArtifacts()
	if result == nil || result.GetDestination() == "" {
		return fmt.Errorf("%w: missing artifact result", ErrInvalidDaemonResponse)
	}
	if _, err := fmt.Fprintf(
		stdout, "Restored %d output file(s) to %s\n",
		result.GetRestoredFileCount(), result.GetDestination(),
	); err != nil {
		return err
	}
	if result.GetConflictFileCount() > 0 {
		if _, err := fmt.Fprintf(
			stderr,
			"Kept existing files unchanged; %d incoming conflict(s) are under %s\n",
			result.GetConflictFileCount(), filepath.Join(result.GetDestination(), ".computehop-conflicts"),
		); err != nil {
			return err
		}
	}
	return nil
}

func resolveArtifactDestination(
	getwd func() (string, error),
	id job.ID,
	defaultDestination string,
	destination string,
) (string, error) {
	if destination == "" {
		if defaultDestination != "" {
			if filepath.IsAbs(defaultDestination) {
				return filepath.Clean(defaultDestination), nil
			}
			target, err := filepath.Abs(defaultDestination)
			if err != nil {
				return "", fmt.Errorf("resolve output directory: %w", err)
			}
			return target, nil
		}
		workingDirectory, err := getwd()
		if err != nil {
			return "", fmt.Errorf("resolve output directory: %w", err)
		}
		return filepath.Join(workingDirectory, ".computehop-results", string(id)), nil
	}
	if filepath.IsAbs(destination) {
		return destination, nil
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	return target, nil
}

func newJobsCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var limit uint32
	var deviceSelector string
	command := &cobra.Command{
		Use:   "jobs",
		Short: "List durable jobs",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_ListJobs{ListJobs: &localv1.ListJobsRequest{
					Limit: limit, DeviceSelector: deviceSelector,
				}},
			})
			if err != nil {
				return err
			}
			result := response.GetListJobs()
			if result == nil {
				return fmt.Errorf("%w: missing job list result", ErrInvalidDaemonResponse)
			}
			if len(result.GetJobs()) == 0 {
				_, err = fmt.Fprintln(stdout, "No jobs.")
				return err
			}

			writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "ID\tSTATE\tPROGRESS\tCOMMAND\tUPDATED"); err != nil {
				return err
			}
			for _, message := range result.GetJobs() {
				value, err := mapper.JobFromProto(message)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				commandText := strings.Join(append([]string{value.Spec.Executable}, value.Spec.Arguments...), " ")
				if _, err := fmt.Fprintf(
					writer,
					"%s\t%s\t%s\t%s\t%s\n",
					value.ID,
					value.State,
					formatJobProgress(value.Progress),
					commandText,
					value.UpdatedAt.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	command.Flags().Uint32Var(&limit, "limit", 100, "maximum jobs to return")
	addDeviceSelectorFlags(command, &deviceSelector)
	return command
}

func newCancelCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var deviceSelector string
	command := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel a durable job",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			id, err := job.ParseID(arguments[0])
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_CancelJob{CancelJob: &localv1.CancelJobRequest{
					JobId: string(id), DeviceSelector: deviceSelector,
				}},
			})
			if err != nil {
				return err
			}
			result := response.GetCancelJob()
			if result == nil {
				return fmt.Errorf("%w: missing cancellation result", ErrInvalidDaemonResponse)
			}
			value, err := mapper.JobFromProto(result.GetJob())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
			}
			if value.State == job.StateCancelled {
				_, err = fmt.Fprintf(stdout, "Cancelled %s\n", value.ID)
			} else {
				_, err = fmt.Fprintf(stdout, "Cancellation requested for %s (%s)\n", value.ID, value.State)
			}
			return err
		},
	}
	addDeviceSelectorFlags(command, &deviceSelector)
	return command
}

func newLogsCommand(
	stdout io.Writer,
	stderr io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var follow bool
	var deviceSelector string
	command := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Read durable stdout and stderr for a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			id, err := job.ParseID(arguments[0])
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			_, err = streamJobLogs(command.Context(), stdout, stderr, client, id, deviceSelector, follow)
			return err
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "wait for new output until the job finishes")
	addDeviceSelectorFlags(command, &deviceSelector)
	return command
}

func streamJobLogs(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	client caller,
	id job.ID,
	deviceSelector string,
	follow bool,
) (job.Job, error) {
	var after uint64
	for {
		response, err := client.Call(ctx, &localv1.Request{
			Operation: &localv1.Request_ReadJobLogs{ReadJobLogs: &localv1.ReadJobLogsRequest{
				JobId:          string(id),
				AfterSequence:  after,
				Limit:          32,
				DeviceSelector: deviceSelector,
			}},
		})
		if err != nil {
			return job.Job{}, err
		}
		result := response.GetReadJobLogs()
		if result == nil {
			return job.Job{}, fmt.Errorf("%w: missing job logs result", ErrInvalidDaemonResponse)
		}
		for _, record := range result.GetRecords() {
			if record.GetSequence() <= after {
				return job.Job{}, fmt.Errorf("%w: job log sequence did not advance", ErrInvalidDaemonResponse)
			}
			var destination io.Writer
			switch record.GetStream() {
			case localv1.JobLogStream_JOB_LOG_STREAM_STDOUT:
				destination = stdout
			case localv1.JobLogStream_JOB_LOG_STREAM_STDERR:
				destination = stderr
			default:
				return job.Job{}, fmt.Errorf("%w: invalid job log stream", ErrInvalidDaemonResponse)
			}
			if _, err := destination.Write(record.GetData()); err != nil {
				return job.Job{}, err
			}
			after = record.GetSequence()
		}
		if result.GetHasMore() {
			continue
		}
		value, err := mapper.JobFromProto(result.GetJob())
		if err != nil {
			return job.Job{}, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if !follow || value.State.Terminal() {
			return value, nil
		}
		if err := waitForNextPoll(ctx); err != nil {
			return job.Job{}, err
		}
	}
}

func waitForJob(ctx context.Context, client caller, id job.ID, deviceSelector string) (job.Job, error) {
	for {
		response, err := client.Call(ctx, &localv1.Request{
			Operation: &localv1.Request_GetJob{GetJob: &localv1.GetJobRequest{
				JobId: string(id), DeviceSelector: deviceSelector,
			}},
		})
		if err != nil {
			return job.Job{}, err
		}
		result := response.GetGetJob()
		if result == nil {
			return job.Job{}, fmt.Errorf("%w: missing job result", ErrInvalidDaemonResponse)
		}
		value, err := mapper.JobFromProto(result.GetJob())
		if err != nil {
			return job.Job{}, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if value.State.Terminal() {
			return value, nil
		}
		if err := waitForNextPoll(ctx); err != nil {
			return job.Job{}, err
		}
	}
}

func waitForNextPoll(ctx context.Context) error {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatJobProgress(progress *job.Progress) string {
	if progress == nil {
		return "—"
	}
	percent := int64(0)
	if progress.TotalBytes > 0 {
		percent = progress.CompletedBytes * 100 / progress.TotalBytes
	}
	return fmt.Sprintf(
		"%s %d%% (%s/%s)",
		progressPhaseLabel(progress.Phase),
		percent,
		formatByteCount(progress.CompletedBytes),
		formatByteCount(progress.TotalBytes),
	)
}

func progressPhaseLabel(phase job.ProgressPhase) string {
	switch phase {
	case job.ProgressSnapshot:
		return "snapshot"
	case job.ProgressUpload:
		return "upload"
	case job.ProgressDownload:
		return "download"
	case job.ProgressRestore:
		return "restore"
	case job.ProgressCollect:
		return "collect"
	default:
		return string(phase)
	}
}

func formatByteCount(value int64) string {
	type unit struct {
		suffix string
		bytes  int64
	}
	for _, candidate := range []unit{
		{suffix: "GiB", bytes: 1 << 30},
		{suffix: "MiB", bytes: 1 << 20},
		{suffix: "KiB", bytes: 1 << 10},
	} {
		if value >= candidate.bytes {
			whole := value / candidate.bytes
			tenth := value % candidate.bytes * 10 / candidate.bytes
			if tenth == 0 {
				return fmt.Sprintf("%d%s", whole, candidate.suffix)
			}
			return fmt.Sprintf("%d.%d%s", whole, tenth, candidate.suffix)
		}
	}
	return fmt.Sprintf("%dB", value)
}

func addDeviceSelectorFlags(command *cobra.Command, destination *string) {
	addDeviceSelectorFlagsWithDefault(command, destination, "")
}

func addDeviceSelectorFlagsWithDefault(command *cobra.Command, destination *string, defaultValue string) {
	command.Flags().StringVar(destination, "on", defaultValue, "paired worker name, device ID, or auto (single active worker)")
	command.Flags().StringVar(destination, "device", defaultValue, "paired worker name, device ID, or auto (legacy alias for --on)")
	_ = command.Flags().MarkHidden("device")
}

func runSubmitError(selector string, err error) error {
	var remoteError *localipc.RemoteError
	if !errors.As(err, &remoteError) {
		return err
	}
	if isAutomaticSelector(selector) {
		if remoteError.Code == localv1.ErrorCode_ERROR_CODE_DEVICE_UNAVAILABLE &&
			isAutoSelectorNoWorkerMessage(remoteError.Message) {
			return errors.New(
				"no active paired worker is available for --on auto; run 'computehop connect nearby' when one nearby worker is visible, or run 'computehop devices' to choose a worker",
			)
		}
		if remoteError.Code == localv1.ErrorCode_ERROR_CODE_CONFLICT &&
			isAutoSelectorAmbiguousMessage(remoteError.Message) {
			return errors.New(
				"more than one active paired worker is available for --on auto; run 'computehop devices', then choose one with 'computehop run --on <device> ...'",
			)
		}
		return err
	}
	selector = strings.TrimSpace(selector)
	if selector != "" && remoteError.Code == localv1.ErrorCode_ERROR_CODE_NOT_FOUND &&
		isExplicitSelectorNoWorkerMessage(remoteError.Message) {
		return fmt.Errorf(
			"no active paired worker matches %q; run 'computehop devices' to see worker names/IDs, or run 'computehop connect nearby' if the worker is nearby but still unpaired",
			selector,
		)
	}
	if selector != "" && remoteError.Code == localv1.ErrorCode_ERROR_CODE_CONFLICT &&
		isExplicitSelectorAmbiguousMessage(remoteError.Message) {
		return fmt.Errorf(
			"more than one active paired worker matches %q; run 'computehop devices', then use a longer device ID or exact worker name with 'computehop run --on <device> ...'",
			selector,
		)
	}
	return err
}

func isAutoSelectorNoWorkerMessage(message string) bool {
	return strings.Contains(message, "automatic worker selection found no active paired workers") ||
		strings.Contains(message, "no active paired worker is available for --on auto")
}

func isAutoSelectorAmbiguousMessage(message string) bool {
	return (strings.Contains(message, "automatic worker selection found") &&
		strings.Contains(message, "active workers")) ||
		strings.Contains(message, "more than one active paired worker is available for --on auto")
}

func isExplicitSelectorNoWorkerMessage(message string) bool {
	return strings.Contains(message, "active worker")
}

func isExplicitSelectorAmbiguousMessage(message string) bool {
	return strings.Contains(message, "matches") && strings.Contains(message, "active workers")
}

func isConnectAutoSelector(selector string) bool {
	switch strings.ToLower(strings.TrimSpace(selector)) {
	case "auto", "nearby":
		return true
	default:
		return false
	}
}

func isAutomaticSelector(selector string) bool {
	switch strings.ToLower(strings.TrimSpace(selector)) {
	case "auto", "best":
		return true
	default:
		return false
	}
}

func deviceSelectorDisplay(selector string) string {
	if isAutomaticSelector(selector) {
		return "an automatically selected worker"
	}
	return selector
}
