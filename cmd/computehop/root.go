package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
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
	root.AddCommand(newRunCommand(dependencies.stdout, dependencies.getwd, clientForCommand))
	root.AddCommand(newJobsCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newCancelCommand(dependencies.stdout, clientForCommand))
	root.AddCommand(newLogsCommand(dependencies.stdout, dependencies.stderr, clientForCommand))
	return root
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
