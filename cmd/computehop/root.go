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
	ErrDaemonNotRunning       = errors.New("ComputeHop daemon is not running")
	ErrDaemonProtocolMismatch = errors.New("ComputeHop daemon does not match this CLI")
	ErrInvalidDaemonResponse  = errors.New("invalid response from computehopd")
)

const (
	exampleWorkerDeviceName   = "Gaming PC"
	exampleConnectivityDomain = "connect.example.com"
	exampleTurnDomain         = "turn.example.com"
	exampleOperatorEmail      = "admin@example.com"
	exampleVPSPublicIP        = "203.0.113.10"
	exampleWorkerCacheSize    = "40GiB"
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
		Use:   "computehop",
		Short: "Run background jobs across your computers",
		Long: strings.TrimSpace(`ComputeHop runs background jobs on this Mac or on paired workers.

Normal flow:
1. Print the local setup command for this Mac.
2. Connect a nearby worker once on the LAN.
3. Run a smoke test.
4. Run real commands and fetch declared outputs when needed.

Use doctor when setup or connectivity is unclear.`),
		Example: strings.TrimSpace(`computehop setup
computehop connect
computehop connect nearby
computehop smoke
computehop run --on auto --no-project hostname
computehop run --on auto -o dist --follow --get npm run build
computehop outputs <job-id>
computehop doctor`),
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
	root.AddCommand(newDiagnosticsCommand(dependencies.stdout, clientForCommand))
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

After the table, devices prints the next command to try for the current state:
install a worker, connect a nearby worker, choose a run target, smoke-test a
connected worker, or fix offline connectivity.

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
						return printDevicesNextStep(stdout, devicesNextStepSummary{})
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
				if _, err = fmt.Fprintln(stdout, "No connected or nearby devices."); err != nil {
					return err
				}
				return printDevicesNextStep(stdout, devicesNextStepSummary{})
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
			nextStep := devicesNextStepSummary{hasDevices: len(result.GetDevices()) > 0 || len(result.GetTrustedDevices()) > 0}
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
				nextStep.addTrustedPeer(peer, availability, path)
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
				nextStep.addNearbyDevice(nearby)
				if _, err := fmt.Fprintf(
					writer,
					"%s\t%s\tnot connected\t%s\t%s\tLAN\t%s\t%s\n",
					nearby.name, nearby.presenceID.Short(), nearby.role, nearby.availability,
					nearby.address, nearby.lastSeen.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			return printDevicesNextStep(stdout, nextStep)
		},
	}
}

type devicesNextStepSummary struct {
	hasDevices             bool
	pairableWorkers        int
	reachableWorkers       int
	connectingWorkers      int
	offlineWorkers         int
	lanOnlyOfflineWorkers  int
	revokedWorkerRecords   int
	nonWorkerDeviceRecords int
}

func (summary *devicesNextStepSummary) addTrustedPeer(peer trust.Peer, availability string, path string) {
	if peer.Role != device.RoleWorker {
		summary.nonWorkerDeviceRecords++
		return
	}
	switch peer.State {
	case trust.StateActive:
		switch availability {
		case "nearby", "remote":
			summary.reachableWorkers++
		case "connecting":
			summary.connectingWorkers++
		default:
			summary.offlineWorkers++
			if path == "LAN only" {
				summary.lanOnlyOfflineWorkers++
			}
		}
	case trust.StateRevoked:
		summary.revokedWorkerRecords++
	}
}

func (summary *devicesNextStepSummary) addNearbyDevice(nearby nearbyDeviceView) {
	if nearby.role == string(device.RoleWorker) {
		summary.pairableWorkers++
		return
	}
	summary.nonWorkerDeviceRecords++
}

func printDevicesNextStep(stdout io.Writer, summary devicesNextStepSummary) error {
	lines := devicesNextStepLines(summary)
	if len(lines) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "Next:"); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(stdout, "- %s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func devicesNextStepLines(summary devicesNextStepSummary) []string {
	switch {
	case summary.reachableWorkers == 1:
		return []string{
			"Run a worker smoke test: computehop smoke",
			"Run a command on the worker: computehop run --on auto hostname",
		}
	case summary.reachableWorkers > 1:
		return []string{
			"Choose a worker for a smoke test: computehop smoke --on <device>",
			"Choose a worker for a command: computehop run --on <device> hostname",
		}
	case summary.pairableWorkers == 1:
		return []string{
			"Connect the nearby worker: computehop connect nearby",
			"Confirm on both devices: computehop connect confirm",
		}
	case summary.pairableWorkers > 1:
		return []string{
			"Choose a worker to connect: computehop connect <device>",
			"Use the NAME or IDENTIFIER from the table, then run: computehop connect confirm",
		}
	case summary.lanOnlyOfflineWorkers > 0:
		return []string{
			"Put LAN-only workers on the same LAN.",
			"Advanced cross-network setup starts with: computehop setup vps",
		}
	case summary.connectingWorkers > 0:
		return []string{
			"Wait for remote connectivity to finish, then run: computehop smoke",
			"Refresh this list: computehop devices",
		}
	case summary.offlineWorkers > 0 || summary.revokedWorkerRecords > 0 || summary.hasDevices:
		return []string{
			"Start ComputeHop on the worker or put both devices on the same LAN.",
			"Look for setup issues: computehop doctor",
		}
	default:
		return []string{
			"Install a worker on another Mac: " + exampleSetupCommand(device.RoleWorker),
			"Then connect it from this Mac: computehop connect nearby",
		}
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
want the exact install command for this Mac or a worker Mac. Advanced subcommands
cover LAN-only installs, Linux/Windows worker packages, and the self-hosted VPS
connectivity stack.`),
		Example: strings.TrimSpace(`computehop setup
computehop setup orchestrator
computehop setup worker --device-name "Gaming PC"
computehop setup worker --device-name "Gaming PC" --lan-only
computehop setup workers
computehop setup smoke
computehop setup vps --connectivity-domain connect.example.com --turn-domain turn.example.com --email admin@example.com --public-ip 203.0.113.10`),
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return printSetupGuide(stdout)
		},
	}
	command.AddCommand(newSetupMacCommand(stdout))
	command.AddCommand(newSetupMacRoleCommand(stdout, device.RoleOrchestrator))
	command.AddCommand(newSetupMacRoleCommand(stdout, device.RoleWorker))
	command.AddCommand(newSetupWorkersCommand(stdout))
	command.AddCommand(newSetupSmokeCommand(stdout))
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
computehop setup mac --role orchestrator --connectivity-domain connect.example.com --turn-domain turn.example.com`),
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
		return strings.Join([]string{
			exampleHelpSetupCommand(device.RoleWorker),
			exampleHelpSetupCommand(device.RoleWorker) + " --cache-size " + exampleWorkerCacheSize,
			exampleHelpSetupCommand(device.RoleWorker) + " --lan-only",
			exampleHelpSetupCommand(device.RoleWorker) + " --connectivity-domain " + exampleConnectivityDomain + " --turn-domain " + exampleTurnDomain,
		}, "\n")
	default:
		return strings.Join([]string{
			exampleHelpSetupCommand(device.RoleOrchestrator),
			exampleHelpSetupCommand(device.RoleOrchestrator) + " --cache-size " + exampleWorkerCacheSize,
			exampleHelpSetupCommand(device.RoleOrchestrator) + " --lan-only",
			exampleHelpSetupCommand(device.RoleOrchestrator) + " --connectivity-domain " + exampleConnectivityDomain + " --turn-domain " + exampleTurnDomain,
		}, "\n")
	}
}

func newSetupWorkersCommand(stdout io.Writer) *cobra.Command {
	options := workerPackageSetupOptions{
		deviceName: exampleWorkerDeviceName,
		target:     "all",
	}
	command := &cobra.Command{
		Use:     "workers",
		Aliases: []string{"pc", "worker-packages"},
		Short:   "Print the Linux and Windows worker package checklist",
		Long: strings.TrimSpace(`Print copyable Linux and Windows worker package commands.

Use this when the Mac is the orchestrator and another computer should run as a
worker. This command only prints the package and pairing checklist; it does not
install anything or require the daemon. Same-LAN mode is the default; pass VPS
connectivity flags after deploying the rendezvous/TURN stack.`),
		Example: strings.TrimSpace(`computehop setup workers
computehop setup workers --target linux --device-name "Home Server"
computehop setup workers --target windows --device-name "Gaming PC"
computehop setup workers --connectivity-domain connect.example.com --turn-domain turn.example.com`),
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := options.validate(); err != nil {
				return err
			}
			return printWorkerPackageSetupGuide(stdout, options)
		},
	}
	command.Flags().StringVar(&options.deviceName, "device-name", options.deviceName, "human-readable worker device name")
	command.Flags().StringVar(&options.target, "target", options.target, "worker target: all, linux, or windows")
	command.Flags().StringVar(&options.connectivityDomain, "connectivity-domain", "", "advanced: public HTTPS domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnDomain, "turn-domain", "", "advanced: public STUN/TURN domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnServer, "turn-server", "", "advanced: TURN relay URI printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnUsername, "turn-username", "", "advanced: short-lived TURN username printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnPassword, "turn-password", "", "advanced: short-lived TURN password printed by deploy/vps/turn-credentials.sh")
	command.Flags().BoolVar(&options.lanOnly, "lan-only", false, "print LAN-only worker commands even when remote connectivity is not configured")
	return command
}

func newSetupVPSCommand(stdout io.Writer) *cobra.Command {
	options := defaultVPSSetupOptions()
	command := &cobra.Command{
		Use:   "vps",
		Short: "Print the advanced one-VPS deployment checklist",
		Long: strings.TrimSpace(`Print a provider-neutral one-VPS checklist for advanced cross-network testing.

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

func newSetupSmokeCommand(stdout io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "smoke",
		Short: "Print the two-Mac package smoke checklist",
		Long: strings.TrimSpace(`Print a two-Mac package smoke checklist without requiring the daemon.

Use this after changing packaging, install, pairing, run submission, logs, or
artifacts. The checklist starts LAN-only so the packaged path is proven before
advanced cross-network connectivity is added.`),
		Example: "computehop setup smoke",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return printPackageSmokeGuide(stdout)
		},
	}
	return command
}

func addMacSetupFlags(command *cobra.Command, options *macSetupOptions) {
	command.Flags().StringVar(&options.deviceName, "device-name", "", "human-readable device name")
	command.Flags().StringVar(&options.cacheSize, "cache-size", "", "verified content cache limit, for example 40GiB")
	command.Flags().StringVar(&options.connectivityDomain, "connectivity-domain", "", "advanced: public HTTPS domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnDomain, "turn-domain", "", "advanced: public STUN/TURN domain from the one-VPS setup")
	command.Flags().StringVar(&options.turnServer, "turn-server", "", "advanced: TURN relay URI printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnUsername, "turn-username", "", "advanced: short-lived TURN username printed by deploy/vps/turn-credentials.sh")
	command.Flags().StringVar(&options.turnPassword, "turn-password", "", "advanced: short-lived TURN password printed by deploy/vps/turn-credentials.sh")
	command.Flags().BoolVar(&options.lanOnly, "lan-only", false, "install without hosted rendezvous, ICE, or TURN")
}

type vpsSetupOptions struct {
	connectivityDomain string
	turnDomain         string
	email              string
	publicIP           string
}

type workerPackageSetupOptions struct {
	deviceName         string
	target             string
	lanOnly            bool
	connectivityDomain string
	turnDomain         string
	turnServer         string
	turnUsername       string
	turnPassword       string
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
	if err := validateSetupConnectivity(
		options.lanOnly,
		options.connectivityDomain,
		options.turnDomain,
		options.turnServer,
		options.turnUsername,
		options.turnPassword,
	); err != nil {
		return err
	}
	if strings.TrimSpace(options.cacheSize) == "" {
		return nil
	}
	if err := validateSetupCacheSize(options.cacheSize); err != nil {
		return fmt.Errorf("--cache-size: %w", err)
	}
	return nil
}

func validateSetupConnectivity(
	lanOnly bool,
	connectivityDomain string,
	turnDomain string,
	turnServer string,
	turnUsername string,
	turnPassword string,
) error {
	connectivityDomain = strings.TrimSpace(connectivityDomain)
	turnDomain = strings.TrimSpace(turnDomain)
	turnServer = strings.TrimSpace(turnServer)
	turnUsername = strings.TrimSpace(turnUsername)
	turnPassword = strings.TrimSpace(turnPassword)
	if lanOnly && (connectivityDomain != "" || turnDomain != "" || turnServer != "" || turnUsername != "" || turnPassword != "") {
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
	return nil
}

func (options workerPackageSetupOptions) validate() error {
	switch strings.ToLower(strings.TrimSpace(options.target)) {
	case "all", "linux", "windows":
	default:
		return errors.New("--target must be all, linux, or windows")
	}
	if err := validateSetupConnectivity(
		options.lanOnly,
		options.connectivityDomain,
		options.turnDomain,
		options.turnServer,
		options.turnUsername,
		options.turnPassword,
	); err != nil {
		return err
	}
	return nil
}

func workerPackageDaemonArgs(options workerPackageSetupOptions) []string {
	if options.lanOnly || strings.TrimSpace(options.connectivityDomain) == "" {
		return []string{"--lan-only"}
	}
	args := []string{
		"--connectivity-url", "https://" + strings.TrimSpace(options.connectivityDomain),
	}
	if strings.TrimSpace(options.turnDomain) != "" {
		args = append(args, "--stun-server", "stun:"+strings.TrimSpace(options.turnDomain)+":3478")
	}
	if strings.TrimSpace(options.turnServer) != "" {
		args = append(
			args,
			"--turn-server", strings.TrimSpace(options.turnServer),
			"--turn-username", strings.TrimSpace(options.turnUsername),
			"--turn-password", strings.TrimSpace(options.turnPassword),
		)
	}
	return args
}

func shellArgs(values []string) string {
	if len(values) == 0 {
		return ""
	}
	escaped := make([]string, len(values))
	for index, value := range values {
		escaped[index] = shellArg(value)
	}
	return strings.Join(escaped, " ")
}

func workerPackagePowerShellArgs(options workerPackageSetupOptions) []string {
	if options.lanOnly || strings.TrimSpace(options.connectivityDomain) == "" {
		return []string{"-LanOnly"}
	}
	args := []string{
		"-ConnectivityUrl", "https://" + strings.TrimSpace(options.connectivityDomain),
	}
	if strings.TrimSpace(options.turnDomain) != "" {
		args = append(args, "-StunServer", "stun:"+strings.TrimSpace(options.turnDomain)+":3478")
	}
	if strings.TrimSpace(options.turnServer) != "" {
		args = append(
			args,
			"-TurnServer", strings.TrimSpace(options.turnServer),
			"-TurnUsername", strings.TrimSpace(options.turnUsername),
			"-TurnPassword", strings.TrimSpace(options.turnPassword),
		)
	}
	return args
}

func powershellOptionArgs(values []string) string {
	if len(values) == 0 {
		return ""
	}
	escaped := make([]string, len(values))
	for index, value := range values {
		if strings.HasPrefix(value, "-") && value != "-" {
			escaped[index] = value
			continue
		}
		escaped[index] = powershellArg(value)
	}
	return strings.Join(escaped, " ")
}

func appendCommandArgs(command string, encodedArgs string) string {
	if strings.TrimSpace(encodedArgs) == "" {
		return command
	}
	return command + " " + encodedArgs
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
		parts = append(parts, "--device-name", exampleWorkerDeviceName)
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

func (options macSetupOptions) installCheckCommand() string {
	command := options.installCommand()
	if command == "make install-macos" {
		return "make install-macos-check"
	}
	const installer = "./packaging/macos/install.sh"
	if strings.HasPrefix(command, installer+" ") {
		return installer + " --check" + strings.TrimPrefix(command, installer)
	}
	return command
}

func (options macSetupOptions) customizeCommand() string {
	role := strings.TrimSpace(options.role)
	if role == "" {
		role = string(device.RoleOrchestrator)
	}
	deviceName := strings.TrimSpace(options.deviceName)
	if deviceName == "" && role == string(device.RoleWorker) {
		deviceName = exampleWorkerDeviceName
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

func defaultVPSSetupOptions() vpsSetupOptions {
	return vpsSetupOptions{
		connectivityDomain: exampleConnectivityDomain,
		turnDomain:         exampleTurnDomain,
		email:              exampleOperatorEmail,
		publicIP:           exampleVPSPublicIP,
	}
}

func (options vpsSetupOptions) initCommand() string {
	return options.initCommandWithExecutable("./init.sh")
}

func (options vpsSetupOptions) rootInitCommand() string {
	return options.initCommandWithExecutable("./deploy/vps/init.sh")
}

func (options vpsSetupOptions) initCommandWithExecutable(executable string) string {
	return fmt.Sprintf(
		"%s --connectivity-domain %s --turn-domain %s --email %s --public-ip %s",
		executable,
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
		"./packaging/macos/install.sh --role worker --device-name %s --connectivity-url %s --stun-server %s",
		shellArg(exampleWorkerDeviceName),
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
		deviceName:         exampleWorkerDeviceName,
		connectivityDomain: options.connectivityDomain,
		turnDomain:         options.turnDomain,
		customizeBase:      []string{"computehop", "setup", "worker"},
	}.customizeCommand()
}

func exampleHelpSetupCommand(role device.Role) string {
	if role == device.RoleWorker {
		return "computehop setup worker --device-name " + strconv.Quote(exampleWorkerDeviceName)
	}
	return "computehop setup " + string(role)
}

func exampleSetupCommand(role device.Role) string {
	options := macSetupOptions{
		role:          string(role),
		customizeBase: []string{"computehop", "setup", string(role)},
	}
	if role == device.RoleWorker {
		options.deviceName = exampleWorkerDeviceName
	}
	return options.customizeCommand()
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

func powershellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func printSetupGuide(stdout io.Writer) error {
	lines := []string{
		"ComputeHop setup",
		"",
		"Happy path:",
		"",
		"1. Install this Mac as the orchestrator:",
		"   computehop setup orchestrator",
		"   computehop doctor",
		"",
		"2. Install a worker on another Mac on the same LAN:",
		"   computehop setup worker --device-name \"Gaming PC\"",
		"   # For Linux/Windows workers, build copyable packages with: make worker-archives",
		"",
		"3. Connect once while both devices are nearby:",
		"   computehop connect nearby",
		"   # Compare the code, then run on both devices:",
		"   computehop connect confirm",
		"",
		"4. Test the worker:",
		"   computehop smoke",
		"",
		"Useful next commands:",
		"   computehop devices",
		"   computehop run --on auto --no-project hostname",
		"   computehop run --on auto -C . --follow cargo test",
		"",
		"Advanced:",
		"   computehop setup mac --role worker --device-name \"Gaming PC\" --lan-only",
		"   computehop setup vps",
		"   # Use VPS/TURN only after same-LAN setup works.",
		"",
		"Development-only daemon:",
		"   go run ./cmd/computehopd --role worker --device-name \"Gaming PC\"",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func printWorkerPackageSetupGuide(stdout io.Writer, options workerPackageSetupOptions) error {
	deviceName := strings.TrimSpace(options.deviceName)
	if deviceName == "" {
		deviceName = exampleWorkerDeviceName
	}
	target := strings.ToLower(strings.TrimSpace(options.target))
	if target == "" {
		target = "all"
	}
	daemonArgs := workerPackageDaemonArgs(options)
	encodedShellArgs := shellArgs(daemonArgs)
	encodedPowerShellArgs := powershellOptionArgs(workerPackagePowerShellArgs(options))
	lines := []string{
		"ComputeHop Linux/Windows worker setup",
		"",
		"Goal:",
		"   run another computer as a worker controlled by the Mac orchestrator.",
		"",
		"Mode:",
		"   " + workerPackageModeLabel(options),
		"",
		"0. On the Mac checkout, build copyable worker packages:",
		"   make worker-archives",
		"",
	}
	if target == "all" || target == "linux" {
		lines = append(lines,
			"Linux worker:",
			"   # Copy the matching archive and .sha256 from dist/workers/ to the Linux computer.",
			"   shasum -a 256 -c ComputeHop-worker-linux-amd64.tar.gz.sha256",
			"   # If shasum is unavailable on Linux:",
			"   sha256sum -c ComputeHop-worker-linux-amd64.tar.gz.sha256",
			"   tar -xzf ComputeHop-worker-linux-amd64.tar.gz",
			"   cd ComputeHop-worker-linux-amd64",
			"   # Check the copied package and service settings without installing anything:",
			"   COMPUTEHOP_DEVICE_NAME="+shellArg(deviceName)+" "+appendCommandArgs("./install-systemd-user.sh --check", encodedShellArgs),
			"   # Or run the worker directly for this terminal session:",
			"   COMPUTEHOP_DEVICE_NAME="+shellArg(deviceName)+" "+appendCommandArgs("./run-worker.sh", encodedShellArgs),
			"",
			"Linux optional login service after the check passes:",
			"   COMPUTEHOP_DEVICE_NAME="+shellArg(deviceName)+" "+appendCommandArgs("./install-systemd-user.sh", encodedShellArgs),
			"",
		)
	}
	if target == "all" || target == "windows" {
		lines = append(lines,
			"Windows worker:",
			"   # Copy the Windows zip and .sha256 from dist/workers/ to the Windows computer.",
			"   Get-FileHash .\\ComputeHop-worker-windows-amd64.zip -Algorithm SHA256",
			"   # Compare that hash with ComputeHop-worker-windows-amd64.zip.sha256.",
			"   Expand-Archive .\\ComputeHop-worker-windows-amd64.zip .",
			"   cd .\\ComputeHop-worker-windows-amd64",
			"   # Check the copied package and scheduled-task settings without installing anything:",
			"   "+appendCommandArgs(".\\install-scheduled-task.ps1 -Check -DeviceName "+powershellArg(deviceName), encodedPowerShellArgs),
			"   # Or run the worker directly for this PowerShell session:",
			"   "+appendCommandArgs(".\\run-worker.ps1 -DeviceName "+powershellArg(deviceName), encodedPowerShellArgs),
			"",
			"Windows optional login task after the check passes:",
			"   "+appendCommandArgs(".\\install-scheduled-task.ps1 -DeviceName "+powershellArg(deviceName), encodedPowerShellArgs),
			"",
		)
	}
	lines = append(lines,
		"Pair from the Mac orchestrator while both devices are on the same LAN:",
		"   computehop connect nearby",
		"   computehop connect confirm",
		"",
	)
	switch target {
	case "linux":
		lines = append(lines,
			"Confirm the same code on the Linux worker:",
			"   ./bin/computehop connect confirm",
			"",
		)
	case "windows":
		lines = append(lines,
			"Confirm the same code on the Windows worker:",
			"   .\\bin\\computehop.exe connect confirm",
			"",
		)
	default:
		lines = append(lines,
			"Confirm the same code on the worker:",
			"   # Linux:",
			"   ./bin/computehop connect confirm",
			"   # Windows:",
			"   .\\bin\\computehop.exe connect confirm",
			"",
		)
	}
	lines = append(lines,
		"Prove remote execution:",
		"   computehop smoke",
		"   computehop run --on auto --no-project --follow hostname",
		"",
		"After smoke prints the worker hostname, remote jobs are running on that computer.",
	)
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func workerPackageModeLabel(options workerPackageSetupOptions) string {
	if strings.TrimSpace(options.connectivityDomain) == "" || options.lanOnly {
		return "LAN-only. Pair and run while both devices are on the same network."
	}
	if strings.TrimSpace(options.turnServer) != "" {
		return "VPS rendezvous, STUN, and authenticated TURN relay."
	}
	return "VPS rendezvous and STUN. LAN is still preferred when available."
}

func printMacSetupGuide(stdout io.Writer, options macSetupOptions) error {
	lines := []string{
		"ComputeHop macOS setup",
		"",
		"Customize:",
		"   " + options.customizeCommand(),
		"",
		"Check without changing this Mac:",
		"   " + options.installCheckCommand(),
		"",
		"Install on this Mac:",
		"   " + options.installCommand(),
		"",
		"After install:",
		"   computehop doctor",
	}
	if options.lanOnly {
		lines = append(lines,
			"",
			"Mode:",
			"   LAN-only. Keep the two devices on the same network.",
		)
	} else if strings.TrimSpace(options.connectivityDomain) != "" {
		lines = append(lines,
			"",
			"Mode:",
			"   Same-LAN first, with configured cross-network connectivity.",
		)
	} else {
		lines = append(lines,
			"",
			"Mode:",
			"   Same-LAN first. Add the VPS later only if you need cross-network workers.",
		)
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
			"Advanced cross-network setup:",
			"   computehop setup vps",
			"   # Then rerun this setup command with --connectivity-domain and --turn-domain.",
		)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func printPackageSmokeGuide(stdout io.Writer) error {
	lines := []string{
		"ComputeHop two-Mac package smoke",
		"",
		"Goal:",
		"   prove the packaged app can install, pair, run remotely through the CLI and Control Center, stream logs, cancel, and return outputs.",
		"",
		"0. Build a copyable package once from the checkout:",
		"   make macos-archive",
		"   # Creates dist/macos/ComputeHop-macos.zip and .sha256.",
		"",
		"1. On the orchestrator Mac, after copying ComputeHop-macos.zip and .sha256:",
		"   shasum -a 256 -c ComputeHop-macos.zip.sha256",
		"   ditto -x -k ComputeHop-macos.zip .",
		"   cd ComputeHop-macos",
		"   ./install.sh --check --role orchestrator --lan-only",
		"   ./install.sh --role orchestrator --lan-only",
		"   ./validate-installed.sh --role orchestrator --lan-only --run-local-smoke",
		"   computehop doctor",
		"",
		"2. On the worker Mac, after copying ComputeHop-macos.zip and .sha256:",
		"   shasum -a 256 -c ComputeHop-macos.zip.sha256",
		"   ditto -x -k ComputeHop-macos.zip .",
		"   cd ComputeHop-macos",
		"   ./install.sh --check --role worker --device-name 'Gaming PC' --lan-only",
		"   ./install.sh --role worker --device-name 'Gaming PC' --lan-only",
		"   ./validate-installed.sh --role worker --device-name 'Gaming PC' --lan-only",
		"",
		"3. Pair while both Macs are on the same LAN:",
		"   # orchestrator",
		"   computehop connect nearby",
		"   computehop connect confirm",
		"   # worker",
		"   computehop connect confirm",
		"",
		"4. Prove CLI remote execution and logs:",
		"   computehop smoke",
		"   computehop run --on auto --no-project --follow hostname",
		"",
		"5. Prove Control Center direct-run recovery:",
		"   # On the orchestrator Mac, open ComputeHop Control Center.",
		"   # Enter: run hostname on the other computer",
		"   # Click Run. If the worker is not connected yet, Run should start Connect.",
		"   # Confirm the same pairing code on both Macs; the task should resume on the worker.",
		"   # The job output should print the worker hostname.",
		"",
		"6. Prove cancellation:",
		"   computehop run --on auto --no-project /bin/sleep 3600",
		"   computehop cancel <job-id>",
		"   computehop jobs --on auto",
		"",
		"7. Prove project transfer and returned outputs from a project folder:",
		"   computehop run --on auto -C /path/to/project -o smoke-output.txt --follow --get /bin/sh -c 'printf ok > smoke-output.txt'",
		"",
		"8. If something fails:",
		"   computehop doctor",
		"   computehop devices",
		"   tail -n 100 ~/Library/Logs/ComputeHop/daemon.log",
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
		"SSH:",
		"   ssh root@" + options.publicIP,
		"",
		"On the VPS:",
		"   git clone https://github.com/austinjiann/spare-compute.git",
		"   cd spare-compute",
		"   sudo ./deploy/vps/bootstrap-ubuntu.sh",
		"   " + options.rootInitCommand(),
		"   docker compose --project-directory deploy/vps config --quiet",
		"   docker compose --project-directory deploy/vps up -d --build",
		"   ./deploy/vps/verify.sh",
		"   ./deploy/vps/turn-credentials.sh",
		"",
		"On each Mac after pairing once on the LAN, print the exact installer command:",
		"   " + options.orchestratorSetupCommand(),
		"   " + options.workerSetupCommand(),
		"",
		"Direct installer equivalents:",
		"   " + options.orchestratorInstallCommand(),
		"   " + options.workerInstallCommand(),
		"   # For forced relay testing, use the setup or installer commands printed by ./deploy/vps/turn-credentials.sh with --turn-server, --turn-username, and --turn-password.",
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
			return printPairingInstructionsWithConfirm(stdout, value, "computehop connect confirm")
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
				return printConnectStatus(command, stdout, client)
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

func printConnectStatus(command *cobra.Command, stdout io.Writer, client caller) error {
	pairings, err := waitingPairings(command.Context(), client, false)
	if err != nil {
		return err
	}
	if len(pairings) > 0 {
		return printWaitingConnectPairings(stdout, pairings)
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
	return printDoctorDevices(stdout, result)
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

func waitingPairings(ctx context.Context, client caller, needsLocalConfirmation bool) ([]trust.Pairing, error) {
	response, err := client.Call(ctx, &localv1.Request{
		Operation: &localv1.Request_ListPairings{ListPairings: &localv1.ListPairingsRequest{}},
	})
	if err != nil {
		return nil, err
	}
	result := response.GetListPairings()
	if result == nil {
		return nil, fmt.Errorf("%w: missing pairing list result", ErrInvalidDaemonResponse)
	}
	pairings := make([]trust.Pairing, 0, len(result.GetPairings()))
	for _, message := range result.GetPairings() {
		value, err := mapper.PairingFromProto(message)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if value.State == trust.PairingWaiting && (!needsLocalConfirmation || !value.LocalConfirmed) {
			pairings = append(pairings, value)
		}
	}
	return pairings, nil
}

func printWaitingConnectPairings(stdout io.Writer, pairings []trust.Pairing) error {
	if _, err := fmt.Fprintln(stdout, "Connection request waiting"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tDEVICE\tCODE\tTHIS DEVICE\tOTHER DEVICE\tEXPIRES"); err != nil {
		return err
	}
	for _, value := range pairings {
		if _, err := fmt.Fprintf(
			writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			value.ID.Short(), value.PeerName, value.Verification,
			confirmationStatus(value.LocalConfirmed), confirmationStatus(value.RemoteConfirmed),
			value.ExpiresAt.Format(time.RFC3339),
		); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "\nNext:"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "- Compare the exact code on both devices. Do not confirm if it differs."); err != nil {
		return err
	}
	if len(pairings) == 1 {
		if pairings[0].LocalConfirmed {
			_, err := fmt.Fprintln(stdout, "- This device is already confirmed. Finish on the other device with: computehop connect confirm")
			return err
		}
		if _, err := fmt.Fprintln(stdout, "- If it matches, run on this device: computehop connect confirm"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "- If it does not match, run: computehop connect reject")
		return err
	}
	if _, err := fmt.Fprintln(stdout, "- If one code matches, choose it with: computehop connect confirm <id>"); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "- If one code does not match, reject it with: computehop connect reject <id>")
	return err
}

func confirmationStatus(confirmed bool) string {
	if confirmed {
		return "confirmed"
	}
	return "not yet"
}

func newPairDecisionCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
	confirmed bool,
) *cobra.Command {
	verb := "confirm"
	short := "Confirm that the verification code matches on this device"
	long := strings.TrimSpace(`Confirm a waiting connection request after comparing the verification
code on both devices.

Run without an ID when only one request is waiting. If multiple requests are
waiting, use the short ID shown by computehop connect. Do not confirm if the
codes differ.`)
	example := strings.TrimSpace(`computehop connect
computehop connect confirm
computehop connect confirm <id>`)
	if !confirmed {
		verb = "reject"
		short = "Reject a pairing request on this device"
		long = strings.TrimSpace(`Reject a waiting connection request.

Run without an ID when only one request is waiting. If multiple requests are
waiting, use the short ID shown by computehop connect. Reject immediately when
the verification code does not match on both devices.`)
		example = strings.TrimSpace(`computehop connect
computehop connect reject
computehop connect reject <id>`)
	}
	return &cobra.Command{
		Use:     verb + " [pairing]",
		Short:   short,
		Long:    long,
		Example: example,
		Args:    cobra.MaximumNArgs(1),
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
	candidates, err := waitingPairings(command.Context(), client, confirmed)
	if err != nil {
		return "", err
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
		Long: strings.TrimSpace(`Check whether the local computehopd daemon is reachable.

When the daemon reports local identity, status prints this computer's device
name, role, and short device ID. Use doctor when status cannot connect or when
you want next-step setup guidance.`),
		Example: strings.TrimSpace(`computehop status
computehop doctor`),
		Args: cobra.NoArgs,
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
				if errors.Is(err, ErrDaemonNotRunning) || errors.Is(err, ErrDaemonProtocolMismatch) {
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
	var lines []string
	switch {
	case errors.Is(err, ErrDaemonProtocolMismatch):
		lines = []string{
			"Daemon: running, but not compatible with this CLI",
			"",
			"Next:",
			"- Check the packaged install from this checkout without changing this Mac: make install-macos-check",
			"- If the check passes, reinstall from this checkout: make install-macos",
			"- If you want to switch back to a manual development daemon: make uninstall-macos",
			"- Then start the daemon from this checkout:",
			"  go run ./cmd/computehopd --role orchestrator --device-name \"This Mac\"",
			"- Then run: computehop doctor",
		}
	case errors.Is(err, ErrDaemonNotRunning):
		lines = []string{
			"Daemon: not running",
			"",
			"Next:",
			"- If the app is installed: open -a ComputeHop",
			"- If you are developing from this repo: go run ./cmd/computehopd --role orchestrator --device-name \"This Mac\"",
			"- To print the exact menu-bar app and launch-at-login install commands: computehop setup orchestrator",
			"- To check that install path without changing this Mac: make install-macos-check",
			"- Then run: computehop doctor",
		}
	default:
		return err
	}
	for _, line := range lines {
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
			"chooses the best active paired worker. With --on <device>, it targets a named\n" +
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
				value, _, err = streamJobLogs(
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
			value, _, err = streamJobLogs(command.Context(), stdout, stderr, client, value.ID, deviceSelector, true)
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
		return fetchArtifactsError(id, err)
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

func fetchArtifactsError(id job.ID, err error) error {
	var remoteError *localipc.RemoteError
	if !errors.As(err, &remoteError) {
		return err
	}
	message := strings.ToLower(remoteError.Message)
	if remoteError.Code == localv1.ErrorCode_ERROR_CODE_CONFLICT &&
		strings.Contains(message, "job artifacts are not ready") {
		if strings.Contains(message, " is succeeded") {
			return fmt.Errorf(
				"no declared outputs are available for %s; rerun the job with -o/--output for each file or directory you want returned",
				id,
			)
		}
		if strings.Contains(message, " is failed") ||
			strings.Contains(message, " is cancelled") ||
			strings.Contains(message, " is rejected") ||
			strings.Contains(message, " is lost") {
			return fmt.Errorf(
				"outputs are not available for %s because the job ended before producing declared outputs",
				id,
			)
		}
		return fmt.Errorf(
			"outputs for %s are not ready yet; wait for the job to succeed, then run 'computehop outputs %s'",
			id, id,
		)
	}
	if remoteError.Code == localv1.ErrorCode_ERROR_CODE_NOT_FOUND &&
		strings.Contains(message, "artifacts") {
		return fmt.Errorf(
			"outputs were not found for %s; check the job ID/worker and make sure the job was submitted with -o/--output",
			id,
		)
	}
	if remoteError.Code == localv1.ErrorCode_ERROR_CODE_CONFLICT &&
		strings.Contains(message, "artifacts are not configured") {
		return errors.New(
			"output retrieval is not enabled on this daemon or worker; restart ComputeHop from this checkout, then run 'computehop doctor'",
		)
	}
	return err
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
		Long: strings.TrimSpace(`List recent durable jobs known to this daemon.

Without --on, jobs lists jobs stored on this computer. Use --on auto to inspect
the best active paired worker, or pass a worker name/device ID to inspect that
worker directly.`),
		Example: strings.TrimSpace(`computehop jobs
computehop jobs --on auto
computehop jobs --on "Gaming PC" --limit 25`),
		Args: cobra.NoArgs,
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
				return printNoJobs(stdout, deviceSelector)
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

func printNoJobs(stdout io.Writer, deviceSelector string) error {
	selector := strings.TrimSpace(deviceSelector)
	if selector == "" {
		for _, line := range []string{
			"No jobs.",
			"",
			"Next:",
			"- Run a local test job: computehop run hostname",
			"- Test a connected worker: computehop smoke",
		} {
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
		}
		return nil
	}
	selectorArg := shellArg(selector)
	for _, line := range []string{
		"No jobs for " + deviceSelectorDisplay(selector) + ".",
		"",
		"Next:",
		"- Run a worker smoke test: computehop smoke --on " + selectorArg,
		"- Submit a remote utility job: computehop run --on " + selectorArg + " --no-project hostname",
	} {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func newCancelCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var deviceSelector string
	command := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel a durable job",
		Long: strings.TrimSpace(`Request cancellation for a queued or running durable job.

ComputeHop usually routes job-specific commands by job ID. Use --on only when
you need to choose a worker explicitly.`),
		Example: strings.TrimSpace(`computehop cancel <job-id>
computehop cancel --on "Gaming PC" <job-id>`),
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
		Long: strings.TrimSpace(`Read durable stdout and stderr for a job.

Use --follow to stream new output until the job reaches a terminal state. For
remote jobs, ComputeHop usually infers the worker from the job ID.`),
		Example: strings.TrimSpace(`computehop logs <job-id>
computehop logs --follow <job-id>
computehop logs --on "Gaming PC" <job-id>`),
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
			value, records, err := streamJobLogs(command.Context(), stdout, stderr, client, id, deviceSelector, follow)
			if err != nil {
				return err
			}
			if records > 0 {
				return nil
			}
			if value.State.Terminal() {
				_, err = fmt.Fprintf(stderr, "No output captured for %s.\n", id)
				return err
			}
			if _, err = fmt.Fprintf(stderr, "No output captured yet for %s (%s).\n", id, value.State); err != nil {
				return err
			}
			_, err = fmt.Fprintf(stderr, "Tip: wait for new output with: computehop logs --follow %s\n", id)
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
) (job.Job, uint64, error) {
	var after uint64
	var emitted uint64
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
			return job.Job{}, emitted, err
		}
		result := response.GetReadJobLogs()
		if result == nil {
			return job.Job{}, emitted, fmt.Errorf("%w: missing job logs result", ErrInvalidDaemonResponse)
		}
		for _, record := range result.GetRecords() {
			if record.GetSequence() <= after {
				return job.Job{}, emitted, fmt.Errorf("%w: job log sequence did not advance", ErrInvalidDaemonResponse)
			}
			var destination io.Writer
			switch record.GetStream() {
			case localv1.JobLogStream_JOB_LOG_STREAM_STDOUT:
				destination = stdout
			case localv1.JobLogStream_JOB_LOG_STREAM_STDERR:
				destination = stderr
			default:
				return job.Job{}, emitted, fmt.Errorf("%w: invalid job log stream", ErrInvalidDaemonResponse)
			}
			if _, err := destination.Write(record.GetData()); err != nil {
				return job.Job{}, emitted, err
			}
			after = record.GetSequence()
			emitted++
		}
		if result.GetHasMore() {
			continue
		}
		value, err := mapper.JobFromProto(result.GetJob())
		if err != nil {
			return job.Job{}, emitted, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if !follow || value.State.Terminal() {
			return value, emitted, nil
		}
		if err := waitForNextPoll(ctx); err != nil {
			return job.Job{}, emitted, err
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
	case job.ProgressPull:
		return "pull"
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
	command.Flags().StringVar(destination, "on", defaultValue, "paired worker name, device ID, or auto (best active worker)")
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
		return enrichUnavailableWorkerSubmitError(remoteError, err)
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
	return enrichUnavailableWorkerSubmitError(remoteError, err)
}

func enrichUnavailableWorkerSubmitError(remoteError *localipc.RemoteError, fallback error) error {
	if remoteError.Code != localv1.ErrorCode_ERROR_CODE_DEVICE_UNAVAILABLE ||
		!isUnavailableWorkerMessage(remoteError.Message) {
		return fallback
	}
	message := strings.TrimSpace(remoteError.Message)
	if message == "" {
		message = "paired worker is unavailable"
	}
	nextSteps := missingSubmitNextSteps(message, []submitNextStep{
		{
			text:     "Start ComputeHop on the worker and keep both devices on the same LAN.",
			required: []string{"start computehop", "same lan"},
		},
		{text: "Check worker status: computehop devices", required: []string{"computehop devices"}},
		{text: "Retry the worker test: computehop smoke", required: []string{"computehop smoke"}},
		{text: "For cross-network workers: computehop setup vps", required: []string{"computehop setup vps"}},
	})
	if len(nextSteps) == 0 {
		return errors.New(message)
	}
	return errors.New(message + "\n\nNext:\n- " + strings.Join(nextSteps, "\n- "))
}

type submitNextStep struct {
	text     string
	required []string
}

func isUnavailableWorkerMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "paired worker is unavailable") ||
		strings.Contains(lower, "remote connectivity path is unavailable") ||
		strings.Contains(lower, "is not reachable")
}

func missingSubmitNextSteps(message string, nextSteps []submitNextStep) []string {
	missing := make([]string, 0, len(nextSteps))
	lower := strings.ToLower(message)
	for _, step := range nextSteps {
		covered := true
		for _, required := range step.required {
			if !strings.Contains(lower, strings.ToLower(required)) {
				covered = false
				break
			}
		}
		if !covered {
			missing = append(missing, step.text)
		}
	}
	return missing
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
