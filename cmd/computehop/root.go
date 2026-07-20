package main

import (
	"errors"
	"fmt"
	"io"
	"net"
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

var ErrInvalidDaemonResponse = errors.New("invalid response from computehopd")

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
	root.AddCommand(newStatusCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newDevicesCommand(dependencies.stdout, dependencies.stderr, clientForCommand))
	root.AddCommand(newPairCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newUnpairCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newRunCommand(dependencies.stdout, dependencies.getwd, clientForCommand))
	root.AddCommand(newJobsCommand(dependencies.stdout, clientForCommand))
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

			writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tIDENTIFIER\tTRUST\tROLE\tADDRESS\tUPDATED"); err != nil {
				return err
			}
			for _, message := range result.GetTrustedDevices() {
				peer, err := mapper.TrustedPeerFromProto(message)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				if _, err := fmt.Fprintf(
					writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
					peer.Name, peer.DeviceID.Short(), peer.State, peer.Role, "—", peer.UpdatedAt.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			for _, nearby := range result.GetDevices() {
				presenceID, err := device.ParsePresenceID(nearby.GetPresenceId())
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				if nearby.GetTrustState() != localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED {
					return fmt.Errorf("%w: invalid nearby trust state", ErrInvalidDaemonResponse)
				}
				role := ""
				switch nearby.GetRole() {
				case localv1.DeviceRole_DEVICE_ROLE_WORKER:
					role = "worker"
				case localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR:
					role = "orchestrator"
				default:
					return fmt.Errorf("%w: invalid nearby device role", ErrInvalidDaemonResponse)
				}
				address := nearby.GetHostName()
				if len(nearby.GetAddresses()) > 0 {
					address = nearby.GetAddresses()[0]
				}
				if address != "" && nearby.GetEndpointReady() && nearby.GetPort() > 0 {
					address = net.JoinHostPort(address, strconv.FormatUint(uint64(nearby.GetPort()), 10))
				} else if address != "" {
					address += " (discovery only)"
				}
				lastSeen := time.Unix(0, nearby.GetLastSeenAtUnixNano()).UTC()
				if nearby.GetName() == "" || address == "" || nearby.GetLastSeenAtUnixNano() <= 0 {
					return fmt.Errorf("%w: incomplete nearby device", ErrInvalidDaemonResponse)
				}
				if _, err := fmt.Fprintf(
					writer,
					"%s\t%s\tunpaired\t%s\t%s\t%s\n",
					nearby.GetName(), presenceID.Short(), role, address, lastSeen.Format(time.RFC3339),
				); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
}

func newPairCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	command := &cobra.Command{
		Use:   "pair [device]",
		Short: "Start pairing or list current verification requests",
		Args:  cobra.MaximumNArgs(1),
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
		Use:   verb + " <pairing>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			var request *localv1.Request
			if confirmed {
				request = &localv1.Request{Operation: &localv1.Request_ConfirmPairing{
					ConfirmPairing: &localv1.ConfirmPairingRequest{PairingSelector: arguments[0]},
				}}
			} else {
				request = &localv1.Request{Operation: &localv1.Request_RejectPairing{
					RejectPairing: &localv1.RejectPairingRequest{PairingSelector: arguments[0]},
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
				_, err = fmt.Fprintf(stdout, "Confirmed %s locally; state: %s.\n", value.PeerName, value.State)
			} else {
				_, err = fmt.Fprintf(stdout, "Rejected pairing with %s.\n", value.PeerName)
			}
			return err
		},
	}
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
	if _, err := fmt.Fprintf(stdout, "Pairing request %s opened with %s.\n", value.ID.Short(), value.PeerName); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Verification code: %s\n", value.Verification); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "Compare this exact code on both devices. Do not confirm if it differs."); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "Confirm on this device with: computehop pair confirm %s\n", value.ID.Short())
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
			_, err = fmt.Fprintf(stdout, "computehopd %s ready\n", ping.GetDaemonVersion())
			return err
		},
	}
}

func newRunCommand(
	stdout io.Writer,
	getwd func() (string, error),
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   "run -- <program> [args...]",
		Short: "Submit a background command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			workingDirectory, err := getwd()
			if err != nil {
				return fmt.Errorf("resolve working directory: %w", err)
			}
			spec, err := mapper.SpecToProto(job.Spec{
				Executable:       arguments[0],
				Arguments:        arguments[1:],
				WorkingDirectory: workingDirectory,
				Executor:         job.ExecutorNative,
			})
			if err != nil {
				return err
			}
			client, err := clientForCommand()
			if err != nil {
				return err
			}
			response, err := client.Call(command.Context(), &localv1.Request{
				Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{Spec: spec}},
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
			_, err = fmt.Fprintf(stdout, "Submitted %s (%s)\n", value.ID, value.State)
			return err
		},
	}
}

func newJobsCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var limit uint32
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
				Operation: &localv1.Request_ListJobs{ListJobs: &localv1.ListJobsRequest{Limit: limit}},
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
			if _, err := fmt.Fprintln(writer, "ID\tSTATE\tCOMMAND\tUPDATED"); err != nil {
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
					"%s\t%s\t%s\t%s\n",
					value.ID,
					value.State,
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
	return command
}

func newCancelCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	return &cobra.Command{
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
				Operation: &localv1.Request_CancelJob{CancelJob: &localv1.CancelJobRequest{JobId: string(id)}},
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
}

func newLogsCommand(
	stdout io.Writer,
	stderr io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var follow bool
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
			var after uint64
			for {
				response, err := client.Call(command.Context(), &localv1.Request{
					Operation: &localv1.Request_ReadJobLogs{ReadJobLogs: &localv1.ReadJobLogsRequest{
						JobId:         string(id),
						AfterSequence: after,
						Limit:         32,
					}},
				})
				if err != nil {
					return err
				}
				result := response.GetReadJobLogs()
				if result == nil {
					return fmt.Errorf("%w: missing job logs result", ErrInvalidDaemonResponse)
				}
				for _, record := range result.GetRecords() {
					if record.GetSequence() <= after {
						return fmt.Errorf("%w: job log sequence did not advance", ErrInvalidDaemonResponse)
					}
					var destination io.Writer
					switch record.GetStream() {
					case localv1.JobLogStream_JOB_LOG_STREAM_STDOUT:
						destination = stdout
					case localv1.JobLogStream_JOB_LOG_STREAM_STDERR:
						destination = stderr
					default:
						return fmt.Errorf("%w: invalid job log stream", ErrInvalidDaemonResponse)
					}
					if _, err := destination.Write(record.GetData()); err != nil {
						return err
					}
					after = record.GetSequence()
				}
				if result.GetHasMore() {
					continue
				}
				value, err := mapper.JobFromProto(result.GetJob())
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDaemonResponse, err)
				}
				if !follow || value.State.Terminal() {
					return nil
				}
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-command.Context().Done():
					if !timer.Stop() {
						<-timer.C
					}
					return command.Context().Err()
				case <-timer.C:
				}
			}
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "wait for new output until the job finishes")
	return command
}
