package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/job"
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
	root.AddCommand(newUnpairCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newRunCommand(dependencies.stdout, dependencies.getwd, clientForCommand))
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
		Short: "List nearby devices discovered on this LAN",
		Args:  cobra.NoArgs,
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
				_, err = fmt.Fprintln(stdout, "No nearby devices.")
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
			if _, err := fmt.Fprintln(writer, "NAME\tIDENTIFIER\tTRUST\tROLE\tAVAILABILITY\tPATH\tADDRESS\tUPDATED"); err != nil {
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
				if peer.State == trust.StateActive && activePeerCounts[key] == 1 && len(matches) == 1 {
					matchIndex := matches[0]
					matchedNearby[matchIndex] = true
					availability = nearbyDevices[matchIndex].availability
					path = "LAN"
					address = nearbyDevices[matchIndex].address
					updatedAt = nearbyDevices[matchIndex].lastSeen
				} else if message := trustedMessages[peer.DeviceID]; message != nil {
					switch message.GetConnectivityState() {
					case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED:
						availability = "remote"
						path = remotePathLabel(message.GetConnectivityPath())
					case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTING:
						availability = "connecting"
						path = "internet"
					}
					if message.GetConnectivityUpdatedAtUnixNano() > 0 {
						updatedAt = time.Unix(0, message.GetConnectivityUpdatedAtUnixNano()).UTC()
					}
				}
				if _, err := fmt.Fprintf(
					writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					peer.Name, peer.DeviceID.Short(), peer.State, peer.Role, availability, path, address,
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
					"%s\t%s\tunpaired\t%s\t%s\tLAN\t%s\t%s\n",
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
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return printSetupGuide(stdout)
		},
	}
	command.AddCommand(newSetupVPSCommand(stdout))
	return command
}

func newSetupVPSCommand(stdout io.Writer) *cobra.Command {
	options := defaultVPSSetupOptions()
	command := &cobra.Command{
		Use:   "vps",
		Short: "Print the one-VPS deployment checklist",
		Args:  cobra.NoArgs,
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

type vpsSetupOptions struct {
	connectivityDomain string
	turnDomain         string
	email              string
	publicIP           string
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
		"1. Install the macOS menu-bar app and launch-at-login daemon:",
		"   make install-macos",
		"",
		"2. Check this computer:",
		"   computehop doctor",
		"",
		"3. Start ComputeHop on another computer on the same LAN:",
		"   go run ./cmd/computehopd --role worker --device-name \"Gaming PC\"",
		"",
		"4. Connect devices:",
		"   computehop connect",
		"   computehop connect auto",
		"   computehop connect <device>",
		"   computehop connect confirm",
		"",
		"5. Run a smoke test:",
		"   computehop run --on auto hostname",
		"",
		"After buying the VPS:",
		"   computehop setup vps",
		"   cd deploy/vps",
		"   sudo ./bootstrap-ubuntu.sh",
		"   " + vpsDefaults.initCommand(),
		"   docker compose up -d --build",
		"   ./verify.sh",
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
		"",
		"On each Mac after pairing once on the LAN:",
		"   " + options.orchestratorInstallCommand(),
		"   " + options.workerInstallCommand(),
		"",
		"Smoke test:",
		"   computehop devices",
		"   computehop run --on auto hostname",
		"",
		"Boundary:",
		"- This enables rendezvous and direct ICE/STUN paths.",
		"- Shared TURN relay use still needs short-lived credential issuance and quotas before launch.",
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
		Use:   "connect [device]",
		Short: "Connect another computer",
		Args:  cobra.MaximumNArgs(1),
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
	candidates := make([]nearbyDeviceView, 0, len(result.GetDevices()))
	for _, message := range result.GetDevices() {
		nearby, err := nearbyViewFromProto(message)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
		}
		if nearby.role == string(device.RoleWorker) {
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

func newUnpairCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   "unpair <device>",
		Short: "Revoke a paired device's pinned identity",
		Args:  cobra.ExactArgs(1),
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
			_, err = fmt.Fprintf(stdout, "Revoked %s (%s). Pair it again to restore trust.\n", peer.Name, peer.DeviceID.Short())
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
		"- To install the menu-bar app and launch-at-login daemon: make install-macos",
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
		if _, err := fmt.Fprintf(stdout, "- Run a smoke test: computehop run --on %s hostname\n", selector); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "- Watch it: computehop jobs --on %s\n", selector)
		return err
	case len(unpairedNearbyDevices) > 0:
		unpairedWorkers := make([]nearbyDeviceView, 0, len(unpairedNearbyDevices))
		for _, nearby := range unpairedNearbyDevices {
			if nearby.role == string(device.RoleWorker) {
				unpairedWorkers = append(unpairedWorkers, nearby)
			}
		}
		if len(unpairedWorkers) == 1 {
			if _, err := fmt.Fprintln(stdout, "- Connect to the nearby worker: computehop connect auto"); err != nil {
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
			"- Start ComputeHop on another computer on the same LAN: go run ./cmd/computehopd --role worker --device-name \"Gaming PC\"",
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
	command := &cobra.Command{
		Use:   "run [--on device] <program> [args...]",
		Short: "Submit a background command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if artifactDestination != "" && !fetchOutputs {
				return errors.New("--to requires --get")
			}
			if fetchOutputs && len(outputs) == 0 {
				return errors.New("--get requires at least one declared output with -o/--output")
			}
			targetDirectory := workingDirectory
			if targetDirectory == "" {
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
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{
					Spec: spec, DeviceSelector: deviceSelector,
				}},
			})
			if err != nil {
				return err
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
	command.Flags().StringVarP(
		&artifactDestination,
		"to",
		"t",
		"",
		"output destination when used with --get (defaults to the submitted working directory)",
	)
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
		Args:    cobra.ExactArgs(1),
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
	command.Flags().StringVar(destination, "on", "", "paired worker name, device ID, or auto")
	command.Flags().StringVar(destination, "device", "", "paired worker name, device ID, or auto (legacy alias for --on)")
	_ = command.Flags().MarkHidden("device")
}

func isConnectAutoSelector(selector string) bool {
	return strings.EqualFold(strings.TrimSpace(selector), "auto")
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
