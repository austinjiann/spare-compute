package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
)

type diagnosticSection struct {
	name string
	body string
}

func newDiagnosticsCommand(
	stdout io.Writer,
	clientForCommand func() (caller, error),
) *cobra.Command {
	var outputPath string
	command := &cobra.Command{
		Use:   "diagnostics",
		Short: "Write a redacted diagnostics bundle",
		Long: strings.TrimSpace(`Write a redacted diagnostics zip for support or bug reports.

The bundle uses the authenticated local daemon API when possible. It includes
daemon status, device summaries, pending pairing states, and recent job
metadata. It intentionally omits raw logs, public keys, pairing verification
codes, network addresses, environment values, local IPC tokens, and full crash
report stack dumps.`),
		Example: strings.TrimSpace(`computehop diagnostics
computehop diagnostics --output ~/Desktop/computehop-diagnostics.zip`),
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			path := strings.TrimSpace(outputPath)
			if path == "" {
				path = defaultDiagnosticsPath(time.Now().UTC())
			}
			if err := writeDiagnosticsBundle(command.Context(), path, clientForCommand); err != nil {
				return err
			}
			_, err := fmt.Fprintf(stdout, "Wrote redacted diagnostics bundle: %s\n", path)
			return err
		},
	}
	command.Flags().StringVarP(&outputPath, "output", "o", "", "zip file to create")
	return command
}

func defaultDiagnosticsPath(now time.Time) string {
	return fmt.Sprintf("computehop-diagnostics-%s.zip", now.Format("20060102-150405"))
}

func writeDiagnosticsBundle(
	ctx context.Context,
	outputPath string,
	clientForCommand func() (caller, error),
) error {
	if strings.TrimSpace(outputPath) == "" {
		return errors.New("diagnostics output path is required")
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create diagnostics bundle: %w", err)
	}
	removePartial := true
	defer func() {
		if removePartial {
			_ = os.Remove(outputPath)
		}
	}()

	writer := zip.NewWriter(file)
	for _, section := range collectDiagnosticSections(ctx, clientForCommand, time.Now().UTC()) {
		if err := addDiagnosticSection(writer, section); err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("finish diagnostics bundle: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close diagnostics bundle: %w", err)
	}
	removePartial = false
	return nil
}

func addDiagnosticSection(writer *zip.Writer, section diagnosticSection) error {
	entry, err := writer.Create(section.name)
	if err != nil {
		return fmt.Errorf("create diagnostics section %s: %w", section.name, err)
	}
	body := redactDiagnosticText(section.body)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if _, err := io.WriteString(entry, body); err != nil {
		return fmt.Errorf("write diagnostics section %s: %w", section.name, err)
	}
	return nil
}

func collectDiagnosticSections(
	ctx context.Context,
	clientForCommand func() (caller, error),
	now time.Time,
) []diagnosticSection {
	sections := []diagnosticSection{
		{
			name: "README.txt",
			body: strings.TrimSpace(`ComputeHop diagnostics bundle

This bundle is intended for support and bug reports. It is redacted before
writing, but review it before sharing.

Included:
- CLI and daemon status.
- Device, pairing, and recent job metadata.
- Recent packaged-app crash report summaries when available.

Not included:
- Raw job stdout/stderr logs.
- Full crash report text or stack dumps.
- Public keys, pairing verification codes, network addresses, local IPC tokens,
  environment values, project files, artifacts, or database files.`),
		},
		{
			name: "summary.txt",
			body: fmt.Sprintf(
				"Created: %s\nCLI version: %s\n",
				now.Format(time.RFC3339),
				version,
			),
		},
	}
	sections = append(sections, collectCrashDiagnosticSection())

	client, err := clientForCommand()
	if err != nil {
		return append(sections, diagnosticSection{
			name: "daemon/status.txt",
			body: "Daemon connection: failed\nError: " + err.Error() + "\n",
		})
	}

	sections = append(sections, collectDaemonDiagnosticSection(
		ctx,
		client,
		"daemon/status.txt",
		&localv1.Request{Operation: &localv1.Request_Ping{Ping: &localv1.PingRequest{}}},
		formatDiagnosticPing,
	))
	sections = append(sections, collectDaemonDiagnosticSection(
		ctx,
		client,
		"daemon/devices.txt",
		&localv1.Request{Operation: &localv1.Request_ListDevices{ListDevices: &localv1.ListDevicesRequest{}}},
		formatDiagnosticDevices,
	))
	sections = append(sections, collectDaemonDiagnosticSection(
		ctx,
		client,
		"daemon/pairings.txt",
		&localv1.Request{Operation: &localv1.Request_ListPairings{ListPairings: &localv1.ListPairingsRequest{}}},
		formatDiagnosticPairings,
	))
	sections = append(sections, collectDaemonDiagnosticSection(
		ctx,
		client,
		"daemon/jobs.txt",
		&localv1.Request{Operation: &localv1.Request_ListJobs{ListJobs: &localv1.ListJobsRequest{Limit: 10}}},
		formatDiagnosticJobs,
	))
	return sections
}

const (
	maximumCrashReports          = 5
	maximumCrashReportReadBytes  = 256 * 1024
	crashReportDirectoryOverride = "COMPUTEHOP_CRASH_REPORT_DIR"
)

type crashReportCandidate struct {
	path    string
	name    string
	size    int64
	modTime time.Time
}

func collectCrashDiagnosticSection() diagnosticSection {
	directories := crashReportDirectories()
	var builder strings.Builder
	if len(directories) == 0 {
		builder.WriteString("Crash report scan: skipped on this platform\n")
		return diagnosticSection{name: "app/crash-reports.txt", body: builder.String()}
	}

	builder.WriteString("Crash report scan: enabled\n")
	for _, directory := range directories {
		fmt.Fprintf(&builder, "Directory: %s\n", directory)
	}

	reports := findCrashReports(directories)
	fmt.Fprintf(&builder, "Reports found: %d\n", len(reports))
	if len(reports) == 0 {
		builder.WriteString("\nNo recent ComputeHop crash reports found.\n")
		return diagnosticSection{name: "app/crash-reports.txt", body: builder.String()}
	}

	if len(reports) > maximumCrashReports {
		reports = reports[:maximumCrashReports]
	}
	for index, report := range reports {
		fmt.Fprintf(&builder, "\nReport %d:\n", index+1)
		fmt.Fprintf(&builder, "  component: %s\n", crashReportComponent(report.name))
		fmt.Fprintf(&builder, "  format: %s\n", strings.TrimPrefix(filepath.Ext(report.name), "."))
		fmt.Fprintf(&builder, "  modified: %s\n", report.modTime.UTC().Format(time.RFC3339))
		fmt.Fprintf(&builder, "  size bytes: %d\n", report.size)
		summary := crashReportSummary(report.path)
		if summary == "" {
			builder.WriteString("  summary: no standard summary lines found; full crash text omitted\n")
			continue
		}
		builder.WriteString("  summary:\n")
		for _, line := range strings.Split(summary, "\n") {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&builder, "    %s\n", line)
			}
		}
	}
	builder.WriteString("\nFull crash report text and stack dumps are omitted by default.\n")
	return diagnosticSection{name: "app/crash-reports.txt", body: builder.String()}
}

func crashReportDirectories() []string {
	if override := strings.TrimSpace(os.Getenv(crashReportDirectoryOverride)); override != "" {
		return []string{override}
	}
	if runtime.GOOS != "darwin" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, "Library", "Logs", "DiagnosticReports"),
		filepath.Join(home, "Library", "Logs", "CrashReporter"),
	}
}

func findCrashReports(directories []string) []crashReportCandidate {
	var reports []crashReportCandidate
	seen := make(map[string]struct{})
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !isComputeHopCrashReportName(entry.Name()) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if _, ok := seen[path]; ok {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[path] = struct{}{}
			reports = append(reports, crashReportCandidate{
				path:    path,
				name:    entry.Name(),
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}
	}
	sort.Slice(reports, func(left, right int) bool {
		if !reports[left].modTime.Equal(reports[right].modTime) {
			return reports[left].modTime.After(reports[right].modTime)
		}
		return reports[left].name < reports[right].name
	})
	return reports
}

func isComputeHopCrashReportName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if !(strings.HasPrefix(lower, "computehop") || strings.HasPrefix(lower, "compute hop")) {
		return false
	}
	return strings.HasSuffix(lower, ".crash") ||
		strings.HasSuffix(lower, ".ips") ||
		strings.HasSuffix(lower, ".diag")
}

func crashReportComponent(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "control center"):
		return "ComputeHop Control Center"
	case strings.HasPrefix(lower, "computehopd"):
		return "computehopd"
	case strings.HasPrefix(lower, "computehop"):
		return "ComputeHop"
	default:
		return "ComputeHop component"
	}
}

func crashReportSummary(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumCrashReportReadBytes))
	if err != nil {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		cleaned := strings.TrimRight(line, "\r")
		if crashReportSummaryLine(cleaned) {
			lines = append(lines, strings.TrimSpace(cleaned))
		}
		if len(lines) >= 24 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func crashReportSummaryLine(line string) bool {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{
		"Process:",
		"Identifier:",
		"Version:",
		"Code Type:",
		"Date/Time:",
		"OS Version:",
		"Crashed Thread:",
		"Triggered by Thread:",
		"Exception Type:",
		"Exception Codes:",
		"Exception Note:",
		"Termination Reason:",
		"Application Specific Information:",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func collectDaemonDiagnosticSection(
	ctx context.Context,
	client caller,
	name string,
	request *localv1.Request,
	format func(*localv1.Response) (string, error),
) diagnosticSection {
	response, err := client.Call(ctx, request)
	if err != nil {
		return diagnosticSection{name: name, body: "Error: " + err.Error() + "\n"}
	}
	body, err := format(response)
	if err != nil {
		body = "Error: " + err.Error() + "\n"
	}
	return diagnosticSection{name: name, body: body}
}

func formatDiagnosticPing(response *localv1.Response) (string, error) {
	ping := response.GetPing()
	if ping == nil {
		return "", fmt.Errorf("%w: missing ping result", ErrInvalidDaemonResponse)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Daemon: ok\n")
	fmt.Fprintf(&builder, "Daemon version: %s\n", ping.GetDaemonVersion())
	fmt.Fprintf(&builder, "Device ID: %s\n", shortDiagnosticID(ping.GetDeviceId()))
	fmt.Fprintf(&builder, "Device name: %s\n", ping.GetDeviceName())
	fmt.Fprintf(&builder, "Role: %s\n", diagnosticRoleLabel(ping.GetRole()))
	fmt.Fprintf(&builder, "Platform: %s/%s\n", ping.GetPlatform(), ping.GetArch())
	fmt.Fprintf(&builder, "CPU count: %d\n", ping.GetLogicalCpuCount())
	fmt.Fprintf(&builder, "Memory bytes: %d\n", ping.GetTotalMemoryBytes())
	fmt.Fprintf(&builder, "Tools: %s\n", diagnosticStringList(ping.GetToolIds()))
	fmt.Fprintf(&builder, "Executors: %s\n", diagnosticExecutorList(ping.GetSupportedExecutors()))
	return builder.String(), nil
}

func formatDiagnosticDevices(response *localv1.Response) (string, error) {
	devices := response.GetListDevices()
	if devices == nil {
		return "", fmt.Errorf("%w: missing device list result", ErrInvalidDaemonResponse)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "LAN discovery: %s\n", diagnosticDiscoveryLabel(devices.GetDiscoveryState()))
	if devices.GetDiscoveryError() != "" {
		fmt.Fprintf(&builder, "Discovery error: %s\n", devices.GetDiscoveryError())
	}
	fmt.Fprintf(&builder, "Trusted devices: %d\n", len(devices.GetTrustedDevices()))
	for _, value := range devices.GetTrustedDevices() {
		fmt.Fprintf(
			&builder,
			"- %s (%s, %s, %s)\n",
			value.GetName(),
			diagnosticRoleLabel(value.GetRole()),
			diagnosticTrustLabel(value.GetTrustState()),
			shortDiagnosticID(value.GetDeviceId()),
		)
		if value.GetConnectivityState() != localv1.ConnectivityState_CONNECTIVITY_STATE_UNSPECIFIED {
			fmt.Fprintf(&builder, "  connectivity: %s\n", diagnosticConnectivityLabel(value.GetConnectivityState()))
		}
		if value.GetConnectivityPath() != "" {
			fmt.Fprintf(&builder, "  path: %s\n", remotePathLabel(value.GetConnectivityPath()))
		}
		if value.GetConnectivityError() != "" {
			fmt.Fprintf(&builder, "  connectivity error: %s\n", value.GetConnectivityError())
		}
		if value.GetPlatform() != "" || value.GetArch() != "" {
			fmt.Fprintf(&builder, "  platform: %s/%s\n", value.GetPlatform(), value.GetArch())
		}
		if value.GetLogicalCpuCount() > 0 {
			fmt.Fprintf(&builder, "  cpu count: %d\n", value.GetLogicalCpuCount())
		}
		if value.GetTotalMemoryBytes() > 0 {
			fmt.Fprintf(&builder, "  memory bytes: %d\n", value.GetTotalMemoryBytes())
		}
		if len(value.GetToolIds()) > 0 {
			fmt.Fprintf(&builder, "  tools: %s\n", diagnosticStringList(value.GetToolIds()))
		}
		if len(value.GetSupportedExecutors()) > 0 {
			fmt.Fprintf(&builder, "  executors: %s\n", diagnosticExecutorList(value.GetSupportedExecutors()))
		}
	}
	fmt.Fprintf(&builder, "Nearby unpaired devices: %d\n", len(devices.GetDevices()))
	for _, value := range devices.GetDevices() {
		fmt.Fprintf(
			&builder,
			"- %s (%s, %s)\n",
			value.GetName(),
			diagnosticRoleLabel(value.GetRole()),
			shortDiagnosticID(value.GetPresenceId()),
		)
		fmt.Fprintf(&builder, "  endpoint ready: %t\n", value.GetEndpointReady())
		if value.GetPlatform() != "" || value.GetArch() != "" {
			fmt.Fprintf(&builder, "  platform: %s/%s\n", value.GetPlatform(), value.GetArch())
		}
	}
	fmt.Fprintf(&builder, "\nNetwork addresses, public keys, and pairing codes are omitted.\n")
	return builder.String(), nil
}

func formatDiagnosticPairings(response *localv1.Response) (string, error) {
	pairings := response.GetListPairings()
	if pairings == nil {
		return "", fmt.Errorf("%w: missing pairing list result", ErrInvalidDaemonResponse)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Pending pairings: %d\n", len(pairings.GetPairings()))
	for _, value := range pairings.GetPairings() {
		fmt.Fprintf(
			&builder,
			"- %s (%s, %s)\n",
			value.GetPeerName(),
			diagnosticRoleLabel(value.GetPeerRole()),
			shortDiagnosticID(value.GetPeerDeviceId()),
		)
		fmt.Fprintf(&builder, "  id: %s\n", shortDiagnosticID(value.GetId()))
		fmt.Fprintf(&builder, "  direction: %s\n", diagnosticPairingDirectionLabel(value.GetDirection()))
		fmt.Fprintf(&builder, "  state: %s\n", diagnosticPairingStateLabel(value.GetState()))
		fmt.Fprintf(&builder, "  confirmed: local=%t remote=%t\n", value.GetLocalConfirmed(), value.GetRemoteConfirmed())
		if value.GetFailure() != "" {
			fmt.Fprintf(&builder, "  failure: %s\n", value.GetFailure())
		}
	}
	fmt.Fprintf(&builder, "\nPairing verification codes and public keys are omitted.\n")
	return builder.String(), nil
}

func formatDiagnosticJobs(response *localv1.Response) (string, error) {
	jobs := response.GetListJobs()
	if jobs == nil {
		return "", fmt.Errorf("%w: missing job list result", ErrInvalidDaemonResponse)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Recent jobs: %d\n", len(jobs.GetJobs()))
	for _, value := range jobs.GetJobs() {
		spec := value.GetSpec()
		fmt.Fprintf(&builder, "- %s\n", shortDiagnosticID(value.GetId()))
		fmt.Fprintf(&builder, "  state: %s\n", diagnosticJobStateLabel(value.GetState()))
		if value.GetUpdatedAtUnixNano() > 0 {
			fmt.Fprintf(&builder, "  updated: %s\n", time.Unix(0, value.GetUpdatedAtUnixNano()).UTC().Format(time.RFC3339))
		}
		if spec != nil {
			fmt.Fprintf(&builder, "  executor: %s\n", diagnosticExecutorLabel(spec.GetExecutor()))
			fmt.Fprintf(&builder, "  command: %s\n", shellArgs(append([]string{spec.GetExecutable()}, spec.GetArguments()...)))
			if spec.GetWorkingDirectory() != "" {
				fmt.Fprintf(&builder, "  working directory: %s\n", spec.GetWorkingDirectory())
			}
			if spec.GetContainerImage() != "" {
				fmt.Fprintf(&builder, "  container image: omitted\n")
			}
			if len(spec.GetOutputs()) > 0 {
				fmt.Fprintf(&builder, "  outputs: %s\n", diagnosticStringList(spec.GetOutputs()))
			}
			if len(spec.GetRequiredToolIds()) > 0 {
				fmt.Fprintf(&builder, "  required tools: %s\n", diagnosticStringList(spec.GetRequiredToolIds()))
			}
			if len(spec.GetEnvironment()) > 0 {
				fmt.Fprintf(&builder, "  environment: omitted (%d values)\n", len(spec.GetEnvironment()))
			}
		}
		if failure := value.GetFailure(); failure != nil {
			fmt.Fprintf(&builder, "  failure code: %s\n", failure.GetCode())
			fmt.Fprintf(&builder, "  failure message: %s\n", failure.GetMessage())
			fmt.Fprintf(&builder, "  retryable: %t\n", failure.GetRetryable())
		}
	}
	fmt.Fprintf(&builder, "\nRaw job logs are omitted because commands can print secrets.\n")
	return builder.String(), nil
}

func diagnosticStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return "none"
	}
	sort.Strings(cleaned)
	return strings.Join(cleaned, ", ")
}

func diagnosticExecutorList(values []localv1.Executor) string {
	if len(values) == 0 {
		return "none"
	}
	labels := make([]string, 0, len(values))
	for _, value := range values {
		label := diagnosticExecutorLabel(value)
		if label != "unspecified" {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

func diagnosticRoleLabel(value localv1.DeviceRole) string {
	switch value {
	case localv1.DeviceRole_DEVICE_ROLE_ORCHESTRATOR:
		return "orchestrator"
	case localv1.DeviceRole_DEVICE_ROLE_WORKER:
		return "worker"
	default:
		return "unspecified"
	}
}

func diagnosticTrustLabel(value localv1.DeviceTrustState) string {
	switch value {
	case localv1.DeviceTrustState_DEVICE_TRUST_STATE_UNPAIRED:
		return "unpaired"
	case localv1.DeviceTrustState_DEVICE_TRUST_STATE_PAIRED:
		return "paired"
	case localv1.DeviceTrustState_DEVICE_TRUST_STATE_REVOKED:
		return "revoked"
	default:
		return "unspecified"
	}
}

func diagnosticDiscoveryLabel(value localv1.DiscoveryState) string {
	switch value {
	case localv1.DiscoveryState_DISCOVERY_STATE_STARTING:
		return "starting"
	case localv1.DiscoveryState_DISCOVERY_STATE_AVAILABLE:
		return "available"
	case localv1.DiscoveryState_DISCOVERY_STATE_UNAVAILABLE:
		return "unavailable"
	default:
		return "unspecified"
	}
}

func diagnosticConnectivityLabel(value localv1.ConnectivityState) string {
	switch value {
	case localv1.ConnectivityState_CONNECTIVITY_STATE_DISABLED:
		return "disabled"
	case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTING:
		return "connecting"
	case localv1.ConnectivityState_CONNECTIVITY_STATE_CONNECTED:
		return "connected"
	case localv1.ConnectivityState_CONNECTIVITY_STATE_UNAVAILABLE:
		return "unavailable"
	default:
		return "unspecified"
	}
}

func diagnosticPairingDirectionLabel(value localv1.PairingDirection) string {
	switch value {
	case localv1.PairingDirection_PAIRING_DIRECTION_OUTBOUND:
		return "outbound"
	case localv1.PairingDirection_PAIRING_DIRECTION_INBOUND:
		return "inbound"
	default:
		return "unspecified"
	}
}

func diagnosticPairingStateLabel(value localv1.PairingState) string {
	switch value {
	case localv1.PairingState_PAIRING_STATE_WAITING:
		return "waiting"
	case localv1.PairingState_PAIRING_STATE_PAIRED:
		return "paired"
	case localv1.PairingState_PAIRING_STATE_REJECTED:
		return "rejected"
	case localv1.PairingState_PAIRING_STATE_EXPIRED:
		return "expired"
	case localv1.PairingState_PAIRING_STATE_FAILED:
		return "failed"
	default:
		return "unspecified"
	}
}

func diagnosticJobStateLabel(value localv1.JobState) string {
	switch value {
	case localv1.JobState_JOB_STATE_CREATED:
		return "created"
	case localv1.JobState_JOB_STATE_VALIDATING:
		return "validating"
	case localv1.JobState_JOB_STATE_QUEUED:
		return "queued"
	case localv1.JobState_JOB_STATE_SNAPSHOTTING:
		return "snapshotting"
	case localv1.JobState_JOB_STATE_TRANSFERRING:
		return "transferring"
	case localv1.JobState_JOB_STATE_STARTING:
		return "starting"
	case localv1.JobState_JOB_STATE_RUNNING:
		return "running"
	case localv1.JobState_JOB_STATE_COLLECTING:
		return "collecting"
	case localv1.JobState_JOB_STATE_RESTORING:
		return "restoring"
	case localv1.JobState_JOB_STATE_SUCCEEDED:
		return "succeeded"
	case localv1.JobState_JOB_STATE_FAILED:
		return "failed"
	case localv1.JobState_JOB_STATE_CANCELLED:
		return "cancelled"
	case localv1.JobState_JOB_STATE_REJECTED:
		return "rejected"
	case localv1.JobState_JOB_STATE_LOST:
		return "lost"
	default:
		return "unspecified"
	}
}

func diagnosticExecutorLabel(value localv1.Executor) string {
	switch value {
	case localv1.Executor_EXECUTOR_NATIVE:
		return "native"
	case localv1.Executor_EXECUTOR_CONTAINER:
		return "container"
	default:
		return "unspecified"
	}
}

func shortDiagnosticID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

type diagnosticRedactor struct {
	pattern     *regexp.Regexp
	replacement string
}

var diagnosticRedactors = []diagnosticRedactor{
	{
		pattern:     regexp.MustCompile(`(?i)(--(?:turn-password|password|token|secret|api-key|openai-api-key)(?:=|\s+))("[^"]*"|'[^']*'|[^\s]+)`),
		replacement: "${1}[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|secret|token|api[_-]?key|authorization)[a-z0-9_.-]*(?:\s*[:=]\s*|\s+))("[^"]*"|'[^']*'|[^\s,}]+)`),
		replacement: "${1}[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`(?i)(https?://[^/\s:@]+:)[^@\s/]+(@)`),
		replacement: "${1}[redacted]${2}",
	},
}

func redactDiagnosticText(value string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		value = strings.ReplaceAll(value, home, "~")
	}
	for _, redactor := range diagnosticRedactors {
		value = redactor.pattern.ReplaceAllString(value, redactor.replacement)
	}
	return value
}
