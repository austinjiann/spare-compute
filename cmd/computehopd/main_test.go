package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	localv1 "github.com/austinjiann/spare-compute/gen/go/computehop/local/v1"
	"github.com/austinjiann/spare-compute/internal/device"
	"github.com/austinjiann/spare-compute/internal/infra/sqlite"
	"github.com/austinjiann/spare-compute/internal/job"
	"github.com/austinjiann/spare-compute/internal/platform/paths"
	"github.com/austinjiann/spare-compute/internal/platform/permissions"
	"github.com/austinjiann/spare-compute/internal/protocol/localipc"
	"github.com/austinjiann/spare-compute/internal/protocol/mapper"
	remoteprotocol "github.com/austinjiann/spare-compute/internal/protocol/remote"
	"github.com/austinjiann/spare-compute/internal/session"
	"github.com/austinjiann/spare-compute/internal/trust"
)

func TestRunCheckInitializesDurableState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout, stderr bytes.Buffer

	if err := run(
		context.Background(),
		[]string{"--check", "--state-dir", stateDir},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("run() error = %v; stderr = %q", err, stderr.String())
	}
	if got, want := stdout.String(), "computehopd ready\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	databasePath, err := paths.DatabasePath(stateDir)
	if err != nil {
		t.Fatalf("DatabasePath() error = %v", err)
	}
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
	identityPath, err := paths.DeviceIdentityPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("device identity was not created: %v", err)
	}
	database, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen daemon database: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close daemon database: %v", err)
	}
}

func TestRunVersionDoesNotCreateState(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	var stdout, stderr bytes.Buffer

	if err := run(
		context.Background(),
		[]string{"--version", "--state-dir", stateDir},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), version+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("version command created state directory: error = %v", err)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stateDir := shortStateDir(t)
	var stdout bytes.Buffer
	stderr := newSignalBuffer("computehopd started")
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(
			ctx,
			[]string{"--state-dir", stateDir},
			&stdout,
			stderr,
			runtimeDependencies{discovery: idleTestDiscovery{}, pairingEndpoint: idlePairingEndpointFactory},
		)
	}()

	select {
	case <-stderr.matched:
		cancel()
	case err := <-result:
		t.Fatalf("daemon exited before startup: %v; logs = %q", err, stderr.String())
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not start; logs = %q", stderr.String())
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not stop after cancellation")
	}
	if logs := stderr.String(); !strings.Contains(logs, "computehopd started") || !strings.Contains(logs, "computehopd stopped") {
		t.Fatalf("daemon lifecycle logs = %q", logs)
	}
}

func TestRunExplainsDuplicateDaemonNetworkPort(t *testing.T) {
	stateDir := shortStateDir(t)
	var stdout, stderr bytes.Buffer
	err := runWithDependencies(
		context.Background(),
		[]string{"--state-dir", stateDir},
		&stdout,
		&stderr,
		runtimeDependencies{
			pairingEndpoint: func(string, session.LocalDevice, trust.Repository) (session.Endpoint, error) {
				return nil, errors.New("listen udp :47823: bind: address already in use")
			},
		},
	)
	assertDuplicateDaemonError(t, err, "initialize pairing endpoint")
}

func TestRunExplainsDuplicateDaemonLocalIPC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows local IPC uses named pipes")
	}
	stateDir := shortStateDir(t)
	if err := permissions.EnsurePrivateDirectory(stateDir); err != nil {
		t.Fatal(err)
	}
	socketPath, err := paths.LocalSocketPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	var stdout, stderr bytes.Buffer
	err = runWithDependencies(
		context.Background(),
		[]string{"--state-dir", stateDir},
		&stdout,
		&stderr,
		runtimeDependencies{
			disableDispatcher: true,
			discovery:         idleTestDiscovery{},
			pairingEndpoint:   idlePairingEndpointFactory,
		},
	)
	assertDuplicateDaemonError(t, err, "initialize local IPC server")
}

func assertDuplicateDaemonError(t *testing.T, err error, stage string) {
	t.Helper()
	if err == nil {
		t.Fatal("run() error = nil")
	}
	for _, want := range []string{
		stage,
		"another ComputeHop daemon already appears to be running",
		"computehop status",
		"stop the existing terminal or launch agent",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestDaemonLocalIPCRoundTripPersistsJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stateDir := shortStateDir(t)
	stderr := newSignalBuffer("computehopd started")
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(
			ctx,
			[]string{"--state-dir", stateDir},
			&bytes.Buffer{},
			stderr,
			runtimeDependencies{
				disableDispatcher: true, discovery: idleTestDiscovery{}, pairingEndpoint: idlePairingEndpointFactory,
			},
		)
	}()

	select {
	case <-stderr.matched:
	case err := <-result:
		t.Fatalf("daemon exited before startup: %v; logs = %q", err, stderr.String())
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not start; logs = %q", stderr.String())
	}

	tokenPath, err := paths.CapabilityTokenPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := permissions.LoadCapabilityToken(tokenPath)
	if err != nil {
		t.Fatalf("load daemon token: %v", err)
	}
	socketPath, err := paths.LocalSocketPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	client, err := localipc.NewClient(socketPath, token)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := mapper.SpecToProto(job.Spec{
		Executable:       "echo",
		Arguments:        []string{"hello"},
		WorkingDirectory: stateDir,
		Executor:         job.ExecutorNative,
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_SubmitJob{SubmitJob: &localv1.SubmitJobRequest{Spec: spec}},
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}
	submittedJob, err := mapper.JobFromProto(submitted.GetSubmitJob().GetJob())
	if err != nil {
		t.Fatal(err)
	}
	if submittedJob.State != job.StateQueued {
		t.Fatalf("submitted state = %s, want queued", submittedJob.State)
	}

	listed, err := client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_ListJobs{ListJobs: &localv1.ListJobsRequest{Limit: 10}},
	})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(listed.GetListJobs().GetJobs()) != 1 {
		t.Fatalf("listed jobs = %d, want 1", len(listed.GetListJobs().GetJobs()))
	}

	cancelled, err := client.Call(context.Background(), &localv1.Request{
		Operation: &localv1.Request_CancelJob{CancelJob: &localv1.CancelJobRequest{
			JobId: string(submittedJob.ID),
		}},
	})
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	cancelledJob, err := mapper.JobFromProto(cancelled.GetCancelJob().GetJob())
	if err != nil {
		t.Fatal(err)
	}
	if cancelledJob.State != job.StateCancelled {
		t.Fatalf("cancelled state = %s, want cancelled", cancelledJob.State)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("daemon shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket remained after shutdown: %v", err)
	}

	databasePath, err := paths.DatabasePath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	persisted, err := database.Jobs().Get(context.Background(), submittedJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != job.StateCancelled {
		t.Fatalf("persisted state = %s, want cancelled", persisted.State)
	}
}

type idleTestDiscovery struct{}

func (idleTestDiscovery) Run(
	ctx context.Context,
	_ device.Announcement,
	_ func(device.Observation),
	ready func(),
) error {
	ready()
	<-ctx.Done()
	return nil
}

type idlePairingEndpoint struct{}

func idlePairingEndpointFactory(string, session.LocalDevice, trust.Repository) (session.Endpoint, error) {
	return idlePairingEndpoint{}, nil
}

func (idlePairingEndpoint) Port() uint16 { return 47823 }
func (idlePairingEndpoint) Run(
	ctx context.Context,
	_ func(session.PairingChannel),
	_ remoteprotocol.Handler,
) error {
	<-ctx.Done()
	return nil
}
func (idlePairingEndpoint) DialPairing(context.Context, device.NearbyDevice) (session.PairingChannel, error) {
	return nil, errors.New("unexpected test pairing dial")
}
func (idlePairingEndpoint) DialRemote(
	context.Context,
	device.NearbyDevice,
	trust.Peer,
) (remoteprotocol.Caller, error) {
	return nil, errors.New("unexpected test remote dial")
}
func (idlePairingEndpoint) Close() error { return nil }

func shortStateDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	directory, err := os.MkdirTemp(base, "ch-")
	if err != nil {
		t.Fatalf("create short state directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

type signalBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	pattern string
	matched chan struct{}
	once    sync.Once
}

func newSignalBuffer(pattern string) *signalBuffer {
	return &signalBuffer{
		pattern: pattern,
		matched: make(chan struct{}),
	}
}

func (buffer *signalBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written, err := buffer.buffer.Write(contents)
	if strings.Contains(buffer.buffer.String(), buffer.pattern) {
		buffer.once.Do(func() { close(buffer.matched) })
	}
	return written, err
}

func (buffer *signalBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"unexpected"}, &stdout, &stderr); err == nil {
		t.Fatalf("run() error = nil")
	}
}

func TestParseOptionsAcceptsFriendlyRemoteConnectivityFlags(t *testing.T) {
	parsed, err := parseOptions([]string{
		"--connectivity-url", "https://connect.example.com",
		"--stun-server", "stun:turn-a.example.com:3478",
		"--stun-server", "stun:turn-b.example.com:3478",
		"--turn-server", "turn:turn.example.com:3478?transport=udp",
		"--turn-username", "1800000000:computehop",
		"--turn-password", "secret",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.connectivityURL != "https://connect.example.com" || len(parsed.stunServers) != 2 ||
		len(parsed.turnServers) != 1 || parsed.turnUsername != "1800000000:computehop" ||
		parsed.turnPassword != "secret" {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseOptionsAcceptsExplicitLANOnlyMode(t *testing.T) {
	parsed, err := parseOptions([]string{"--lan-only"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.lanOnly || parsed.connectivityURL != "" || len(parsed.stunServers) != 0 ||
		len(parsed.turnServers) != 0 {
		t.Fatalf("parsed = %#v", parsed)
	}
}

func TestParseOptionsUsesAndValidatesFriendlyCacheSize(t *testing.T) {
	defaults, err := parseOptions(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.cacheBytes != 20<<30 {
		t.Fatalf("default cache bytes = %d", defaults.cacheBytes)
	}
	parsed, err := parseOptions([]string{"--cache-size", "1.5GiB"}, &bytes.Buffer{})
	if err != nil || parsed.cacheBytes != 3<<29 {
		t.Fatalf("cache bytes = %d, %v", parsed.cacheBytes, err)
	}
	if _, err := parseOptions([]string{"--cache-size", "512KiB"}, &bytes.Buffer{}); err == nil {
		t.Fatal("undersized cache was accepted")
	}
}

func TestParseOptionsRejectsPartialRemoteConnectivityFlags(t *testing.T) {
	if _, err := parseOptions(
		[]string{"--stun-server", "stun:turn.example.com:3478"}, &bytes.Buffer{},
	); err == nil {
		t.Fatal("standalone --stun-server was accepted")
	}
	if _, err := parseOptions(
		[]string{"--turn-server", "turn:turn.example.com:3478"}, &bytes.Buffer{},
	); err == nil {
		t.Fatal("standalone --turn-server was accepted")
	}
	if _, err := parseOptions(
		[]string{"--connectivity-url", "https://connect.example.com"}, &bytes.Buffer{},
	); err == nil {
		t.Fatal("standalone --connectivity-url was accepted")
	}
	if _, err := parseOptions([]string{"--stun-server", ""}, &bytes.Buffer{}); err == nil {
		t.Fatal("empty --stun-server was accepted")
	}
	if _, err := parseOptions([]string{
		"--connectivity-url", "https://connect.example.com",
		"--turn-server", "turn:turn.example.com:3478",
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("TURN server without credentials was accepted")
	}
	if _, err := parseOptions([]string{
		"--connectivity-url", "https://connect.example.com",
		"--stun-server", "stun:turn.example.com:3478",
		"--turn-username", "1800000000:computehop",
		"--turn-password", "secret",
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("TURN credentials without TURN server were accepted")
	}
	if _, err := parseOptions([]string{
		"--lan-only",
		"--connectivity-url", "https://connect.example.com",
		"--stun-server", "stun:turn.example.com:3478",
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("--lan-only with remote connectivity flags was accepted")
	}
}

func TestRunRejectsBlankStateDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"--check", "--state-dir", " "},
		&stdout,
		&stderr,
	); err == nil {
		t.Fatalf("run() error = nil")
	}
}

func TestRunRejectsUnsafeStateDirectoryWithoutChangingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are represented by ACLs")
	}
	stateDir := filepath.Join(t.TempDir(), "open-state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(
		context.Background(),
		[]string{"--check", "--state-dir", stateDir},
		&stdout,
		&stderr,
	); err == nil {
		t.Fatal("run() error = nil")
	}
	info, err := os.Stat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("state directory permissions changed to %o", got)
	}
}
